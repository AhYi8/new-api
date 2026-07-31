package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestResolveOriginTaskPreservesSelectedMultiKeyIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldMainDatabaseType := common.MainDatabaseType()
	oldLogDatabaseType := common.LogDatabaseType()
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled = false

	db, err := gorm.Open(sqlite.Open("file:resolve_origin_task?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.SetDatabaseTypes(oldMainDatabaseType, oldLogDatabaseType)
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Task{}, &model.Ability{}))

	channel := model.Channel{
		Name:    "locked-duplicate-key-channel",
		Key:     "same-key\nsame-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:         true,
			MultiKeyMode:       constant.MultiKeyModePolling,
			MultiKeySize:       2,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
		},
	}
	require.NoError(t, db.Create(&channel).Error)

	originTask := model.Task{
		TaskID:    "task-origin",
		UserId:    7,
		ChannelId: channel.Id,
	}
	require.NoError(t, db.Create(&originTask).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/continue", nil)
	common.SetContextKey(ctx, constant.ContextKeyChannelIsMultiKey, false)
	common.SetContextKey(ctx, constant.ContextKeyChannelMultiKeyIndex, 0)
	common.SetContextKey(ctx, constant.ContextKeyChannelName, "preselected-channel")
	common.SetContextKey(ctx, constant.ContextKeyChannelParamOverride, map[string]interface{}{"stale": true})
	common.SetContextKey(ctx, constant.ContextKeyChannelHeaderOverride, map[string]interface{}{"X-Stale": "true"})
	common.SetContextKey(ctx, constant.ContextKeyChannelOrganization, "stale-organization")
	ctx.Set("api_version", "stale-version")

	info := &relaycommon.RelayInfo{
		UserId:        originTask.UserId,
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelId: channel.Id + 1},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{OriginTaskID: originTask.TaskID},
	}
	require.Nil(t, ResolveOriginTask(ctx, info))

	assert.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyChannelIsMultiKey))
	assert.Equal(t, 1, common.GetContextKeyInt(ctx, constant.ContextKeyChannelMultiKeyIndex))
	assert.Equal(t, "same-key", common.GetContextKeyString(ctx, constant.ContextKeyChannelKey))
	assert.Equal(t, channel.Name, common.GetContextKeyString(ctx, constant.ContextKeyChannelName))
	assert.Empty(t, common.GetContextKeyStringMap(ctx, constant.ContextKeyChannelParamOverride))
	assert.Empty(t, common.GetContextKeyStringMap(ctx, constant.ContextKeyChannelHeaderOverride))
	assert.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyChannelOrganization))
	assert.Empty(t, ctx.GetString("api_version"))
	assert.True(t, info.ChannelIsMultiKey)
	assert.Equal(t, 1, info.ChannelMultiKeyIndex)

	changed := model.AutoDisableChannel(
		channel.Id,
		common.GetContextKeyString(ctx, constant.ContextKeyChannelKey),
		common.GetPointer(common.GetContextKeyInt(ctx, constant.ContextKeyChannelMultiKeyIndex)),
		common.GetPointer(channel.ChannelInfo.StateGeneration),
		"status_code=401, invalid key",
		http.StatusUnauthorized,
	)
	require.True(t, changed)

	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[1])
}
