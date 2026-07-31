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

	require.True(t, handlerMultiKeyUpdate(channel, "key-1", nil, common.ChannelStatusAutoDisabled, "status_code=401, invalid key", 401))
	assert.Equal(t, 401, channel.ChannelInfo.MultiKeyDisabledStatusCode[0])
	assert.Equal(t, int64(1), channel.ChannelInfo.MultiKeyDisabledGeneration[0])

	require.True(t, handlerMultiKeyUpdate(channel, "key-1", nil, common.ChannelStatusAutoDisabled, "status_code=401, invalid key", 401))
	assert.Equal(t, int64(2), channel.ChannelInfo.MultiKeyDisabledGeneration[0])

	require.True(t, handlerMultiKeyUpdate(channel, "key-1", nil, common.ChannelStatusEnabled, "", 0))
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledStatusCode, 0)
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledGeneration, 0)

	channel.ChannelInfo.MultiKeyDisabledStatusCode = map[int]int{0: 500}
	require.True(t, handlerMultiKeyUpdate(channel, "key-1", nil, common.ChannelStatusManuallyDisabled, "manual", 0))
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledStatusCode, 0)
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledGeneration, 0)

	require.False(t, handlerMultiKeyUpdate(channel, "key-1", nil, common.ChannelStatusAutoDisabled, "status_code=401, invalid key", 401))
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.ChannelInfo.MultiKeyStatusList[0])
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledGeneration, 0)

	require.True(t, handlerMultiKeyUpdate(channel, "key-1", nil, common.ChannelStatusEnabled, "", 0))
	require.True(t, handlerMultiKeyUpdate(channel, "key-1", nil, common.ChannelStatusAutoDisabled, "status_code=401, invalid key", 401))
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

	require.True(t, AutoDisableChannel(
		channel.Id,
		"key-1",
		common.GetPointer(0),
		common.GetPointer(int64(0)),
		"status_code=401, invalid key",
		401,
	))
	require.False(t, AutoDisableChannel(
		channel.Id,
		"key-1",
		common.GetPointer(0),
		common.GetPointer(int64(0)),
		"status_code=401, invalid key",
		401,
	))

	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 401, stored.ChannelInfo.MultiKeyDisabledStatusCode[0])
	assert.Equal(t, int64(1), stored.ChannelInfo.MultiKeyDisabledGeneration[0])
	assert.Equal(t, int64(1), stored.ChannelInfo.MultiKeyGenerationCounter)
	assert.Equal(t, int64(1), stored.ChannelInfo.StateGeneration)

	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cached.ChannelInfo.MultiKeyDisabledGeneration[0])
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

	changed := AutoDisableChannel(
		channel.Id,
		"key-1",
		common.GetPointer(0),
		common.GetPointer(int64(0)),
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
	changed = AutoDisableChannel(
		stored.Id,
		"key-1",
		common.GetPointer(0),
		common.GetPointer(int64(0)),
		"status_code=500, delayed failure",
		500,
	)
	assert.False(t, changed)

	stored, err = GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	assert.NotContains(t, stored.ChannelInfo.MultiKeyStatusList, 0)
}

func TestAutoDisableChannelUsesExactMultiKeyIndex(t *testing.T) {
	truncateTables(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCacheEnabled })

	channel := &Channel{
		Name:    "duplicate-key-channel",
		Key:     "same-key\nsame-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
		ChannelInfo: ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       2,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
		},
	}
	require.NoError(t, DB.Create(channel).Error)

	changed := AutoDisableChannel(channel.Id, "same-key", common.GetPointer(1), common.GetPointer(int64(0)), "status_code=401, invalid key", 401)
	require.True(t, changed)

	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[1])
	assert.Equal(t, 401, stored.ChannelInfo.MultiKeyDisabledStatusCode[1])
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
}

func TestAutoDisableChannelRejectsMissingOrStaleMultiKeyTarget(t *testing.T) {
	truncateTables(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCacheEnabled })

	channel := &Channel{
		Name:    "stale-key-target-channel",
		Key:     "first-key\nsecond-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
		},
	}
	require.NoError(t, DB.Create(channel).Error)

	assert.False(t, AutoDisableChannel(channel.Id, "first-key", common.GetPointer(0), nil, "missing generation", 401))
	assert.False(t, AutoDisableChannel(channel.Id, "first-key", nil, common.GetPointer(int64(0)), "missing index", 401))
	assert.False(t, AutoDisableChannel(channel.Id, "first-key", common.GetPointer(2), common.GetPointer(int64(0)), "invalid index", 401))
	assert.False(t, AutoDisableChannel(channel.Id, "changed-key", common.GetPointer(0), common.GetPointer(int64(0)), "changed key", 401))
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("key", "second-key\nfirst-key").Error)
	assert.False(t, AutoDisableChannel(channel.Id, "first-key", common.GetPointer(0), common.GetPointer(int64(0)), "reordered key", 401))

	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Empty(t, stored.ChannelInfo.MultiKeyStatusList)
}

func TestUpdateChannelStatusRejectsAutoDisabledStatus(t *testing.T) {
	truncateTables(t)
	channel := &Channel{
		Name:    "unverified-auto-disable",
		Key:     "key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	require.NoError(t, DB.Create(channel).Error)

	assert.False(t, UpdateChannelStatus(channel.Id, channel.Key, common.ChannelStatusAutoDisabled, "unverified"))
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, int64(0), stored.ChannelInfo.StateGeneration)
}

func TestAutoDisableChannelRechecksLatestSettingsAndManualStatus(t *testing.T) {
	tests := []struct {
		name         string
		channel      Channel
		usingKey     string
		usingIndex   *int
		assertStored func(t *testing.T, stored Channel)
	}{
		{
			name: "最新 AutoBan 已关闭",
			channel: Channel{
				Name:    "auto-ban-disabled",
				Key:     "key",
				Status:  common.ChannelStatusEnabled,
				AutoBan: common.GetPointer(0),
			},
			usingKey: "key",
			assertStored: func(t *testing.T, stored Channel) {
				assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
			},
		},
		{
			name: "单密钥渠道已手动禁用",
			channel: Channel{
				Name:    "manual-single-key",
				Key:     "key",
				Status:  common.ChannelStatusManuallyDisabled,
				AutoBan: common.GetPointer(1),
			},
			usingKey: "key",
			assertStored: func(t *testing.T, stored Channel) {
				assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
			},
		},
		{
			name: "单密钥文本已变更",
			channel: Channel{
				Name:    "changed-single-key",
				Key:     "new-key",
				Status:  common.ChannelStatusEnabled,
				AutoBan: common.GetPointer(1),
			},
			usingKey: "old-key",
			assertStored: func(t *testing.T, stored Channel) {
				assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
			},
		},
		{
			name: "多密钥目标已手动禁用",
			channel: Channel{
				Name:    "manual-multi-key",
				Key:     "key\nother-key",
				Status:  common.ChannelStatusEnabled,
				AutoBan: common.GetPointer(1),
				ChannelInfo: ChannelInfo{
					IsMultiKey:         true,
					MultiKeySize:       2,
					MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
				},
			},
			usingKey:   "key",
			usingIndex: common.GetPointer(0),
			assertStored: func(t *testing.T, stored Channel) {
				assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
				assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateTables(t)
			originalMemoryCacheEnabled := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = false
			t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCacheEnabled })
			require.NoError(t, DB.Create(&tt.channel).Error)

			assert.False(t, AutoDisableChannel(tt.channel.Id, tt.usingKey, tt.usingIndex, common.GetPointer(tt.channel.ChannelInfo.StateGeneration), "delayed failure", 401))

			stored, err := GetChannelById(tt.channel.Id, true)
			require.NoError(t, err)
			tt.assertStored(t, *stored)
		})
	}
}

func TestAutoDisableChannelAllowsChannelWideDisableWithoutKey(t *testing.T) {
	truncateTables(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCacheEnabled })

	channel := &Channel{
		Name:    "channel-wide-disable",
		Key:     "first-key\nsecond-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
		},
	}
	require.NoError(t, DB.Create(channel).Error)

	require.True(t, AutoDisableChannel(channel.Id, "", nil, nil, "余额不足", 0))

	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
	assert.Empty(t, stored.ChannelInfo.MultiKeyStatusList)
}

func TestAutoDisableChannelRejectsFailureBeforeManualReenable(t *testing.T) {
	tests := []struct {
		name    string
		channel Channel
		key     string
		index   *int
	}{
		{
			name: "单密钥",
			channel: Channel{
				Name:    "single-key-aba",
				Key:     "single-key",
				Status:  common.ChannelStatusEnabled,
				AutoBan: common.GetPointer(1),
			},
			key: "single-key",
		},
		{
			name: "多密钥",
			channel: Channel{
				Name:    "multi-key-aba",
				Key:     "first-key\nsecond-key",
				Status:  common.ChannelStatusEnabled,
				AutoBan: common.GetPointer(1),
				ChannelInfo: ChannelInfo{
					IsMultiKey:   true,
					MultiKeySize: 2,
				},
			},
			key:   "second-key",
			index: common.GetPointer(1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateTables(t)
			originalMemoryCacheEnabled := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = false
			t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCacheEnabled })
			require.NoError(t, DB.Create(&tt.channel).Error)

			requestGeneration := int64(0)
			require.True(t, UpdateChannelStatus(tt.channel.Id, tt.key, common.ChannelStatusManuallyDisabled, "manual disable"))
			require.True(t, UpdateChannelStatus(tt.channel.Id, tt.key, common.ChannelStatusEnabled, "manual enable"))

			assert.False(t, AutoDisableChannel(tt.channel.Id, tt.key, tt.index, &requestGeneration, "stale failure", 401))

			stored, err := GetChannelById(tt.channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
			assert.Equal(t, int64(2), stored.ChannelInfo.StateGeneration)
			if tt.index != nil {
				assert.NotContains(t, stored.ChannelInfo.MultiKeyStatusList, *tt.index)
			}
		})
	}
}

func TestChannelStatusByTagAdvancesStateGeneration(t *testing.T) {
	truncateTables(t)
	channel := &Channel{
		Name:    "tag-status-generation",
		Key:     "key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
		Tag:     common.GetPointer("shared-tag"),
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{ChannelId: channel.Id, Group: "default", Model: "gpt-4o-mini", Enabled: true}).Error)

	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", false).Error)
	require.NoError(t, EnableChannelByTag("shared-tag"))
	var ability Ability
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)

	require.NoError(t, DisableChannelByTag("shared-tag"))
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", true).Error)
	require.NoError(t, DisableChannelByTag("shared-tag"))
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.False(t, ability.Enabled)
	require.NoError(t, EnableChannelByTag("shared-tag"))

	requestGeneration := int64(0)
	assert.False(t, AutoDisableChannel(channel.Id, channel.Key, nil, &requestGeneration, "stale failure", 401))
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, int64(2), stored.ChannelInfo.StateGeneration)
}

func TestEditChannelByTagAdvancesStateGeneration(t *testing.T) {
	truncateTables(t)
	oldOverride := `{"X-Request-Version":"old"}`
	newOverride := `{"X-Request-Version":"new"}`
	channel := &Channel{
		Name:           "tag-edit-generation",
		Key:            "key",
		Status:         common.ChannelStatusEnabled,
		AutoBan:        common.GetPointer(1),
		Tag:            common.GetPointer("editable-tag"),
		HeaderOverride: &oldOverride,
	}
	require.NoError(t, DB.Create(channel).Error)

	require.NoError(t, EditChannelByTag("editable-tag", nil, nil, nil, nil, nil, nil, nil, &newOverride))

	requestGeneration := int64(0)
	assert.False(t, AutoDisableChannel(channel.Id, channel.Key, nil, &requestGeneration, "stale failure", 401))
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.NotNil(t, stored.HeaderOverride)
	assert.Equal(t, newOverride, *stored.HeaderOverride)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, int64(1), stored.ChannelInfo.StateGeneration)
}

func TestCacheUpdateChannelIfNewerRejectsOlderOrEqualState(t *testing.T) {
	truncateTables(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		InitChannelCache()
	})

	channel := &Channel{
		Name:    "admin-latest-channel",
		Key:     "admin-key",
		Status:  common.ChannelStatusEnabled,
		Group:   "default",
		Models:  "gpt-4o-mini",
		AutoBan: common.GetPointer(0),
		ChannelInfo: ChannelInfo{
			StateGeneration:      2,
			MultiKeyPollingIndex: 7,
		},
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{ChannelId: channel.Id, Group: "default", Model: "gpt-4o-mini", Enabled: true}).Error)
	InitChannelCache()
	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	cached.ChannelInfo.MultiKeyPollingIndex = 7

	for _, generation := range []int64{1, 2} {
		CacheUpdateChannelIfNewer(&Channel{
			Id:      channel.Id,
			Name:    "stale-auto-disable",
			Status:  common.ChannelStatusAutoDisabled,
			AutoBan: common.GetPointer(1),
			ChannelInfo: ChannelInfo{
				StateGeneration: generation,
			},
		})
		cached, err = CacheGetChannel(channel.Id)
		require.NoError(t, err)
		assert.Equal(t, "admin-latest-channel", cached.Name)
		assert.Equal(t, "admin-key", cached.Key)
		assert.Equal(t, common.ChannelStatusEnabled, cached.Status)
		assert.False(t, cached.GetAutoBan())
	}

	CacheUpdateChannelIfNewer(&Channel{
		Id:     channel.Id,
		Name:   "new-auto-disable",
		Status: common.ChannelStatusAutoDisabled,
		ChannelInfo: ChannelInfo{
			StateGeneration: 3,
		},
	})
	cached, err = CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, "new-auto-disable", cached.Name)
	assert.Equal(t, common.ChannelStatusAutoDisabled, cached.Status)
	assert.Equal(t, 7, cached.ChannelInfo.MultiKeyPollingIndex)
	assert.NotContains(t, group2model2channels["default"]["gpt-4o-mini"], channel.Id)
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

	require.True(t, AutoDisableChannel(
		channel.Id,
		"key-2",
		common.GetPointer(1),
		common.GetPointer(int64(0)),
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

func TestReconcileAutoDisabledMultiKeyChannel(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		status     int
		isMultiKey bool
		statusList map[int]int
		expected   bool
	}{
		{
			name:       "全部密钥启用时恢复",
			key:        "key-1\nkey-2",
			status:     common.ChannelStatusAutoDisabled,
			isMultiKey: true,
			expected:   true,
		},
		{
			name:       "部分密钥启用时恢复",
			key:        "key-1\nkey-2",
			status:     common.ChannelStatusAutoDisabled,
			isMultiKey: true,
			statusList: map[int]int{0: common.ChannelStatusAutoDisabled},
			expected:   true,
		},
		{
			name:       "全部密钥禁用时不恢复",
			key:        "key-1\nkey-2",
			status:     common.ChannelStatusAutoDisabled,
			isMultiKey: true,
			statusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
				1: common.ChannelStatusManuallyDisabled,
			},
		},
		{
			name:       "手动禁用渠道不恢复",
			key:        "key-1",
			status:     common.ChannelStatusManuallyDisabled,
			isMultiKey: true,
		},
		{
			name:       "已启用渠道保持不变",
			key:        "key-1",
			status:     common.ChannelStatusEnabled,
			isMultiKey: true,
		},
		{
			name:       "空密钥渠道保持不变",
			status:     common.ChannelStatusAutoDisabled,
			isMultiKey: true,
		},
		{
			name:       "单密钥渠道保持不变",
			key:        "key-1",
			status:     common.ChannelStatusAutoDisabled,
			isMultiKey: false,
		},
		{
			name:       "越界状态不影响实际密钥判断",
			key:        "key-1",
			status:     common.ChannelStatusAutoDisabled,
			isMultiKey: true,
			statusList: map[int]int{1: common.ChannelStatusAutoDisabled},
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateTables(t)
			channel := &Channel{
				Name:      tt.name,
				Key:       tt.key,
				Status:    tt.status,
				Group:     "default",
				Models:    "gpt-4o-mini",
				OtherInfo: `{"status_reason":"All keys are disabled","status_time":123,"keep":"value"}`,
				ChannelInfo: ChannelInfo{
					IsMultiKey:         tt.isMultiKey,
					MultiKeyStatusList: tt.statusList,
				},
			}
			require.NoError(t, DB.Create(channel).Error)
			require.NoError(t, DB.Create(&Ability{
				Group:     "default",
				Model:     "gpt-4o-mini",
				ChannelId: channel.Id,
				Enabled:   false,
			}).Error)

			reconciled, err := ReconcileAutoDisabledMultiKeyChannel(channel.Id)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, reconciled)

			stored, err := GetChannelById(channel.Id, true)
			require.NoError(t, err)
			var ability Ability
			require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
			if tt.expected {
				assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
				assert.True(t, ability.Enabled)
				otherInfo := stored.GetOtherInfo()
				assert.NotContains(t, otherInfo, "status_reason")
				assert.NotContains(t, otherInfo, "status_time")
				assert.Equal(t, "value", otherInfo["keep"])

				reconciled, err = ReconcileAutoDisabledMultiKeyChannel(channel.Id)
				require.NoError(t, err)
				assert.False(t, reconciled)
				return
			}

			assert.Equal(t, tt.status, stored.Status)
			assert.False(t, ability.Enabled)
			assert.Contains(t, stored.GetOtherInfo(), "status_reason")
		})
	}
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
