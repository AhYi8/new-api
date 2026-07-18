package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMonitorSettingKeepsDefaultSkipStatusCodeWhenOldConfigIsMissing(t *testing.T) {
	setting := MonitorSetting{MultiKeyAutoDisabledTestSkipStatusCodes: "401"}

	err := config.UpdateConfigFromMap(&setting, map[string]string{
		"auto_test_channel_enabled": "true",
	})

	require.NoError(t, err)
	assert.Equal(t, "401", setting.MultiKeyAutoDisabledTestSkipStatusCodes)
}

func TestGetMonitorSetting_ChannelTestEnabledEnvOverridesEnabledConfig(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	t.Setenv("CHANNEL_TEST_ENABLED", "false")
	t.Setenv("CHANNEL_TEST_FREQUENCY", "5")
	monitorSetting = MonitorSetting{
		AutoTestChannelEnabled: true,
		AutoTestChannelMinutes: 20,
	}

	setting := GetMonitorSetting()

	require.NotNil(t, setting)
	assert.False(t, setting.AutoTestChannelEnabled)
	assert.Equal(t, float64(5), setting.AutoTestChannelMinutes)
}

func TestGetMonitorSetting_ChannelTestEnabledEnvCanEnableDisabledConfig(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	t.Setenv("CHANNEL_TEST_ENABLED", "true")
	monitorSetting = MonitorSetting{
		AutoTestChannelEnabled: false,
		AutoTestChannelMinutes: 12,
	}

	setting := GetMonitorSetting()

	require.NotNil(t, setting)
	assert.True(t, setting.AutoTestChannelEnabled)
	assert.Equal(t, float64(12), setting.AutoTestChannelMinutes)
}

func TestGetMonitorSetting_NormalizesNegativeMultiKeyTestLimit(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	monitorSetting = MonitorSetting{MultiKeyAutoDisabledTestLimit: -1}

	setting := GetMonitorSetting()

	require.NotNil(t, setting)
	assert.Zero(t, setting.MultiKeyAutoDisabledTestLimit)
}
