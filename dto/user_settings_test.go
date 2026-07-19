package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelNotificationSettingsDefaultToEnabled(t *testing.T) {
	setting := UserSetting{}

	assert.True(t, setting.IsChannelAutoDisableNotifyEnabled())
	assert.True(t, setting.IsChannelAutoRecoveryNotifyEnabled())
}

func TestChannelNotificationSettingsRespectExplicitValues(t *testing.T) {
	disabled := false
	enabled := true
	setting := UserSetting{
		ChannelAutoDisableNotifyEnabled:  &disabled,
		ChannelAutoRecoveryNotifyEnabled: &enabled,
	}

	assert.False(t, setting.IsChannelAutoDisableNotifyEnabled())
	assert.True(t, setting.IsChannelAutoRecoveryNotifyEnabled())
}

func TestChannelNotificationSettingsPreserveExplicitFalseInJSON(t *testing.T) {
	disabled := false
	setting := UserSetting{ChannelAutoDisableNotifyEnabled: &disabled}

	data, err := common.Marshal(setting)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"channel_auto_disable_notify_enabled":false`)

	var decoded UserSetting
	require.NoError(t, common.Unmarshal(data, &decoded))
	assert.False(t, decoded.IsChannelAutoDisableNotifyEnabled())
}
