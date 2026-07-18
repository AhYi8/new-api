package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMultiKeyByIndex(t *testing.T) {
	channel := &Channel{
		Key: "enabled-key\nauto-disabled-key",
		ChannelInfo: ChannelInfo{
			IsMultiKey: true,
		},
	}

	key, err := channel.GetMultiKeyByIndex(1)
	require.Nil(t, err)
	assert.Equal(t, "auto-disabled-key", key)

	_, err = channel.GetMultiKeyByIndex(2)
	require.NotNil(t, err)
}

func TestRecoverAutoDisabledMultiKeysRevalidatesAndEnablesChannel(t *testing.T) {
	truncateTables(t)

	channel := &Channel{
		Name:   "recovery-channel",
		Key:    "recover-key\nmanual-key\nchanged-key",
		Status: common.ChannelStatusAutoDisabled,
		Group:  "default",
		Models: "gpt-4o-mini",
		ChannelInfo: ChannelInfo{
			IsMultiKey: true,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
				1: common.ChannelStatusManuallyDisabled,
				2: common.ChannelStatusAutoDisabled,
			},
			MultiKeyDisabledReason: map[int]string{0: "auto", 1: "manual", 2: "auto"},
			MultiKeyDisabledTime:   map[int]int64{0: 10, 1: 20, 2: 30},
		},
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-4o-mini",
		ChannelId: channel.Id,
		Enabled:   false,
	}).Error)

	result, err := RecoverAutoDisabledMultiKeys(channel.Id, map[int]string{
		0: "recover-key",
		1: "manual-key",
		2: "stale-key",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Recovered)
	assert.True(t, result.ChannelEnabled)

	updated, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, updated.Status)
	assert.NotContains(t, updated.ChannelInfo.MultiKeyStatusList, 0)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, updated.ChannelInfo.MultiKeyStatusList[1])
	assert.Equal(t, common.ChannelStatusAutoDisabled, updated.ChannelInfo.MultiKeyStatusList[2])
	assert.NotContains(t, updated.ChannelInfo.MultiKeyDisabledReason, 0)
	assert.NotContains(t, updated.ChannelInfo.MultiKeyDisabledTime, 0)
	assert.Equal(t, "auto", updated.ChannelInfo.MultiKeyDisabledReason[2])
	assert.Equal(t, int64(30), updated.ChannelInfo.MultiKeyDisabledTime[2])

	var ability Ability
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)
}

func TestRecoverAutoDisabledMultiKeysDoesNotOverrideManualChannelDisable(t *testing.T) {
	truncateTables(t)

	channel := &Channel{
		Name:   "manual-disabled-channel",
		Key:    "auto-disabled-key",
		Status: common.ChannelStatusManuallyDisabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:         true,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled},
		},
	}
	require.NoError(t, DB.Create(channel).Error)

	result, err := RecoverAutoDisabledMultiKeys(channel.Id, map[int]string{0: "auto-disabled-key"})
	require.NoError(t, err)
	assert.Zero(t, result.Recovered)
	assert.False(t, result.ChannelEnabled)

	updated, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, updated.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, updated.ChannelInfo.MultiKeyStatusList[0])
}

func TestEnabledChannelCanBeReaddedWithOneCallerCacheRefresh(t *testing.T) {
	truncateTables(t)

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		channelSyncLock.Lock()
		group2model2channels = nil
		channelsIDM = nil
		channel2advancedCustomConfig = nil
		channelSyncLock.Unlock()
	})

	channel := &Channel{
		Name:   "route-recovery-channel",
		Key:    "key",
		Status: common.ChannelStatusAutoDisabled,
		Group:  "default",
		Models: "gpt-4o-mini",
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-4o-mini",
		ChannelId: channel.Id,
		Enabled:   false,
	}).Error)
	InitChannelCache()

	selected, err := GetRandomSatisfiedChannel("default", "gpt-4o-mini", 0, "")
	require.NoError(t, err)
	assert.Nil(t, selected)

	require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, ""))
	selected, err = GetRandomSatisfiedChannel("default", "gpt-4o-mini", 0, "")
	require.NoError(t, err)
	assert.Nil(t, selected)

	InitChannelCache()
	selected, err = GetRandomSatisfiedChannel("default", "gpt-4o-mini", 0, "")
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, channel.Id, selected.Id)
}
