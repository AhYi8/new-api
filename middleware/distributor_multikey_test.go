package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupContextForSelectedChannelWithKeyIndexBypassesKeyStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{
		Id:     1,
		Key:    "enabled-key\nauto-disabled-key",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:         true,
			MultiKeyStatusList: map[int]int{1: common.ChannelStatusAutoDisabled},
		},
	}

	normalContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, SetupContextForSelectedChannel(normalContext, channel, "gpt-4o-mini"))
	assert.Equal(t, "enabled-key", common.GetContextKeyString(normalContext, constant.ContextKeyChannelKey))
	assert.Equal(t, 0, common.GetContextKeyInt(normalContext, constant.ContextKeyChannelMultiKeyIndex))

	specificContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, SetupContextForSelectedChannelWithKeyIndex(specificContext, channel, "gpt-4o-mini", 1))
	assert.Equal(t, "auto-disabled-key", common.GetContextKeyString(specificContext, constant.ContextKeyChannelKey))
	assert.Equal(t, 1, common.GetContextKeyInt(specificContext, constant.ContextKeyChannelMultiKeyIndex))
}

func TestSetupContextForSelectedChannelWithKeyIndexRejectsInvalidIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{
		Id:  1,
		Key: "key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.NotNil(t, SetupContextForSelectedChannelWithKeyIndex(ctx, channel, "gpt-4o-mini", 1))
}

func TestSetupContextForSelectedChannelClearsPreviousOptionalSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelOrganization, "stale-organization")
	common.SetContextKey(ctx, constant.ContextKeyChannelMultiKeyIndex, 9)
	ctx.Set("api_version", "stale-version")
	ctx.Set("region", "stale-region")
	ctx.Set("plugin", "stale-plugin")
	ctx.Set("bot_id", "stale-bot")

	channel := &model.Channel{
		Id:     2,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "target-key",
		Status: common.ChannelStatusEnabled,
	}
	require.Nil(t, SetupContextForSelectedChannel(ctx, channel, "gpt-4o-mini"))

	assert.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyChannelOrganization))
	assert.Equal(t, 0, common.GetContextKeyInt(ctx, constant.ContextKeyChannelMultiKeyIndex))
	assert.Empty(t, ctx.GetString("api_version"))
	assert.Empty(t, ctx.GetString("region"))
	assert.Empty(t, ctx.GetString("plugin"))
	assert.Empty(t, ctx.GetString("bot_id"))
}
