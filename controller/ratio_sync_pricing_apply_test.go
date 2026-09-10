package controller

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupRatioSyncPricingApplyTest 为上游价格同步测试建立独立 sqlite 与空定价配置，
// 并完整恢复被替换的全局状态（含 InitLogDB 顺带修改的 LOG_DB 与日志库方言）。
func setupRatioSyncPricingApplyTest(t *testing.T) {
	t.Helper()
	oldDB := model.DB
	oldLOGDB := model.LOG_DB
	oldLogDatabaseType := common.LogDatabaseType()
	dsn := fmt.Sprintf("file:ratio-sync-pricing-apply-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	model.DB = db

	// controller 包无法直接调用 model.initCol，借道 InitLogDB 完成方言列名初始化
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())

	common.OptionMapRWMutex.Lock()
	oldOptionMap := common.OptionMap
	common.OptionMap = map[string]string{
		"ModelRatio":                   `{}`,
		"CompletionRatio":              `{}`,
		"CacheRatio":                   `{}`,
		"CreateCacheRatio":             `{}`,
		"ImageRatio":                   `{}`,
		"AudioRatio":                   `{}`,
		"AudioCompletionRatio":         `{}`,
		"ModelPrice":                   `{}`,
		"billing_setting.billing_mode": `{}`,
		"billing_setting.billing_expr": `{}`,
		"ModelPricingLocks":            `{}`,
	}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLOGDB
		common.SetLogDatabaseType(oldLogDatabaseType)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
}

// 实证行为契约：
// 1. 本地某模型完全未设置价格；
// 2. 上游同时提供倍率与表达式计费；
// 3. 前端因“表达式优先”规则实际发送的表达式计费分辨率被应用；
// 4. 重新拉取上游差异时，基础倍率仍未设置的模型不应从差异列表中消失。
func TestUpstreamSyncExprApplyKeepsModelInDifferences(t *testing.T) {
	setupRatioSyncPricingApplyTest(t)

	const modelName = "gemini-3.1-pro-preview"
	const expr = `len <= 200000 ? tier("0_200k", p * 2 + c * 12 + cr * 0.2) : tier("512k_plus", p * 4 + c * 18 + cr * 0.4)`

	// 模拟官方倍率预设渠道返回的数据（模型同时具备倍率与表达式）
	upstreamChannels := []struct {
		name string
		data map[string]any
	}{
		{
			name: "官方倍率预设(-100)",
			data: map[string]any{
				"model_ratio":      map[string]any{modelName: 1.0},
				"completion_ratio": map[string]any{modelName: 6.0},
				"cache_ratio":      map[string]any{modelName: 0.1},
				"billing_mode":     map[string]any{modelName: "tiered_expr"},
				"billing_expr":     map[string]any{modelName: expr},
			},
		},
	}

	// 第一次拉取：本地未设置，模型应出现在差异里
	diffs := buildDifferences(model.GetPricingSyncRuntimeData(), upstreamChannels)
	require.Contains(t, diffs, modelName, "本地未设置价格时，模型应出现在上游差异中")

	// 应用前端实际发送的表达式计费分辨率（前端“表达式优先”规则导致）
	resolutions := map[string]map[string]any{
		modelName: {
			"billing_mode": "tiered_expr",
			"billing_expr": expr,
		},
	}
	result, err := model.ApplyModelPricingSync(resolutions)
	require.NoError(t, err)
	assert.Equal(t, []string{modelName}, result.AppliedModels)

	// 第二次拉取：模型仍处于“基础价格未设置”状态，必须继续出现在差异中
	diffs = buildDifferences(model.GetPricingSyncRuntimeData(), upstreamChannels)
	require.Contains(t, diffs, modelName, "应用表达式计费后基础倍率仍未设置，模型不应从差异中消失")

	// 诊断性断言：表达式字段已与上游一致，证明模型仍在差异中的原因是基础倍率未写入
	modelDiffs, exists := diffs[modelName]
	require.True(t, exists)
	for _, field := range []string{"billing_mode", "billing_expr"} {
		assert.NotContains(t, modelDiffs, field, "表达式字段应已与上游一致而不出现在差异中")
	}
	assert.Contains(t, modelDiffs, "model_ratio", "基础倍率未设置时必须保留在差异中")
}

// 上游仅提供辅助倍率（如补全倍率）时，应用同步后所有上游字段均已一致，
// 模型从差异列表中消失属于预期行为（价格已成功写入，并非丢失）。
func TestUpstreamSyncAuxRatioApplyRemovesModelFromDifferences(t *testing.T) {
	setupRatioSyncPricingApplyTest(t)

	const modelName = "aux-only-model"
	upstreamChannels := []struct {
		name string
		data map[string]any
	}{
		{
			name: "官方倍率预设(-100)",
			data: map[string]any{
				"completion_ratio": map[string]any{modelName: 4.0},
			},
		},
	}

	diffs := buildDifferences(model.GetPricingSyncRuntimeData(), upstreamChannels)
	require.Contains(t, diffs, modelName, "本地未设置补全倍率时，模型应出现在差异中")

	result, err := model.ApplyModelPricingSync(map[string]map[string]any{
		modelName: {"completion_ratio": 4.0},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{modelName}, result.AppliedModels)

	diffs = buildDifferences(model.GetPricingSyncRuntimeData(), upstreamChannels)
	assert.NotContains(t, diffs, modelName, "上游仅有补全倍率且已同步一致后，模型从差异中消失是预期行为")
}
