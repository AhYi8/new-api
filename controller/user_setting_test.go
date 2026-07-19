package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpdateUserSettingRootCanUpdateChannelNotificationSettings(t *testing.T) {
	db := setupUserSettingControllerTestDB(t)
	disabled := false
	enabled := true

	user := model.User{
		Username: "root-channel-notify",
		Password: "password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
	}
	user.SetSetting(dto.UserSetting{
		NotifyType:                       dto.NotifyTypeWebhook,
		QuotaWarningThreshold:            2,
		WebhookUrl:                       "https://example.com/hook",
		WebhookSecret:                    "secret",
		BarkUrl:                          "https://example.com/bark",
		GotifyUrl:                        "https://example.com/gotify",
		GotifyToken:                      "gotify-token",
		ChannelAutoDisableNotifyEnabled:  &enabled,
		ChannelAutoRecoveryNotifyEnabled: &disabled,
		SidebarModules:                   `{"admin":["channel"]}`,
		BillingPreference:                "subscription",
		Language:                         "zh",
	})
	require.NoError(t, db.Create(&user).Error)

	context, recorder := newUserSettingTestContext(t, user.Id, `{"notify_type":"email","quota_warning_threshold":1,"notification_email":"root@example.com","channel_auto_disable_notify_enabled":false}`)
	UpdateUserSetting(context)

	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	setting := stored.GetSetting()
	require.NotNil(t, setting.ChannelAutoDisableNotifyEnabled)
	require.NotNil(t, setting.ChannelAutoRecoveryNotifyEnabled)
	assert.False(t, *setting.ChannelAutoDisableNotifyEnabled)
	assert.False(t, *setting.ChannelAutoRecoveryNotifyEnabled)
	assert.Equal(t, "root@example.com", setting.NotificationEmail)
	assert.Equal(t, "https://example.com/hook", setting.WebhookUrl)
	assert.Equal(t, "secret", setting.WebhookSecret)
	assert.Equal(t, "https://example.com/bark", setting.BarkUrl)
	assert.Equal(t, "https://example.com/gotify", setting.GotifyUrl)
	assert.Equal(t, "gotify-token", setting.GotifyToken)
	assert.Equal(t, `{"admin":["channel"]}`, setting.SidebarModules)
	assert.Equal(t, "subscription", setting.BillingPreference)
	assert.Equal(t, "zh", setting.Language)

	context, recorder = newUserSettingTestContext(t, user.Id, `{"notify_type":"email","quota_warning_threshold":1,"notification_email":"root@example.com","channel_auto_recovery_notify_enabled":true}`)
	UpdateUserSetting(context)
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)

	require.NoError(t, db.First(&stored, user.Id).Error)
	setting = stored.GetSetting()
	require.NotNil(t, setting.ChannelAutoDisableNotifyEnabled)
	require.NotNil(t, setting.ChannelAutoRecoveryNotifyEnabled)
	assert.False(t, *setting.ChannelAutoDisableNotifyEnabled)
	assert.True(t, *setting.ChannelAutoRecoveryNotifyEnabled)
}

func TestUpdateUserSettingNonRootCannotUpdateChannelNotificationSettings(t *testing.T) {
	db := setupUserSettingControllerTestDB(t)
	disabled := false
	enabled := true
	user := model.User{
		Username: "admin-channel-notify",
		Password: "password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
	}
	user.SetSetting(dto.UserSetting{
		NotifyType:                       dto.NotifyTypeEmail,
		QuotaWarningThreshold:            1,
		ChannelAutoDisableNotifyEnabled:  &disabled,
		ChannelAutoRecoveryNotifyEnabled: &enabled,
	})
	require.NoError(t, db.Create(&user).Error)

	context, recorder := newUserSettingTestContext(t, user.Id, `{"notify_type":"email","quota_warning_threshold":1,"channel_auto_disable_notify_enabled":true,"channel_auto_recovery_notify_enabled":false}`)
	UpdateUserSetting(context)

	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	setting := stored.GetSetting()
	require.NotNil(t, setting.ChannelAutoDisableNotifyEnabled)
	require.NotNil(t, setting.ChannelAutoRecoveryNotifyEnabled)
	assert.False(t, *setting.ChannelAutoDisableNotifyEnabled)
	assert.True(t, *setting.ChannelAutoRecoveryNotifyEnabled)
}

func newUserSettingTestContext(t *testing.T, userID int, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", userID)
	context.Request = httptest.NewRequest("PUT", "/api/user/setting", strings.NewReader(body))
	return context, recorder
}

func setupUserSettingControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldRedisEnabled := common.RedisEnabled
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
	})
	return setupModelListControllerTestDB(t)
}
