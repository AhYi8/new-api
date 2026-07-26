package controller

import (
	"bytes"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePricingSyncResolutions(t *testing.T) {
	testCases := []struct {
		name        string
		resolutions map[string]map[string]any
	}{
		{name: "空请求", resolutions: map[string]map[string]any{}},
		{name: "空模型名", resolutions: map[string]map[string]any{" ": {"model_price": 1.0}}},
		{name: "未知字段", resolutions: map[string]map[string]any{"model": {"unknown": 1.0}}},
		{name: "负数", resolutions: map[string]map[string]any{"model": {"model_price": -1.0}}},
		{name: "NaN", resolutions: map[string]map[string]any{"model": {"model_price": math.NaN()}}},
		{name: "无穷大", resolutions: map[string]map[string]any{"model": {"model_ratio": math.Inf(1)}}},
		{name: "计费模式类型错误", resolutions: map[string]map[string]any{"model": {"billing_mode": 1.0}}},
		{name: "计费模式值错误", resolutions: map[string]map[string]any{"model": {"billing_mode": "per-token"}}},
		{name: "空表达式", resolutions: map[string]map[string]any{"model": {"billing_expr": " "}}},
		{name: "计费模式缺少表达式", resolutions: map[string]map[string]any{"model": {"billing_mode": "tiered_expr"}}},
		{name: "计费表达式缺少模式", resolutions: map[string]map[string]any{"model": {"billing_expr": "p * 2"}}},
		{name: "计费表达式语法错误", resolutions: map[string]map[string]any{"model": {"billing_mode": "tiered_expr", "billing_expr": "p +"}}},
		{name: "计费表达式产生负数", resolutions: map[string]map[string]any{"model": {"billing_mode": "tiered_expr", "billing_expr": "-p"}}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := normalizePricingSyncResolutions(testCase.resolutions)
			require.Error(t, err)
		})
	}

	normalized, err := normalizePricingSyncResolutions(map[string]map[string]any{
		" model ": {"model_price": 0.0, "billing_mode": "tiered_expr", "billing_expr": "p * 2"},
	})
	require.NoError(t, err)
	assert.Contains(t, normalized, "model")
	assert.Equal(t, 0.0, normalized["model"]["model_price"])
}

func TestNormalizeModelPricingLocksRequest(t *testing.T) {
	locked := true
	modelNames, normalizedLocked, err := normalizeModelPricingLocksRequest(modelPricingLocksRequest{
		ModelNames: []string{" model-b ", "model-a"},
		Locked:     &locked,
	})
	require.NoError(t, err)
	assert.True(t, normalizedLocked)
	assert.Equal(t, []string{"model-a", "model-b"}, modelNames)
}

func TestUpdateModelPricingLocksRejectsInvalidRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	locked := true
	testCases := []struct {
		name    string
		request modelPricingLocksRequest
	}{
		{name: "缺少锁定状态", request: modelPricingLocksRequest{ModelNames: []string{"model"}}},
		{name: "空模型列表", request: modelPricingLocksRequest{Locked: &locked}},
		{name: "空模型名", request: modelPricingLocksRequest{ModelNames: []string{" "}, Locked: &locked}},
		{name: "模型名过长", request: modelPricingLocksRequest{ModelNames: []string{strings.Repeat("a", maxPricingSyncModelNameBytes+1)}, Locked: &locked}},
		{name: "规范化后重复", request: modelPricingLocksRequest{ModelNames: []string{"model", " model "}, Locked: &locked}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			body, err := common.Marshal(testCase.request)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPut, "/api/ratio_sync/locks", bytes.NewReader(body))

			UpdateModelPricingLocks(context)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestFilterLockedPricingDifferences(t *testing.T) {
	differences := map[string]map[string]dto.DifferenceItem{
		"locked-b": {"model_price": {}},
		"unlocked": {"model_ratio": {}},
		"locked-a": {"billing_expr": {}},
	}

	ignored := filterLockedPricingDifferences(differences, map[string]bool{
		"locked-a": true,
		"locked-b": true,
	})

	assert.Equal(t, []string{"locked-a", "locked-b"}, ignored)
	assert.Equal(t, map[string]map[string]dto.DifferenceItem{
		"unlocked": {"model_ratio": {}},
	}, differences)
}

func TestAliasGroupAutomaticLocksFilterUpstreamPricingDifferences(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))

	setupModelAliasControllerOptions(t, map[string]string{
		"ModelPrice": `{"alias-main":1}`,
	})

	_, err := model.SaveModelAliasConfiguration([]model.ModelAliasGroup{
		{Alias: "alias-main", Models: []string{"alias-member"}},
	}, true, 30)
	require.NoError(t, err)
	locks, err := model.GetModelPricingLocks()
	require.NoError(t, err)
	differences := map[string]map[string]dto.DifferenceItem{
		"alias-main":   {"model_price": {}},
		"alias-member": {"model_price": {}},
		"unlocked":     {"model_price": {}},
	}

	ignored := filterLockedPricingDifferences(differences, locks)

	assert.Equal(t, []string{"alias-main", "alias-member"}, ignored)
	assert.Equal(t, map[string]map[string]dto.DifferenceItem{
		"unlocked": {"model_price": {}},
	}, differences)
}
