package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type channelAggregationGroupsTestResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Groups []struct {
			Type           int    `json:"type"`
			Eligible       bool   `json:"eligible"`
			DisabledReason string `json:"disabled_reason"`
		} `json:"groups"`
	} `json:"data"`
}

func TestPrepareChannelAggregationReturnsOrderedBasicConfigs(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	firstTag := "primary"
	firstRemark := "first remark"
	firstOrganization := "org-first"
	firstTestModel := "gpt-4o-mini"
	firstWeight := uint(20)
	firstPriority := int64(8)
	firstAutoBan := 0
	first := &model.Channel{
		Type:               1,
		Name:               "first",
		Key:                "key-a",
		OpenAIOrganization: &firstOrganization,
		TestModel:          &firstTestModel,
		Weight:             &firstWeight,
		Models:             "gpt-4o,gpt-4o-mini",
		Group:              "default,premium",
		Priority:           &firstPriority,
		AutoBan:            &firstAutoBan,
		Tag:                &firstTag,
		Remark:             &firstRemark,
		Other:              "v1",
		Status:             common.ChannelStatusEnabled,
	}
	first.SetSetting(dto.ChannelSettings{Proxy: "http://proxy-first.example.com"})
	second := &model.Channel{
		Type:   1,
		Name:   "second",
		Key:    "key-b",
		Models: "gpt-4o-mini,o1",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}
	second.SetSetting(dto.ChannelSettings{Proxy: "http://proxy-second.example.com"})
	require.NoError(t, db.Create(first).Error)
	require.NoError(t, db.Create(second).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/channel/aggregation/prepare",
		strings.NewReader(`{"source_ids":[2,1]}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	PrepareChannelAggregation(ctx)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Key           string                           `json:"key"`
			SourceConfigs []channelAggregationSourceConfig `json:"source_configs"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.Equal(t, "key-a\nkey-b", response.Data.Key)
	require.Len(t, response.Data.SourceConfigs, 2)

	firstConfig := response.Data.SourceConfigs[0]
	assert.Equal(t, first.Id, firstConfig.ID)
	assert.Equal(t, first.Name, firstConfig.Name)
	assert.Equal(t, first.OpenAIOrganization, firstConfig.OpenAIOrganization)
	assert.Equal(t, first.TestModel, firstConfig.TestModel)
	assert.Equal(t, first.Weight, firstConfig.Weight)
	assert.Equal(t, first.Group, firstConfig.Group)
	assert.Equal(t, first.Priority, firstConfig.Priority)
	assert.Equal(t, first.AutoBan, firstConfig.AutoBan)
	assert.Equal(t, first.Tag, firstConfig.Tag)
	assert.Equal(t, first.Remark, firstConfig.Remark)
	assert.Equal(t, first.Other, firstConfig.Other)
	assert.Equal(t, "http://proxy-first.example.com", firstConfig.Proxy)
	assert.Equal(t, first.Models, firstConfig.Models)
	assert.Equal(t, second.Id, response.Data.SourceConfigs[1].ID)

	var rawResponse map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &rawResponse))
	data := rawResponse["data"].(map[string]interface{})
	configs := data["source_configs"].([]interface{})
	for _, config := range configs {
		configMap := config.(map[string]interface{})
		for _, excludedField := range []string{
			"key",
			"setting",
			"settings",
			"model_mapping",
			"param_override",
			"header_override",
			"status_code_mapping",
			"balance",
			"used_quota",
		} {
			_, exists := configMap[excludedField]
			assert.False(t, exists, "source_configs 不应包含字段 %s", excludedField)
		}
	}
}

func TestGetChannelAggregationGroupsReturnsUnsupportedReasons(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	channels := []*model.Channel{
		{Type: constant.ChannelTypeCodex, Name: "codex-a", Key: "key-a", Status: common.ChannelStatusEnabled},
		{Type: constant.ChannelTypeCodex, Name: "codex-b", Key: "key-b", Status: common.ChannelStatusEnabled},
		{Type: constant.ChannelTypeVertexAi, Name: "vertex-a", Key: "key-a", Status: common.ChannelStatusEnabled},
		{Type: constant.ChannelTypeVertexAi, Name: "vertex-b", Key: "key-b", Status: common.ChannelStatusEnabled},
	}
	for _, channel := range channels[2:] {
		channel.SetOtherSettings(dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeAPIKey})
	}
	require.NoError(t, db.Create(&channels).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/aggregation/groups", nil)
	GetChannelAggregationGroups(ctx)

	var response channelAggregationGroupsTestResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Groups, 2)
	groupsByType := make(map[int]struct {
		Eligible       bool
		DisabledReason string
	}, len(response.Data.Groups))
	for _, group := range response.Data.Groups {
		groupsByType[group.Type] = struct {
			Eligible       bool
			DisabledReason string
		}{Eligible: group.Eligible, DisabledReason: group.DisabledReason}
	}
	assert.Equal(t, model.ChannelAggregationReasonCodex, groupsByType[constant.ChannelTypeCodex].DisabledReason)
	assert.False(t, groupsByType[constant.ChannelTypeCodex].Eligible)
	assert.Equal(t, model.ChannelAggregationReasonVertexAPIKey, groupsByType[constant.ChannelTypeVertexAi].DisabledReason)
	assert.False(t, groupsByType[constant.ChannelTypeVertexAi].Eligible)
}

func TestGetChannelAggregationGroupsOmitsSingleChannelGroups(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	channels := []*model.Channel{
		{Type: 1, Name: "single", Key: "key-a", Status: common.ChannelStatusEnabled},
		{Type: 2, Name: "merge-a", Key: "key-b", Status: common.ChannelStatusEnabled},
		{Type: 2, Name: "merge-b", Key: "key-c", Status: common.ChannelStatusEnabled},
	}
	require.NoError(t, db.Create(&channels).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/aggregation/groups", nil)
	GetChannelAggregationGroups(ctx)

	var response channelAggregationGroupsTestResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Groups, 1)
	assert.Equal(t, 2, response.Data.Groups[0].Type)
	assert.True(t, response.Data.Groups[0].Eligible)
}

func TestGetChannelAggregationGroupsDoesNotRewriteInvalidVertexSettings(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	channels := []*model.Channel{
		{Type: constant.ChannelTypeVertexAi, Name: "vertex-a", Key: "key-a", OtherSettings: "{", Status: common.ChannelStatusEnabled},
		{Type: constant.ChannelTypeVertexAi, Name: "vertex-b", Key: "key-b", OtherSettings: "{", Status: common.ChannelStatusEnabled},
	}
	require.NoError(t, db.Create(&channels).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/aggregation/groups", nil)
	GetChannelAggregationGroups(ctx)

	var response channelAggregationGroupsTestResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Groups, 1)
	assert.Equal(t, model.ChannelAggregationReasonInvalid, response.Data.Groups[0].DisabledReason)

	var stored []model.Channel
	require.NoError(t, db.Order("id ASC").Find(&stored).Error)
	require.Len(t, stored, 2)
	assert.Equal(t, "key-a", stored[0].Key)
	assert.Equal(t, "key-b", stored[1].Key)
	assert.Equal(t, "{", stored[0].OtherSettings)
	assert.Equal(t, "{", stored[1].OtherSettings)
}
