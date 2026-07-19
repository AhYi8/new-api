package service

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShouldNotifyRootUserChannelUpdateUsesIndependentSettings(t *testing.T) {
	disabled := false
	enabled := true
	setting := dto.UserSetting{
		ChannelAutoDisableNotifyEnabled:  &disabled,
		ChannelAutoRecoveryNotifyEnabled: &enabled,
	}

	assert.False(t, shouldNotifyRootUserChannelUpdate(setting, common.ChannelStatusAutoDisabled))
	assert.True(t, shouldNotifyRootUserChannelUpdate(setting, common.ChannelStatusEnabled))

	setting.ChannelAutoDisableNotifyEnabled = &enabled
	setting.ChannelAutoRecoveryNotifyEnabled = &disabled
	assert.True(t, shouldNotifyRootUserChannelUpdate(setting, common.ChannelStatusAutoDisabled))
	assert.False(t, shouldNotifyRootUserChannelUpdate(setting, common.ChannelStatusEnabled))
}

func TestShouldNotifyRootUserChannelUpdateDefaultsToEnabled(t *testing.T) {
	assert.True(t, shouldNotifyRootUserChannelUpdate(dto.UserSetting{}, common.ChannelStatusAutoDisabled))
	assert.True(t, shouldNotifyRootUserChannelUpdate(dto.UserSetting{}, common.ChannelStatusEnabled))
}

func TestRootChannelNotificationSkipsWhenRootUserIsMissing(t *testing.T) {
	_ = setupChannelNotificationServiceTest(t)

	assert.NotPanics(t, func() {
		NotifyRootUser(dto.NotifyTypeChannelTest, "test", "test")
		notifyRootUserChannelUpdate(common.ChannelStatusAutoDisabled, "test", "test", "test")
	})
}

func TestDisableChannelSkipsNotificationWithoutChangingStatusUpdate(t *testing.T) {
	db := setupChannelNotificationServiceTest(t)
	disabled := false
	root := createRootUserForChannelNotificationTest(t, db, dto.UserSetting{ChannelAutoDisableNotifyEnabled: &disabled})

	channel := model.Channel{
		Name:   "channel-notify-disabled",
		Type:   1,
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, db.Create(&channel).Error)

	channelError := types.NewChannelError(channel.Id, channel.Type, channel.Name, false, channel.Key, true)
	DisableChannel(*channelError, "test error", 401)

	var stored model.Channel
	require.NoError(t, db.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
	assertChannelNotificationLimitUnused(t, root.Id, formatNotifyType(channel.Id, common.ChannelStatusAutoDisabled))
}

func TestEnableChannelSkipsRecoveryNotificationWithoutChangingStatusUpdate(t *testing.T) {
	db := setupChannelNotificationServiceTest(t)
	disabled := false
	root := createRootUserForChannelNotificationTest(t, db, dto.UserSetting{ChannelAutoRecoveryNotifyEnabled: &disabled})

	channel := model.Channel{
		Name:   "channel-recovery-notify-disabled",
		Type:   1,
		Key:    "test-key",
		Status: common.ChannelStatusAutoDisabled,
	}
	require.NoError(t, db.Create(&channel).Error)

	EnableChannel(channel.Id, "", channel.Name)

	var stored model.Channel
	require.NoError(t, db.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assertChannelNotificationLimitUnused(t, root.Id, formatNotifyType(channel.Id, common.ChannelStatusEnabled))
}

func TestDisableMultiKeyChannelPreservesPerKeyBehaviorWhenNotificationDisabled(t *testing.T) {
	db := setupChannelNotificationServiceTest(t)
	disabled := false
	root := createRootUserForChannelNotificationTest(t, db, dto.UserSetting{ChannelAutoDisableNotifyEnabled: &disabled})

	channel := model.Channel{
		Name:        "multi-key-notify-disabled",
		Type:        1,
		Key:         "first-key\nsecond-key",
		Status:      common.ChannelStatusEnabled,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true},
	}
	require.NoError(t, db.Create(&channel).Error)

	firstError := types.NewChannelError(channel.Id, channel.Type, channel.Name, true, "first-key", true)
	DisableChannel(*firstError, "first key error", 401)

	var stored model.Channel
	require.NoError(t, db.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[0])

	secondError := types.NewChannelError(channel.Id, channel.Type, channel.Name, true, "second-key", true)
	DisableChannel(*secondError, "second key error", 401)

	require.NoError(t, db.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[1])
	assertChannelNotificationLimitUnused(t, root.Id, formatNotifyType(channel.Id, common.ChannelStatusAutoDisabled))
}

func setupChannelNotificationServiceTest(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}))

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldMainDatabaseType := common.MainDatabaseType()
	oldLogDatabaseType := common.LogDatabaseType()
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	notifyLimitStore = sync.Map{}
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		common.SetDatabaseTypes(oldMainDatabaseType, oldLogDatabaseType)
		notifyLimitStore = sync.Map{}
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func createRootUserForChannelNotificationTest(t *testing.T, db *gorm.DB, setting dto.UserSetting) model.User {
	t.Helper()
	root := model.User{
		Username: fmt.Sprintf("root-%s", strings.ReplaceAll(t.Name(), "/", "-")),
		Password: "password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
	}
	root.SetSetting(setting)
	require.NoError(t, db.Create(&root).Error)
	return root
}

func assertChannelNotificationLimitUnused(t *testing.T, userID int, notifyType string) {
	t.Helper()
	prefix := fmt.Sprintf("%d:%s:", userID, notifyType)
	used := false
	notifyLimitStore.Range(func(key, _ any) bool {
		keyString, ok := key.(string)
		if ok && strings.HasPrefix(keyString, prefix) {
			used = true
			return false
		}
		return true
	})
	assert.False(t, used, "关闭通知时不应消耗通知限流次数")
}
