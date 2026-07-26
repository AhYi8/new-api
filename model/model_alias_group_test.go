package model

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelAliasGroupTest(t *testing.T) {
	t.Helper()
	oldDB := DB
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldMainDatabaseType := common.MainDatabaseType()
	oldLogDatabaseType := common.LogDatabaseType()

	dsn := fmt.Sprintf("file:model-alias-group-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}, &Channel{}, &Ability{}))
	DB = db
	common.MemoryCacheEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	initCol()

	common.OptionMapRWMutex.Lock()
	oldOptionMap := common.OptionMap
	testOptionMap := map[string]string{
		ModelAliasGroupsOptionKey:        "[]",
		ModelAliasScanEnabledOptionKey:   "true",
		ModelAliasScanIntervalOptionKey:  "30",
		ModelAliasPendingCountsOptionKey: "{}",
		modelAliasScanRevisionOptionKey:  "",
		ModelPricingLocksOptionKey:       "{}",
		"ModelPrice":                     "{}",
		"ModelRatio":                     `{"alias":1,"alias-a":1,"alias-b":1,"deepseek-v4-pro":1}`,
		"CompletionRatio":                "{}",
		"CacheRatio":                     "{}",
		"CreateCacheRatio":               "{}",
		"ImageRatio":                     "{}",
		"AudioRatio":                     "{}",
		"AudioCompletionRatio":           "{}",
		"billing_setting.billing_mode":   "{}",
		"billing_setting.billing_expr":   "{}",
	}
	common.OptionMap = testOptionMap
	common.OptionMapRWMutex.Unlock()
	testPricingOptions := make(map[string]string, len(modelPricingSyncOptionKeys))
	for _, key := range modelPricingSyncOptionKeys {
		testPricingOptions[key] = testOptionMap[key]
	}
	require.NoError(t, publishModelPricingOptions(testPricingOptions))

	t.Cleanup(func() {
		DB = oldDB
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		common.SetDatabaseTypes(oldMainDatabaseType, oldLogDatabaseType)
		initCol()
		restoredPricingOptions := make(map[string]string, len(modelPricingSyncOptionKeys))
		for _, key := range modelPricingSyncOptionKeys {
			restoredPricingOptions[key] = oldOptionMap[key]
			if restoredPricingOptions[key] == "" {
				restoredPricingOptions[key] = "{}"
			}
		}
		require.NoError(t, publishModelPricingOptions(restoredPricingOptions))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
}

func TestNormalizeModelAliasGroupsValidatesAndDeduplicates(t *testing.T) {
	normalized, err := NormalizeModelAliasGroups([]ModelAliasGroup{
		{Alias: " deepseek-v4 ", Models: []string{"vendor/model", " vendor/model ", "Vendor/Model"}},
	})
	require.NoError(t, err)
	require.Len(t, normalized, 1)
	assert.Equal(t, "deepseek-v4", normalized[0].Alias)
	assert.Equal(t, []string{"vendor/model", "Vendor/Model"}, normalized[0].Models)

	testCases := []struct {
		name   string
		groups []ModelAliasGroup
	}{
		{name: "空统一名称", groups: []ModelAliasGroup{{Models: []string{"vendor/model"}}}},
		{name: "没有供应商名称", groups: []ModelAliasGroup{{Alias: "alias"}}},
		{name: "统一名称作为供应商名称", groups: []ModelAliasGroup{{Alias: "alias", Models: []string{"alias"}}}},
		{
			name: "跨组重复供应商名称",
			groups: []ModelAliasGroup{
				{Alias: "alias-a", Models: []string{"vendor/model"}},
				{Alias: "alias-b", Models: []string{"vendor/model"}},
			},
		},
		{
			name: "供应商名称与其他统一名称重复",
			groups: []ModelAliasGroup{
				{Alias: "alias-a", Models: []string{"vendor/model"}},
				{Alias: "vendor/model", Models: []string{"vendor/other"}},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NormalizeModelAliasGroups(testCase.groups)
			assert.Error(t, err)
		})
	}
}

func TestFilterModelAliasCatalogMatchesNamesAndExcludesExactAlias(t *testing.T) {
	pricing := []Pricing{
		{ModelName: "deepseek-v4-pro"},
		{ModelName: "nvidia/DeepSeek-V4-Pro"},
		{ModelName: "deepseek-ai/deepseek-v4-pro"},
		{ModelName: "deepseek-ai/deepseek-v4-pro"},
		{ModelName: "unrelated-model", Description: "deepseek-v4-pro"},
		{ModelName: "  "},
	}

	matched := filterModelAliasCatalog(pricing, "deepseek-v4-pro")

	assert.Equal(t, []string{
		"deepseek-ai/deepseek-v4-pro",
		"nvidia/DeepSeek-V4-Pro",
	}, matched)
}

func TestSearchModelAliasCatalogValidatesKeyword(t *testing.T) {
	_, err := SearchModelAliasCatalog("  ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "统一名称不能为空")

	_, err = SearchModelAliasCatalog(strings.Repeat("a", maxModelAliasNameLength+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不能超过")
}

func TestParseModelAliasChannelMappingRejectsNull(t *testing.T) {
	raw := "null"
	_, err := parseModelAliasChannelMapping(&raw)
	assert.EqualError(t, err, "模型映射必须是 JSON 对象")
}

func TestClassifyModelAliasChannelRejectsMappingCycle(t *testing.T) {
	group := ModelAliasGroup{Alias: "alias", Models: []string{"vendor/model"}}
	channel := newModelAliasTestChannel("环形映射", "vendor/model", map[string]string{
		"vendor/model": "alias",
	})

	item, _ := classifyModelAliasChannel(channel, group)

	assert.Equal(t, ModelAliasPreviewStatusConflict, item.Status)
	assert.Equal(t, "mapping_target_conflict", item.Reason)
}

func TestModelAliasGroupPreviewAndApply(t *testing.T) {
	setupModelAliasGroupTest(t)
	groups, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)
	require.Len(t, groups, 1)

	channels := []*Channel{
		newModelAliasTestChannel("新增", "vendor/a", map[string]string{"other": "target"}),
		newModelAliasTestChannel("已一致", "vendor/b,deepseek-v4-pro", map[string]string{"deepseek-v4-pro": "vendor/b"}),
		newModelAliasTestChannel("更新旧目标", "vendor/b", map[string]string{"deepseek-v4-pro": "vendor/a"}),
		newModelAliasTestChannel("多个目标", "vendor/a,vendor/b", nil),
		newModelAliasTestChannel("组外冲突", "vendor/a", map[string]string{"deepseek-v4-pro": "outside/model"}),
		newModelAliasTestChannel("别名已是直接模型", "vendor/a,deepseek-v4-pro", nil),
		newModelAliasTestChannel("大小写不匹配", "Vendor/A", nil),
		newModelAliasTestChannelWithRawMapping("无效映射", "vendor/a", "{"),
		newModelAliasTestChannel("只缺别名", "vendor/a", map[string]string{"deepseek-v4-pro": "vendor/a"}),
	}
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(DB))
	}

	preview, err := PreviewModelAliasGroup("deepseek-v4-pro")
	require.NoError(t, err)
	assert.Equal(t, 2, preview.Counts[ModelAliasPreviewStatusNew])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusUnchanged])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusUpdated])
	assert.Equal(t, 3, preview.Counts[ModelAliasPreviewStatusConflict])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusMultipleMatches])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusUnmatched])

	result, err := ApplyModelAliasGroup("deepseek-v4-pro")
	require.NoError(t, err)
	assert.Equal(t, 3, result.Applied)
	assert.Equal(t, 6, result.Skipped)
	assert.Empty(t, result.Failed)

	assertModelAliasChannel(t, channels[0].Id, "vendor/a", true)
	assertModelAliasChannel(t, channels[2].Id, "vendor/b", true)
	assertModelAliasChannel(t, channels[8].Id, "vendor/a", true)

	var newChannelMapping map[string]string
	var newChannel Channel
	require.NoError(t, DB.First(&newChannel, channels[0].Id).Error)
	require.NoError(t, common.UnmarshalJsonStr(*newChannel.ModelMapping, &newChannelMapping))
	assert.Equal(t, "target", newChannelMapping["other"])

	var conflictChannel Channel
	require.NoError(t, DB.First(&conflictChannel, channels[4].Id).Error)
	assert.NotContains(t, conflictChannel.Models, "deepseek-v4-pro")
	var conflictMapping map[string]string
	require.NoError(t, common.UnmarshalJsonStr(*conflictChannel.ModelMapping, &conflictMapping))
	assert.Equal(t, "outside/model", conflictMapping["deepseek-v4-pro"])

	secondResult, err := ApplyModelAliasGroup("deepseek-v4-pro")
	require.NoError(t, err)
	assert.Zero(t, secondResult.Applied)
	assert.Equal(t, len(channels), secondResult.Skipped)
	assert.Empty(t, secondResult.Failed)
}

func TestApplyModelAliasGroupWithSelectionAppliesOnlySelectedChannels(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)

	channels := []*Channel{
		newModelAliasTestChannel("渠道 A", "vendor/a", nil),
		newModelAliasTestChannel("渠道 B", "vendor/b", nil),
	}
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(DB))
	}

	result, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channels[1].Id},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Applied)
	assert.Equal(t, 1, result.Skipped)
	assert.Empty(t, result.Failed)

	assertModelAliasChannel(t, channels[1].Id, "vendor/b", true)
	var skippedChannel Channel
	require.NoError(t, DB.First(&skippedChannel, channels[0].Id).Error)
	assert.NotContains(t, skippedChannel.GetModels(), "deepseek-v4-pro")
}

func TestApplyModelAliasGroupWithSelectionAppliesSelectedMultipleMatchTarget(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)
	channel := newModelAliasTestChannel("多匹配", "vendor/a,vendor/b", nil)
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))

	result, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channel.Id},
		TargetModels:       map[int]string{channel.Id: "vendor/b"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Applied)
	assert.Zero(t, result.Skipped)
	assert.Empty(t, result.Failed)
	assertModelAliasChannel(t, channel.Id, "vendor/b", true)

	preview, err := PreviewModelAliasGroup("deepseek-v4-pro")
	require.NoError(t, err)
	require.Len(t, preview.Items, 1)
	assert.Equal(t, ModelAliasPreviewStatusMultipleMatches, preview.Items[0].Status)
	assert.Equal(t, "vendor/b", preview.Items[0].CurrentTarget)
	assert.Empty(t, preview.Items[0].ProposedTarget)
	assert.Zero(t, preview.Counts[ModelAliasPreviewStatusUnchanged])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusMultipleMatches])

	result, err = ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channel.Id},
		TargetModels:       map[int]string{channel.Id: "vendor/a"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Applied)
	assert.Zero(t, result.Skipped)
	assert.Empty(t, result.Failed)
	assertModelAliasChannel(t, channel.Id, "vendor/a", true)
}

func TestApplyModelAliasGroupWithSelectionRejectsInvalidTarget(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)
	channel := newModelAliasTestChannel("多匹配", "vendor/a,vendor/b", nil)
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))

	result, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channel.Id},
		TargetModels:       map[int]string{channel.Id: "outside/model"},
	})
	require.NoError(t, err)
	assert.Zero(t, result.Applied)
	assert.Zero(t, result.Skipped)
	require.Len(t, result.Failed, 1)
	assert.Contains(t, result.Failed[0].Error, "目标模型不在匹配结果中")

	var unchanged Channel
	require.NoError(t, DB.First(&unchanged, channel.Id).Error)
	assert.NotContains(t, unchanged.GetModels(), "deepseek-v4-pro")
}

func TestApplyModelAliasGroupWithSelectionRejectsInvalidChannelIDs(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a"}},
	})
	require.NoError(t, err)
	channel := newModelAliasTestChannel("渠道 A", "vendor/a", nil)
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))

	tests := []struct {
		name      string
		channelID int
	}{
		{name: "非正数渠道 ID", channelID: 0},
		{name: "预览外渠道 ID", channelID: channel.Id + 1000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
				SelectedChannelIDs: []int{test.channelID},
			})
			require.Error(t, err)

			var unchanged Channel
			require.NoError(t, DB.First(&unchanged, channel.Id).Error)
			assert.NotContains(t, unchanged.GetModels(), "deepseek-v4-pro")
		})
	}
}

func TestApplyModelAliasGroupWithSelectionRejectsCyclicMultipleMatchTarget(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)
	channel := newModelAliasTestChannel("多匹配", "vendor/a,vendor/b", map[string]string{
		"vendor/a": "deepseek-v4-pro",
	})
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))

	result, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channel.Id},
		TargetModels:       map[int]string{channel.Id: "vendor/a"},
	})
	require.NoError(t, err)
	assert.Zero(t, result.Applied)
	assert.Zero(t, result.Skipped)
	require.Len(t, result.Failed, 1)
	assert.Contains(t, result.Failed[0].Error, "循环映射")

	var unchanged Channel
	require.NoError(t, DB.First(&unchanged, channel.Id).Error)
	assert.NotContains(t, unchanged.GetModels(), "deepseek-v4-pro")
}

func TestModelAliasScanCountsPendingChannelsAndKeepsLastSuccessfulResult(t *testing.T) {
	setupModelAliasGroupTest(t)
	configuration, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/a", "vendor/old"}},
	}, true, 30)
	require.NoError(t, err)
	require.Nil(t, configuration.Groups[0].PendingCount)

	channels := []*Channel{
		newModelAliasTestChannel("新增", "vendor/a", nil),
		newModelAliasTestChannel("更新", "vendor/a", map[string]string{"alias": "vendor/old"}),
		newModelAliasTestChannel("多匹配", "vendor/a,vendor/old", nil),
		newModelAliasTestChannel("冲突", "vendor/a", map[string]string{"alias": "outside/model"}),
		newModelAliasTestChannel("已一致", "vendor/a,alias", map[string]string{"alias": "vendor/a"}),
		newModelAliasTestChannelWithRawMapping("无关坏映射", "unrelated/model", "{"),
	}
	channels[1].Status = common.ChannelStatusManuallyDisabled
	channels[2].Status = common.ChannelStatusManuallyDisabled
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
	}

	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.ScannedGroups)
	assert.Equal(t, 6, summary.ScannedChannels)
	assert.Equal(t, 4, summary.PendingCount)
	assert.True(t, summary.IsCurrent())

	configuration, err = GetModelAliasConfiguration()
	require.NoError(t, err)
	require.NotNil(t, configuration.Groups[0].PendingCount)
	assert.Equal(t, 4, *configuration.Groups[0].PendingCount)

	preview, err := PreviewModelAliasGroup("alias")
	require.NoError(t, err)
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusUnmatched])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusConflict])

	require.NoError(t, DB.Create(newModelAliasTestChannel("新增二", "vendor/a", nil)).Error)
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ScanModelAliasPendingCounts(cancelledContext, nil)
	assert.ErrorIs(t, err, context.Canceled)
	configuration, err = GetModelAliasConfiguration()
	require.NoError(t, err)
	require.NotNil(t, configuration.Groups[0].PendingCount)
	assert.Equal(t, 4, *configuration.Groups[0].PendingCount)

	common.OptionMapRWMutex.RLock()
	rawCounts := common.OptionMap[ModelAliasPendingCountsOptionKey]
	common.OptionMapRWMutex.RUnlock()
	assert.NotContains(t, rawCounts, "channel")
}

func TestSaveModelAliasConfigurationInvalidatesOnlyChangedGroups(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias-a", Models: []string{"vendor/a"}},
		{Alias: "alias-b", Models: []string{"vendor/b"}},
	}, true, 30)
	require.NoError(t, err)
	require.NoError(t, DB.Create(newModelAliasTestChannel("渠道 A", "vendor/a", nil)).Error)
	require.NoError(t, DB.Create(newModelAliasTestChannel("渠道 B", "vendor/b", nil)).Error)
	_, err = ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)

	configuration, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias-a", Models: []string{"vendor/a-new"}},
		{Alias: "alias-b", Models: []string{"vendor/b"}},
	}, false, 45)
	require.NoError(t, err)
	assert.False(t, configuration.ScanEnabled)
	assert.Equal(t, 45, configuration.ScanIntervalMinutes)
	require.Nil(t, configuration.Groups[0].PendingCount)
	require.NotNil(t, configuration.Groups[1].PendingCount)
	assert.Equal(t, 1, *configuration.Groups[1].PendingCount)

	_, err = SaveModelAliasConfiguration(configuration.Groups, true, MinimumModelAliasScanIntervalMinutes-1)
	assert.ErrorContains(t, err, "不能小于")
	if strconv.IntSize == 64 {
		tooLarge := maxModelAliasScanIntervalMinutes + 1
		_, err = SaveModelAliasConfiguration(configuration.Groups, true, int(tooLarge))
		assert.ErrorContains(t, err, "间隔过大")
	}
}

func TestModelAliasScanRejectsStaleRevision(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/a"}},
	}, true, 30)
	require.NoError(t, err)
	revision := getModelAliasScanRevision()
	require.NoError(t, InvalidateModelAliasPendingCount("alias"))

	stored, err := saveModelAliasPendingCounts(revision, map[string]int{"alias": 99})
	require.NoError(t, err)
	assert.False(t, stored)
	configuration, err := GetModelAliasConfiguration()
	require.NoError(t, err)
	assert.Nil(t, configuration.Groups[0].PendingCount)
}

func TestModelAliasScanUsesDatabaseSnapshotWhenOptionMapIsStale(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/old"}},
	}, true, 30)
	require.NoError(t, err)

	remoteGroups := []ModelAliasGroup{{Alias: "alias", Models: []string{"vendor/new"}}}
	groupsData, err := common.Marshal(remoteGroups)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where(commonKeyCol+" = ?", ModelAliasGroupsOptionKey).
		Update("value", string(groupsData)).Error)
	require.NoError(t, DB.Model(&Option{}).Where(commonKeyCol+" = ?", modelAliasScanRevisionOptionKey).
		Update("value", "remote-revision").Error)
	require.NoError(t, DB.Create(newModelAliasTestChannel("新配置渠道", "vendor/new", nil)).Error)

	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.PendingCount)
	assert.True(t, summary.IsCurrent())

	counts := getModelAliasPendingCounts()
	assert.Equal(t, 1, counts["alias"])
}

func TestModelAliasScanTreatsEmptyRevisionAsCurrent(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/model"}},
	}, true, 30)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where(commonKeyCol+" = ?", modelAliasScanRevisionOptionKey).
		Update("value", "").Error)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[modelAliasScanRevisionOptionKey] = ""
	common.OptionMapRWMutex.Unlock()

	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.True(t, summary.IsCurrent())
}

func TestModelAliasScanRevisionChangesOnEverySave(t *testing.T) {
	setupModelAliasGroupTest(t)
	groups := []ModelAliasGroup{{Alias: "alias", Models: []string{"vendor/a"}}}
	_, err := SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)

	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.True(t, summary.IsCurrent())

	_, err = SaveModelAliasConfiguration(groups, false, 45)
	require.NoError(t, err)
	assert.False(t, summary.IsCurrent())
}

func TestSaveModelAliasConfigurationCopiesAllPricingModesAndLocksModels(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{
		"ModelPrice":                   `{"per-call-main":0,"per-call-target":9,"expr-main":1.25,"expr-target":9}`,
		"ModelRatio":                   `{"ratio-main":0,"ratio-target":9,"per-call-target":9,"expr-main":2,"expr-target":9}`,
		"CompletionRatio":              `{"ratio-main":3,"ratio-target":9,"per-call-target":9,"expr-main":4,"expr-target":9}`,
		"CacheRatio":                   `{"ratio-main":0,"ratio-target":9,"per-call-target":9,"expr-main":0.1,"expr-target":9}`,
		"CreateCacheRatio":             `{"ratio-main":5,"ratio-target":9,"per-call-target":9,"expr-main":0,"expr-target":9}`,
		"ImageRatio":                   `{"ratio-main":6,"ratio-target":9,"per-call-target":9,"expr-main":0.2,"expr-target":9}`,
		"AudioRatio":                   `{"ratio-main":7,"ratio-target":9,"per-call-target":9,"expr-main":0.3,"expr-target":9}`,
		"AudioCompletionRatio":         `{"ratio-main":8,"ratio-target":9,"per-call-target":9,"expr-main":0.4,"expr-target":9}`,
		"billing_setting.billing_mode": `{"per-call-target":"tiered_expr","ratio-target":"tiered_expr","expr-main":"tiered_expr","expr-target":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"per-call-target":"p * 9","ratio-target":"p * 9","expr-main":"p * 2 + c * 3","expr-target":"p * 9"}`,
	})

	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "per-call-main", Models: []string{"per-call-target"}},
		{Alias: "ratio-main", Models: []string{"ratio-target"}},
		{Alias: "expr-main", Models: []string{"expr-target"}},
	}, true, 30)
	require.NoError(t, err)

	assert.Equal(t, float64(0), getModelAliasPricingValue(t, "ModelPrice", "per-call-target"))
	for _, key := range append(modelPricingRatioOptionKeys, "billing_setting.billing_mode", "billing_setting.billing_expr") {
		assertModelAliasPricingMissing(t, key, "per-call-target")
	}

	ratioExpected := map[string]float64{
		"ModelRatio": 0, "CompletionRatio": 3, "CacheRatio": 0, "CreateCacheRatio": 5,
		"ImageRatio": 6, "AudioRatio": 7, "AudioCompletionRatio": 8,
	}
	for key, expected := range ratioExpected {
		assert.Equal(t, expected, getModelAliasPricingValue(t, key, "ratio-target"), key)
	}
	assertModelAliasPricingMissing(t, "ModelPrice", "ratio-target")
	assertModelAliasPricingMissing(t, "billing_setting.billing_mode", "ratio-target")
	assertModelAliasPricingMissing(t, "billing_setting.billing_expr", "ratio-target")

	exprExpected := map[string]any{
		"ModelPrice": 1.25, "ModelRatio": float64(2), "CompletionRatio": float64(4),
		"CacheRatio": 0.1, "CreateCacheRatio": float64(0), "ImageRatio": 0.2,
		"AudioRatio": 0.3, "AudioCompletionRatio": 0.4,
		"billing_setting.billing_mode": "tiered_expr",
		"billing_setting.billing_expr": "p * 2 + c * 3",
	}
	for key, expected := range exprExpected {
		assert.Equal(t, expected, getModelAliasPricingValue(t, key, "expr-target"), key)
	}
	for _, key := range modelPricingSyncOptionKeys {
		assert.Equal(t, readDatabaseOptionValue(t, key), readMemoryOptionValue(key), key)
	}
	price, exists := ratio_setting.GetModelPrice("per-call-target", false)
	assert.True(t, exists)
	assert.Equal(t, float64(0), price)
	ratio, exists, _ := ratio_setting.GetModelRatio("ratio-target")
	assert.True(t, exists)
	assert.Equal(t, float64(0), ratio)
	assert.Equal(t, float64(3), ratio_setting.GetCompletionRatio("ratio-target"))
	cacheRatio, exists := ratio_setting.GetCacheRatio("ratio-target")
	assert.True(t, exists)
	assert.Equal(t, float64(0), cacheRatio)
	createCacheRatio, exists := ratio_setting.GetCreateCacheRatio("ratio-target")
	assert.True(t, exists)
	assert.Equal(t, float64(5), createCacheRatio)
	imageRatio, exists := ratio_setting.GetImageRatio("ratio-target")
	assert.True(t, exists)
	assert.Equal(t, float64(6), imageRatio)
	assert.Equal(t, float64(7), ratio_setting.GetAudioRatio("ratio-target"))
	assert.Equal(t, float64(8), ratio_setting.GetAudioCompletionRatio("ratio-target"))
	assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("expr-target"))
	expr, exists := billing_setting.GetBillingExpr("expr-target")
	assert.True(t, exists)
	assert.Equal(t, "p * 2 + c * 3", expr)

	locks, err := GetModelPricingLocks()
	require.NoError(t, err)
	for _, modelName := range []string{"per-call-main", "per-call-target", "ratio-main", "ratio-target", "expr-main", "expr-target"} {
		assert.True(t, locks[modelName], modelName)
	}

	applyResult, err := ApplyModelPricingSync(map[string]map[string]any{
		"per-call-main":   {"model_price": 99.0},
		"per-call-target": {"model_price": 99.0},
		"unlocked-model":  {"model_price": 5.0},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"unlocked-model"}, applyResult.AppliedModels)
	assert.Equal(t, []string{"per-call-main", "per-call-target"}, applyResult.IgnoredLockedModels)
}

func TestSaveModelAliasConfigurationRollsBackWhenMainModelHasNoPrice(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{"ModelPrice": `{"existing-main":1}`})
	groups := []ModelAliasGroup{{Alias: "existing-main", Models: []string{"existing-target"}}}
	_, err := SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)
	before := readModelAliasOptionValues(t)

	_, err = SaveModelAliasConfiguration(append(groups, ModelAliasGroup{
		Alias: "missing-main", Models: []string{"missing-target"},
	}), false, 45)
	require.ErrorContains(t, err, "缺少明确的按次价格、按量倍率或完整表达式配置")
	assert.Equal(t, before, readModelAliasOptionValues(t))

	configuration, err := GetModelAliasConfiguration()
	require.NoError(t, err)
	assert.Equal(t, groups, configuration.Groups)
	locks, err := GetModelPricingLocks()
	require.NoError(t, err)
	assert.False(t, locks["missing-main"])
	assert.False(t, locks["missing-target"])
}

func TestSaveModelAliasConfigurationRejectsIncompleteExpressionsWithFallbackPrice(t *testing.T) {
	for _, testCase := range []struct {
		name string
		expr string
	}{
		{name: "缺少表达式", expr: `{}`},
		{name: "空白表达式", expr: `{"expr-main":"  "}`},
		{name: "无效表达式", expr: `{"expr-main":"p *"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			setupModelAliasGroupTest(t)
			setModelAliasPricingOptions(t, map[string]string{
				"ModelPrice":                   `{"expr-main":1}`,
				"ModelRatio":                   `{"expr-main":2}`,
				"billing_setting.billing_mode": `{"expr-main":"tiered_expr"}`,
				"billing_setting.billing_expr": testCase.expr,
			})
			before := readModelAliasOptionValues(t)
			beforeMemory := readModelAliasMemoryOptionValues()

			_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
				{Alias: "expr-main", Models: []string{"expr-target"}},
			}, true, 30)
			require.Error(t, err)
			assert.Equal(t, before, readModelAliasOptionValues(t))
			assert.Equal(t, beforeMemory, readModelAliasMemoryOptionValues())
			_, hasPrice := ratio_setting.GetModelPrice("expr-target", false)
			assert.False(t, hasPrice)
			_, hasRatio, _ := ratio_setting.GetModelRatio("expr-target")
			assert.False(t, hasRatio)
			assert.Equal(t, billing_setting.BillingModeRatio, billing_setting.GetBillingMode("expr-target"))
			_, hasExpr := billing_setting.GetBillingExpr("expr-target")
			assert.False(t, hasExpr)
			locks, lockErr := GetModelPricingLocks()
			require.NoError(t, lockErr)
			assert.False(t, locks["expr-target"])
		})
	}
}

func TestModelAliasScanRefreshesPricesRelocksAndSkipsInvalidGroups(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{
		"ModelPrice": `{"sync-main":1,"skip-main":3}`,
	})
	groups := []ModelAliasGroup{
		{Alias: "sync-main", Models: []string{"sync-target"}},
		{Alias: "skip-main", Models: []string{"skip-target"}},
	}
	_, err := SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)

	require.NoError(t, UpdateOption("ModelPrice", `{"sync-main":2,"sync-target":1,"skip-target":3}`))
	require.NoError(t, UpdateOption(ModelPricingLocksOptionKey, `{}`))

	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.SyncedGroups)
	assert.Equal(t, 1, summary.SyncedModels)
	require.Len(t, summary.SkippedGroups, 1)
	assert.Equal(t, "skip-main", summary.SkippedGroups[0].Alias)
	assert.Contains(t, summary.SkippedGroups[0].Reason, "缺少明确")
	assert.Equal(t, float64(2), getModelAliasPricingValue(t, "ModelPrice", "sync-target"))
	assert.Equal(t, float64(3), getModelAliasPricingValue(t, "ModelPrice", "skip-target"))

	locks, err := GetModelPricingLocks()
	require.NoError(t, err)
	for _, modelName := range []string{"sync-main", "sync-target", "skip-main", "skip-target"} {
		assert.True(t, locks[modelName], modelName)
	}
}

func TestRemovingModelsOrGroupsKeepsCopiedPricesAndLocks(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{"ModelPrice": `{"preserve-main":4}`})
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "preserve-main", Models: []string{"preserve-a", "preserve-b"}},
	}, true, 30)
	require.NoError(t, err)
	require.NoError(t, UpdateOption("ModelPrice", `{"preserve-main":9,"preserve-a":4,"preserve-b":4}`))

	_, err = SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "preserve-main", Models: []string{"preserve-a"}},
	}, true, 30)
	require.NoError(t, err)
	assert.Equal(t, float64(4), getModelAliasPricingValue(t, "ModelPrice", "preserve-b"))
	locks, err := GetModelPricingLocks()
	require.NoError(t, err)
	assert.True(t, locks["preserve-b"])
	require.NoError(t, UpdateOption("ModelPrice", `{"preserve-main":12,"preserve-a":9,"preserve-b":4}`))

	_, err = SaveModelAliasConfiguration(nil, true, 30)
	require.NoError(t, err)
	assert.Equal(t, float64(9), getModelAliasPricingValue(t, "ModelPrice", "preserve-a"))
	assert.Equal(t, float64(4), getModelAliasPricingValue(t, "ModelPrice", "preserve-b"))
	locks, err = GetModelPricingLocks()
	require.NoError(t, err)
	for _, modelName := range []string{"preserve-main", "preserve-a", "preserve-b"} {
		assert.True(t, locks[modelName], modelName)
	}
}

func TestModelAliasPricingLocksCannotBeRemovedUntilModelLeavesGroup(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{"ModelPrice": `{"lock-main":1}`})
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "lock-main", Models: []string{"lock-member", "removed-member"}},
	}, true, 30)
	require.NoError(t, err)
	_, err = SetModelPricingLock("manual-lock", true)
	require.NoError(t, err)

	result, err := SetModelPricingLocks([]string{"lock-main", "lock-member", "removed-member"}, false)
	require.NoError(t, err)
	assert.Empty(t, result.ChangedModels)
	for _, modelName := range []string{"lock-main", "lock-member", "removed-member", "manual-lock"} {
		assert.Contains(t, result.LockedModels, modelName)
	}

	_, err = SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "lock-main", Models: []string{"lock-member"}},
	}, true, 30)
	require.NoError(t, err)
	result, err = SetModelPricingLocks([]string{"removed-member"}, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"removed-member"}, result.ChangedModels)
	assert.Contains(t, result.LockedModels, "manual-lock")

	_, err = SaveModelAliasConfiguration(nil, true, 30)
	require.NoError(t, err)
	result, err = SetModelPricingLocks([]string{"lock-main", "lock-member"}, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"lock-main", "lock-member"}, result.ChangedModels)
	assert.Equal(t, []string{"manual-lock"}, result.LockedModels)
}

func TestScanSettingOnlySaveDoesNotRequireOrCopyMainModelPrice(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{"ModelPrice": `{"settings-main":1}`})
	groups := []ModelAliasGroup{{Alias: "settings-main", Models: []string{"settings-target"}}}
	_, err := SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)
	require.NoError(t, UpdateOption("ModelPrice", `{"settings-target":1}`))

	configuration, err := SaveModelAliasConfiguration(groups, false, 45)
	require.NoError(t, err)
	assert.False(t, configuration.ScanEnabled)
	assert.Equal(t, 45, configuration.ScanIntervalMinutes)
	assert.Equal(t, float64(1), getModelAliasPricingValue(t, "ModelPrice", "settings-target"))
}

func TestSaveModelAliasConfigurationReportsOnlyNewOrChangedCurrentGroups(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{"ModelPrice": `{"changes-main":1}`})
	groups := []ModelAliasGroup{{Alias: "changes-main", Models: []string{"vendor/a", "vendor/b"}}}
	_, changed, err := SaveModelAliasConfigurationWithChanges(groups, true, 30)
	require.NoError(t, err)
	assert.True(t, changed)

	_, changed, err = SaveModelAliasConfigurationWithChanges([]ModelAliasGroup{
		{Alias: "changes-main", Models: []string{"vendor/b", "vendor/a"}},
	}, false, 45)
	require.NoError(t, err)
	assert.False(t, changed)

	_, changed, err = SaveModelAliasConfigurationWithChanges(nil, false, 45)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestSaveModelAliasConfigurationCopiesOnlyChangedCurrentGroups(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{
		"ModelPrice": `{"changed-main":1,"unchanged-main":4}`,
	})
	groups := []ModelAliasGroup{
		{Alias: "changed-main", Models: []string{"changed-member"}},
		{Alias: "unchanged-main", Models: []string{"unchanged-member"}},
	}
	_, err := SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)
	require.NoError(t, UpdateOption("ModelPrice", `{"changed-main":2,"changed-member":1,"unchanged-main":9,"unchanged-member":4}`))

	_, changed, err := SaveModelAliasConfigurationWithChanges([]ModelAliasGroup{
		{Alias: "changed-main", Models: []string{"changed-member", "changed-second"}},
		groups[1],
	}, true, 30)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, float64(2), getModelAliasPricingValue(t, "ModelPrice", "changed-member"))
	assert.Equal(t, float64(2), getModelAliasPricingValue(t, "ModelPrice", "changed-second"))
	assert.Equal(t, float64(4), getModelAliasPricingValue(t, "ModelPrice", "unchanged-member"))
}

func TestModelAliasScanDoesNotCommitStalePrices(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{"ModelPrice": `{"stale-main":1}`})
	groups := []ModelAliasGroup{{Alias: "stale-main", Models: []string{"stale-target"}}}
	_, err := SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)
	revision := getModelAliasScanRevision()
	require.NoError(t, UpdateOption("ModelPrice", `{"stale-main":2,"stale-target":1}`))
	require.NoError(t, InvalidateModelAliasPendingCount("stale-main"))

	stored, _, err := saveModelAliasScanResult(revision, groups, map[string]int{"stale-main": 99})
	require.NoError(t, err)
	assert.False(t, stored)
	assert.Equal(t, float64(1), getModelAliasPricingValue(t, "ModelPrice", "stale-target"))
	assert.NotEqual(t, 99, getModelAliasPendingCounts()["stale-main"])
}

func TestModelAliasScanDoesNotCommitAfterScanningIsDisabled(t *testing.T) {
	setupModelAliasGroupTest(t)
	setModelAliasPricingOptions(t, map[string]string{"ModelPrice": `{"disabled-main":1}`})
	groups := []ModelAliasGroup{{Alias: "disabled-main", Models: []string{"disabled-target"}}}
	_, err := SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)
	revision := getModelAliasScanRevision()
	require.NoError(t, UpdateOption("ModelPrice", `{"disabled-main":2,"disabled-target":1}`))
	require.NoError(t, UpdateOption(ModelPricingLocksOptionKey, `{}`))
	require.NoError(t, DB.Model(&Option{}).
		Where(commonKeyCol+" = ?", ModelAliasScanEnabledOptionKey).
		Update("value", "false").Error)

	stored, _, err := saveModelAliasScanResult(revision, groups, map[string]int{"disabled-main": 99})
	require.NoError(t, err)
	assert.False(t, stored)
	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.Zero(t, summary.ScannedGroups)
	assert.Equal(t, float64(1), getModelAliasPricingValue(t, "ModelPrice", "disabled-target"))
	assert.Empty(t, getModelAliasPendingCounts())
	locks, err := GetModelPricingLocks()
	require.NoError(t, err)
	assert.Empty(t, locks)
}

func setModelAliasPricingOptions(t *testing.T, values map[string]string) {
	t.Helper()
	for key, value := range values {
		require.NoError(t, UpdateOption(key, value), key)
	}
}

func getModelAliasPricingValue(t *testing.T, key string, modelName string) any {
	t.Helper()
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[key]
	common.OptionMapRWMutex.RUnlock()
	values := make(map[string]any)
	require.NoError(t, common.UnmarshalJsonStr(raw, &values), key)
	value, exists := values[modelName]
	require.True(t, exists, "%s 缺少模型 %s", key, modelName)
	return value
}

func readMemoryOptionValue(key string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap[key]
}

func readDatabaseOptionValue(t *testing.T, key string) string {
	t.Helper()
	var option Option
	require.NoError(t, DB.Where(commonKeyCol+" = ?", key).First(&option).Error)
	return option.Value
}

func readModelAliasMemoryOptionValues() map[string]string {
	keys := append([]string{
		ModelAliasGroupsOptionKey,
		ModelAliasScanEnabledOptionKey,
		ModelAliasScanIntervalOptionKey,
		ModelAliasPendingCountsOptionKey,
		modelAliasScanRevisionOptionKey,
		ModelPricingLocksOptionKey,
	}, modelPricingSyncOptionKeys...)
	values := make(map[string]string, len(keys))
	common.OptionMapRWMutex.RLock()
	for _, key := range keys {
		values[key] = common.OptionMap[key]
	}
	common.OptionMapRWMutex.RUnlock()
	return values
}

func assertModelAliasPricingMissing(t *testing.T, key string, modelName string) {
	t.Helper()
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[key]
	common.OptionMapRWMutex.RUnlock()
	values := make(map[string]any)
	require.NoError(t, common.UnmarshalJsonStr(raw, &values), key)
	assert.NotContains(t, values, modelName, key)
}

func readModelAliasOptionValues(t *testing.T) map[string]string {
	t.Helper()
	keys := append([]string{
		ModelAliasGroupsOptionKey,
		ModelAliasScanEnabledOptionKey,
		ModelAliasScanIntervalOptionKey,
		ModelAliasPendingCountsOptionKey,
		modelAliasScanRevisionOptionKey,
		ModelPricingLocksOptionKey,
	}, modelPricingSyncOptionKeys...)
	var options []Option
	require.NoError(t, DB.Where(commonKeyCol+" IN ?", keys).Find(&options).Error)
	values := make(map[string]string, len(options))
	for _, option := range options {
		values[option.Key] = option.Value
	}
	return values
}

func newModelAliasTestChannel(name string, models string, mapping map[string]string) *Channel {
	channel := &Channel{Name: name, Models: models, Group: "default", Status: common.ChannelStatusEnabled, Key: "test-key"}
	if mapping == nil {
		return channel
	}
	data, _ := common.Marshal(mapping)
	text := string(data)
	channel.ModelMapping = &text
	return channel
}

func newModelAliasTestChannelWithRawMapping(name string, models string, mapping string) *Channel {
	channel := newModelAliasTestChannel(name, models, nil)
	channel.ModelMapping = &mapping
	return channel
}

func assertModelAliasChannel(t *testing.T, channelID int, expectedTarget string, expectAliasAbility bool) {
	t.Helper()
	var channel Channel
	require.NoError(t, DB.First(&channel, channelID).Error)
	assert.Contains(t, channel.GetModels(), "deepseek-v4-pro")
	require.NotNil(t, channel.ModelMapping)
	var mapping map[string]string
	require.NoError(t, common.UnmarshalJsonStr(*channel.ModelMapping, &mapping))
	assert.Equal(t, expectedTarget, mapping["deepseek-v4-pro"])

	var abilityCount int64
	require.NoError(t, DB.Model(&Ability{}).
		Where("channel_id = ? AND model = ?", channelID, "deepseek-v4-pro").
		Count(&abilityCount).Error)
	if expectAliasAbility {
		assert.EqualValues(t, 1, abilityCount)
	} else {
		assert.Zero(t, abilityCount)
	}
}
