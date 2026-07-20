package controller

import (
	"net/http"
	"net/http/httptest"
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
