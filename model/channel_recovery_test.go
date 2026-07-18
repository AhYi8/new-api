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

func TestGetMultiKeyDisabledStatusCode(t *testing.T) {
	tests := []struct {
		name     string
		info     ChannelInfo
		index    int
		expected int
	}{
		{
			name: "优先读取结构化状态码",
			info: ChannelInfo{
				MultiKeyDisabledStatusCode: map[int]int{0: 401},
				MultiKeyDisabledReason:     map[int]string{0: "status_code=500, upstream error"},
			},
			index:    0,
			expected: 401,
		},
		{
			name: "兼容解析历史禁用原因",
			info: ChannelInfo{
				MultiKeyDisabledReason: map[int]string{1: "status_code=403, invalid key"},
			},
			index:    1,
			expected: 403,
		},
		{
			name: "拒绝超出范围的历史状态码",
			info: ChannelInfo{
				MultiKeyDisabledReason: map[int]string{0: "status_code=99, invalid"},
			},
			index: 0,
		},
		{
			name: "未知原因不推断状态码",
			info: ChannelInfo{
				MultiKeyDisabledReason: map[int]string{0: "invalid key"},
			},
			index: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{ChannelInfo: tt.info}
			assert.Equal(t, tt.expected, channel.GetMultiKeyDisabledStatusCode(tt.index))
		})
	}
}

func TestHandlerMultiKeyUpdateMaintainsDisabledStatusCode(t *testing.T) {
	channel := &Channel{
		Key:    "key-1",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey: true,
		},
	}

	require.True(t, handlerMultiKeyUpdate(channel, "key-1", common.ChannelStatusAutoDisabled, "status_code=401, invalid key", 401))
	assert.Equal(t, 401, channel.ChannelInfo.MultiKeyDisabledStatusCode[0])
	assert.Equal(t, int64(1), channel.ChannelInfo.MultiKeyDisabledGeneration[0])

	require.True(t, handlerMultiKeyUpdate(channel, "key-1", common.ChannelStatusAutoDisabled, "status_code=401, invalid key", 401))
	assert.Equal(t, int64(2), channel.ChannelInfo.MultiKeyDisabledGeneration[0])

	require.True(t, handlerMultiKeyUpdate(channel, "key-1", common.ChannelStatusEnabled, "", 0))
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledStatusCode, 0)
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledGeneration, 0)

	channel.ChannelInfo.MultiKeyDisabledStatusCode = map[int]int{0: 500}
	require.True(t, handlerMultiKeyUpdate(channel, "key-1", common.ChannelStatusManuallyDisabled, "manual", 0))
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledStatusCode, 0)
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledGeneration, 0)

	require.False(t, handlerMultiKeyUpdate(channel, "key-1", common.ChannelStatusAutoDisabled, "status_code=401, invalid key", 401))
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.ChannelInfo.MultiKeyStatusList[0])
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledGeneration, 0)

	require.True(t, handlerMultiKeyUpdate(channel, "key-1", common.ChannelStatusEnabled, "", 0))
	require.True(t, handlerMultiKeyUpdate(channel, "key-1", common.ChannelStatusAutoDisabled, "status_code=401, invalid key", 401))
	assert.Equal(t, int64(3), channel.ChannelInfo.MultiKeyDisabledGeneration[0])
}

func TestUpdateChannelStatusPersistsMultiKeyGenerationWithMemoryCache(t *testing.T) {
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
		Name:   "generation-cache-channel",
		Key:    "key-1\nkey-2",
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: "gpt-4o-mini",
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
		},
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-4o-mini",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error)
	InitChannelCache()
	cachedBeforeUpdate, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	cachedBeforeUpdate.ChannelInfo.MultiKeyPollingIndex = 1

	require.True(t, UpdateChannelStatusWithDisabledStatusCode(
		channel.Id,
		"key-1",
		common.ChannelStatusAutoDisabled,
		"status_code=401, invalid key",
		401,
	))
	require.True(t, UpdateChannelStatusWithDisabledStatusCode(
		channel.Id,
		"key-1",
		common.ChannelStatusAutoDisabled,
		"status_code=401, invalid key",
		401,
	))

	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 401, stored.ChannelInfo.MultiKeyDisabledStatusCode[0])
	assert.Equal(t, int64(2), stored.ChannelInfo.MultiKeyDisabledGeneration[0])
	assert.Equal(t, int64(2), stored.ChannelInfo.MultiKeyGenerationCounter)

	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), cached.ChannelInfo.MultiKeyDisabledGeneration[0])
	assert.Equal(t, 1, cached.ChannelInfo.MultiKeyPollingIndex)
}

func TestUpdateChannelStatusDoesNotOverrideManualMultiKeyDisable(t *testing.T) {
	truncateTables(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCacheEnabled })

	channel := &Channel{
		Name:   "manual-key-channel",
		Key:    "key-1\nkey-2",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       2,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
		},
	}
	require.NoError(t, DB.Create(channel).Error)

	changed := UpdateChannelStatusWithDisabledStatusCode(
		channel.Id,
		"key-1",
		common.ChannelStatusAutoDisabled,
		"status_code=500, delayed failure",
		500,
	)
	assert.False(t, changed)

	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.NotContains(t, stored.ChannelInfo.MultiKeyDisabledStatusCode, 0)
	assert.NotContains(t, stored.ChannelInfo.MultiKeyDisabledGeneration, 0)

	stored.Status = common.ChannelStatusManuallyDisabled
	stored.ChannelInfo.MultiKeyStatusList = nil
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", stored.Id).Updates(map[string]any{
		"status":       stored.Status,
		"channel_info": stored.ChannelInfo,
	}).Error)
	changed = UpdateChannelStatusWithDisabledStatusCode(
		stored.Id,
		"key-1",
		common.ChannelStatusAutoDisabled,
		"status_code=500, delayed failure",
		500,
	)
	assert.False(t, changed)

	stored, err = GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	assert.NotContains(t, stored.ChannelInfo.MultiKeyStatusList, 0)
}

func TestChannelUpdateRejectsStaleMultiKeyGeneration(t *testing.T) {
	truncateTables(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCacheEnabled })

	channel := &Channel{
		Name:   "stale-generation-channel",
		Key:    "key-1\nkey-2",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:                 true,
			MultiKeySize:               2,
			MultiKeyStatusList:         map[int]int{0: common.ChannelStatusAutoDisabled},
			MultiKeyDisabledGeneration: map[int]int64{0: 1},
			MultiKeyGenerationCounter:  1,
		},
	}
	require.NoError(t, DB.Create(channel).Error)
	stale, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)

	require.True(t, UpdateChannelStatusWithDisabledStatusCode(
		channel.Id,
		"key-2",
		common.ChannelStatusAutoDisabled,
		"status_code=500, newer failure",
		500,
	))
	stale.Name = "stale update"
	err = stale.Update()
	require.ErrorContains(t, err, "渠道密钥状态已变化")

	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stored.ChannelInfo.MultiKeyGenerationCounter)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[1])
	assert.NotEqual(t, "stale update", stored.Name)

	staleAfterDisable := stored
	recovery, err := RecoverAutoDisabledMultiKeys(channel.Id, map[int]MultiKeyRecoveryCandidate{
		0: {
			Key:                "key-1",
			DisabledGeneration: 1,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, recovery.Recovered)

	recovered, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, int64(3), recovered.ChannelInfo.MultiKeyGenerationCounter)
	assert.NotContains(t, recovered.ChannelInfo.MultiKeyStatusList, 0)

	staleAfterDisable.Name = "stale after recovery"
	err = staleAfterDisable.Update()
	require.ErrorContains(t, err, "渠道密钥状态已变化")
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
			MultiKeyDisabledStatusCode: map[int]int{
				0: 401,
				2: 500,
			},
			MultiKeyDisabledGeneration: map[int]int64{0: 3, 2: 4},
		},
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-4o-mini",
		ChannelId: channel.Id,
		Enabled:   false,
	}).Error)

	result, err := RecoverAutoDisabledMultiKeys(channel.Id, map[int]MultiKeyRecoveryCandidate{
		0: {Key: "recover-key", DisabledReason: "auto", DisabledTime: 10, DisabledStatusCode: 401, DisabledGeneration: 3},
		1: {Key: "manual-key", DisabledReason: "manual", DisabledTime: 20},
		2: {Key: "stale-key", DisabledReason: "auto", DisabledTime: 30, DisabledStatusCode: 500, DisabledGeneration: 4},
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
	assert.NotContains(t, updated.ChannelInfo.MultiKeyDisabledStatusCode, 0)
	assert.NotContains(t, updated.ChannelInfo.MultiKeyDisabledGeneration, 0)
	assert.Equal(t, "auto", updated.ChannelInfo.MultiKeyDisabledReason[2])
	assert.Equal(t, int64(30), updated.ChannelInfo.MultiKeyDisabledTime[2])
	assert.Equal(t, 500, updated.ChannelInfo.MultiKeyDisabledStatusCode[2])
	assert.Equal(t, int64(4), updated.ChannelInfo.MultiKeyDisabledGeneration[2])

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

	result, err := RecoverAutoDisabledMultiKeys(channel.Id, map[int]MultiKeyRecoveryCandidate{0: {Key: "auto-disabled-key"}})
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
