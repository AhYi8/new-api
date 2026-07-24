package model

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelAliasGroupTest(t *testing.T) {
	t.Helper()
	oldDB := DB
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldMainDatabaseType := common.MainDatabaseType()
	oldLogDatabaseType := common.LogDatabaseType()

	dsn := fmt.Sprintf("file:model-alias-group-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}, &Channel{}, &Ability{}))
	DB = db
	common.MemoryCacheEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	initCol()

	common.OptionMapRWMutex.Lock()
	oldOptionMap := common.OptionMap
	common.OptionMap = map[string]string{
		ModelAliasGroupsOptionKey:        "[]",
		ModelAliasScanEnabledOptionKey:   "true",
		ModelAliasScanIntervalOptionKey:  "30",
		ModelAliasPendingCountsOptionKey: "{}",
		modelAliasScanRevisionOptionKey:  "",
	}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		DB = oldDB
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		common.SetDatabaseTypes(oldMainDatabaseType, oldLogDatabaseType)
		initCol()
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
}

func TestNormalizeModelAliasGroupsValidatesAndDeduplicates(t *testing.T) {
	normalized, err := NormalizeModelAliasGroups([]ModelAliasGroup{
		{Alias: " deepseek-v4 ", Models: []string{"vendor/model", " vendor/model ", "Vendor/Model"}},
	})
	require.NoError(t, err)
	require.Len(t, normalized, 1)
	assert.Equal(t, "deepseek-v4", normalized[0].Alias)
	assert.Equal(t, []string{"vendor/model", "Vendor/Model"}, normalized[0].Models)

	testCases := []struct {
		name   string
		groups []ModelAliasGroup
	}{
		{name: "空统一名称", groups: []ModelAliasGroup{{Models: []string{"vendor/model"}}}},
		{name: "没有供应商名称", groups: []ModelAliasGroup{{Alias: "alias"}}},
		{name: "统一名称作为供应商名称", groups: []ModelAliasGroup{{Alias: "alias", Models: []string{"alias"}}}},
		{
			name: "跨组重复供应商名称",
			groups: []ModelAliasGroup{
				{Alias: "alias-a", Models: []string{"vendor/model"}},
				{Alias: "alias-b", Models: []string{"vendor/model"}},
			},
		},
		{
			name: "供应商名称与其他统一名称重复",
			groups: []ModelAliasGroup{
				{Alias: "alias-a", Models: []string{"vendor/model"}},
				{Alias: "vendor/model", Models: []string{"vendor/other"}},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NormalizeModelAliasGroups(testCase.groups)
			assert.Error(t, err)
		})
	}
}

func TestFilterModelAliasCatalogMatchesNamesAndExcludesExactAlias(t *testing.T) {
	pricing := []Pricing{
		{ModelName: "deepseek-v4-pro"},
		{ModelName: "nvidia/DeepSeek-V4-Pro"},
		{ModelName: "deepseek-ai/deepseek-v4-pro"},
		{ModelName: "deepseek-ai/deepseek-v4-pro"},
		{ModelName: "unrelated-model", Description: "deepseek-v4-pro"},
		{ModelName: "  "},
	}

	matched := filterModelAliasCatalog(pricing, "deepseek-v4-pro")

	assert.Equal(t, []string{
		"deepseek-ai/deepseek-v4-pro",
		"nvidia/DeepSeek-V4-Pro",
	}, matched)
}

func TestSearchModelAliasCatalogValidatesKeyword(t *testing.T) {
	_, err := SearchModelAliasCatalog("  ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "统一名称不能为空")

	_, err = SearchModelAliasCatalog(strings.Repeat("a", maxModelAliasNameLength+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不能超过")
}

func TestParseModelAliasChannelMappingRejectsNull(t *testing.T) {
	raw := "null"
	_, err := parseModelAliasChannelMapping(&raw)
	assert.EqualError(t, err, "模型映射必须是 JSON 对象")
}

func TestClassifyModelAliasChannelRejectsMappingCycle(t *testing.T) {
	group := ModelAliasGroup{Alias: "alias", Models: []string{"vendor/model"}}
	channel := newModelAliasTestChannel("环形映射", "vendor/model", map[string]string{
		"vendor/model": "alias",
	})

	item, _ := classifyModelAliasChannel(channel, group)

	assert.Equal(t, ModelAliasPreviewStatusConflict, item.Status)
	assert.Equal(t, "mapping_target_conflict", item.Reason)
}

func TestModelAliasGroupPreviewAndApply(t *testing.T) {
	setupModelAliasGroupTest(t)
	groups, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)
	require.Len(t, groups, 1)

	channels := []*Channel{
		newModelAliasTestChannel("新增", "vendor/a", map[string]string{"other": "target"}),
		newModelAliasTestChannel("已一致", "vendor/b,deepseek-v4-pro", map[string]string{"deepseek-v4-pro": "vendor/b"}),
		newModelAliasTestChannel("更新旧目标", "vendor/b", map[string]string{"deepseek-v4-pro": "vendor/a"}),
		newModelAliasTestChannel("多个目标", "vendor/a,vendor/b", nil),
		newModelAliasTestChannel("组外冲突", "vendor/a", map[string]string{"deepseek-v4-pro": "outside/model"}),
		newModelAliasTestChannel("别名已是直接模型", "vendor/a,deepseek-v4-pro", nil),
		newModelAliasTestChannel("大小写不匹配", "Vendor/A", nil),
		newModelAliasTestChannelWithRawMapping("无效映射", "vendor/a", "{"),
		newModelAliasTestChannel("只缺别名", "vendor/a", map[string]string{"deepseek-v4-pro": "vendor/a"}),
	}
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(DB))
	}

	preview, err := PreviewModelAliasGroup("deepseek-v4-pro")
	require.NoError(t, err)
	assert.Equal(t, 2, preview.Counts[ModelAliasPreviewStatusNew])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusUnchanged])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusUpdated])
	assert.Equal(t, 3, preview.Counts[ModelAliasPreviewStatusConflict])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusMultipleMatches])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusUnmatched])

	result, err := ApplyModelAliasGroup("deepseek-v4-pro")
	require.NoError(t, err)
	assert.Equal(t, 3, result.Applied)
	assert.Equal(t, 6, result.Skipped)
	assert.Empty(t, result.Failed)

	assertModelAliasChannel(t, channels[0].Id, "vendor/a", true)
	assertModelAliasChannel(t, channels[2].Id, "vendor/b", true)
	assertModelAliasChannel(t, channels[8].Id, "vendor/a", true)

	var newChannelMapping map[string]string
	var newChannel Channel
	require.NoError(t, DB.First(&newChannel, channels[0].Id).Error)
	require.NoError(t, common.UnmarshalJsonStr(*newChannel.ModelMapping, &newChannelMapping))
	assert.Equal(t, "target", newChannelMapping["other"])

	var conflictChannel Channel
	require.NoError(t, DB.First(&conflictChannel, channels[4].Id).Error)
	assert.NotContains(t, conflictChannel.Models, "deepseek-v4-pro")
	var conflictMapping map[string]string
	require.NoError(t, common.UnmarshalJsonStr(*conflictChannel.ModelMapping, &conflictMapping))
	assert.Equal(t, "outside/model", conflictMapping["deepseek-v4-pro"])

	secondResult, err := ApplyModelAliasGroup("deepseek-v4-pro")
	require.NoError(t, err)
	assert.Zero(t, secondResult.Applied)
	assert.Equal(t, len(channels), secondResult.Skipped)
	assert.Empty(t, secondResult.Failed)
}

func TestApplyModelAliasGroupWithSelectionAppliesOnlySelectedChannels(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)

	channels := []*Channel{
		newModelAliasTestChannel("渠道 A", "vendor/a", nil),
		newModelAliasTestChannel("渠道 B", "vendor/b", nil),
	}
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(DB))
	}

	result, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channels[1].Id},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Applied)
	assert.Equal(t, 1, result.Skipped)
	assert.Empty(t, result.Failed)

	assertModelAliasChannel(t, channels[1].Id, "vendor/b", true)
	var skippedChannel Channel
	require.NoError(t, DB.First(&skippedChannel, channels[0].Id).Error)
	assert.NotContains(t, skippedChannel.GetModels(), "deepseek-v4-pro")
}

func TestApplyModelAliasGroupWithSelectionAppliesSelectedMultipleMatchTarget(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)
	channel := newModelAliasTestChannel("多匹配", "vendor/a,vendor/b", nil)
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))

	result, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channel.Id},
		TargetModels:       map[int]string{channel.Id: "vendor/b"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Applied)
	assert.Zero(t, result.Skipped)
	assert.Empty(t, result.Failed)
	assertModelAliasChannel(t, channel.Id, "vendor/b", true)

	preview, err := PreviewModelAliasGroup("deepseek-v4-pro")
	require.NoError(t, err)
	require.Len(t, preview.Items, 1)
	assert.Equal(t, ModelAliasPreviewStatusMultipleMatches, preview.Items[0].Status)
	assert.Equal(t, "vendor/b", preview.Items[0].CurrentTarget)
	assert.Empty(t, preview.Items[0].ProposedTarget)
	assert.Zero(t, preview.Counts[ModelAliasPreviewStatusUnchanged])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusMultipleMatches])

	result, err = ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channel.Id},
		TargetModels:       map[int]string{channel.Id: "vendor/a"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Applied)
	assert.Zero(t, result.Skipped)
	assert.Empty(t, result.Failed)
	assertModelAliasChannel(t, channel.Id, "vendor/a", true)
}

func TestApplyModelAliasGroupWithSelectionRejectsInvalidTarget(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)
	channel := newModelAliasTestChannel("多匹配", "vendor/a,vendor/b", nil)
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))

	result, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channel.Id},
		TargetModels:       map[int]string{channel.Id: "outside/model"},
	})
	require.NoError(t, err)
	assert.Zero(t, result.Applied)
	assert.Zero(t, result.Skipped)
	require.Len(t, result.Failed, 1)
	assert.Contains(t, result.Failed[0].Error, "目标模型不在匹配结果中")

	var unchanged Channel
	require.NoError(t, DB.First(&unchanged, channel.Id).Error)
	assert.NotContains(t, unchanged.GetModels(), "deepseek-v4-pro")
}

func TestApplyModelAliasGroupWithSelectionRejectsInvalidChannelIDs(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a"}},
	})
	require.NoError(t, err)
	channel := newModelAliasTestChannel("渠道 A", "vendor/a", nil)
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))

	tests := []struct {
		name      string
		channelID int
	}{
		{name: "非正数渠道 ID", channelID: 0},
		{name: "预览外渠道 ID", channelID: channel.Id + 1000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
				SelectedChannelIDs: []int{test.channelID},
			})
			require.Error(t, err)

			var unchanged Channel
			require.NoError(t, DB.First(&unchanged, channel.Id).Error)
			assert.NotContains(t, unchanged.GetModels(), "deepseek-v4-pro")
		})
	}
}

func TestApplyModelAliasGroupWithSelectionRejectsCyclicMultipleMatchTarget(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasGroups([]ModelAliasGroup{
		{Alias: "deepseek-v4-pro", Models: []string{"vendor/a", "vendor/b"}},
	})
	require.NoError(t, err)
	channel := newModelAliasTestChannel("多匹配", "vendor/a,vendor/b", map[string]string{
		"vendor/a": "deepseek-v4-pro",
	})
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))

	result, err := ApplyModelAliasGroupWithSelection("deepseek-v4-pro", ModelAliasApplySelection{
		SelectedChannelIDs: []int{channel.Id},
		TargetModels:       map[int]string{channel.Id: "vendor/a"},
	})
	require.NoError(t, err)
	assert.Zero(t, result.Applied)
	assert.Zero(t, result.Skipped)
	require.Len(t, result.Failed, 1)
	assert.Contains(t, result.Failed[0].Error, "循环映射")

	var unchanged Channel
	require.NoError(t, DB.First(&unchanged, channel.Id).Error)
	assert.NotContains(t, unchanged.GetModels(), "deepseek-v4-pro")
}

func TestModelAliasScanCountsPendingChannelsAndKeepsLastSuccessfulResult(t *testing.T) {
	setupModelAliasGroupTest(t)
	configuration, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/a", "vendor/old"}},
	}, true, 30)
	require.NoError(t, err)
	require.Nil(t, configuration.Groups[0].PendingCount)

	channels := []*Channel{
		newModelAliasTestChannel("新增", "vendor/a", nil),
		newModelAliasTestChannel("更新", "vendor/a", map[string]string{"alias": "vendor/old"}),
		newModelAliasTestChannel("多匹配", "vendor/a,vendor/old", nil),
		newModelAliasTestChannel("冲突", "vendor/a", map[string]string{"alias": "outside/model"}),
		newModelAliasTestChannel("已一致", "vendor/a,alias", map[string]string{"alias": "vendor/a"}),
		newModelAliasTestChannelWithRawMapping("无关坏映射", "unrelated/model", "{"),
	}
	channels[1].Status = common.ChannelStatusManuallyDisabled
	channels[2].Status = common.ChannelStatusManuallyDisabled
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
	}

	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.ScannedGroups)
	assert.Equal(t, 6, summary.ScannedChannels)
	assert.Equal(t, 4, summary.PendingCount)
	assert.True(t, summary.IsCurrent())

	configuration, err = GetModelAliasConfiguration()
	require.NoError(t, err)
	require.NotNil(t, configuration.Groups[0].PendingCount)
	assert.Equal(t, 4, *configuration.Groups[0].PendingCount)

	preview, err := PreviewModelAliasGroup("alias")
	require.NoError(t, err)
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusUnmatched])
	assert.Equal(t, 1, preview.Counts[ModelAliasPreviewStatusConflict])

	require.NoError(t, DB.Create(newModelAliasTestChannel("新增二", "vendor/a", nil)).Error)
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ScanModelAliasPendingCounts(cancelledContext, nil)
	assert.ErrorIs(t, err, context.Canceled)
	configuration, err = GetModelAliasConfiguration()
	require.NoError(t, err)
	require.NotNil(t, configuration.Groups[0].PendingCount)
	assert.Equal(t, 4, *configuration.Groups[0].PendingCount)

	common.OptionMapRWMutex.RLock()
	rawCounts := common.OptionMap[ModelAliasPendingCountsOptionKey]
	common.OptionMapRWMutex.RUnlock()
	assert.NotContains(t, rawCounts, "channel")
}

func TestSaveModelAliasConfigurationInvalidatesOnlyChangedGroups(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias-a", Models: []string{"vendor/a"}},
		{Alias: "alias-b", Models: []string{"vendor/b"}},
	}, true, 30)
	require.NoError(t, err)
	require.NoError(t, DB.Create(newModelAliasTestChannel("渠道 A", "vendor/a", nil)).Error)
	require.NoError(t, DB.Create(newModelAliasTestChannel("渠道 B", "vendor/b", nil)).Error)
	_, err = ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)

	configuration, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias-a", Models: []string{"vendor/a-new"}},
		{Alias: "alias-b", Models: []string{"vendor/b"}},
	}, false, 45)
	require.NoError(t, err)
	assert.False(t, configuration.ScanEnabled)
	assert.Equal(t, 45, configuration.ScanIntervalMinutes)
	require.Nil(t, configuration.Groups[0].PendingCount)
	require.NotNil(t, configuration.Groups[1].PendingCount)
	assert.Equal(t, 1, *configuration.Groups[1].PendingCount)

	_, err = SaveModelAliasConfiguration(configuration.Groups, true, MinimumModelAliasScanIntervalMinutes-1)
	assert.ErrorContains(t, err, "不能小于")
	if strconv.IntSize == 64 {
		tooLarge := maxModelAliasScanIntervalMinutes + 1
		_, err = SaveModelAliasConfiguration(configuration.Groups, true, int(tooLarge))
		assert.ErrorContains(t, err, "间隔过大")
	}
}

func TestModelAliasScanRejectsStaleRevision(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/a"}},
	}, true, 30)
	require.NoError(t, err)
	revision := getModelAliasScanRevision()
	require.NoError(t, InvalidateModelAliasPendingCount("alias"))

	stored, err := saveModelAliasPendingCounts(revision, map[string]int{"alias": 99})
	require.NoError(t, err)
	assert.False(t, stored)
	configuration, err := GetModelAliasConfiguration()
	require.NoError(t, err)
	assert.Nil(t, configuration.Groups[0].PendingCount)
}

func TestModelAliasScanUsesDatabaseSnapshotWhenOptionMapIsStale(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/old"}},
	}, true, 30)
	require.NoError(t, err)

	remoteGroups := []ModelAliasGroup{{Alias: "alias", Models: []string{"vendor/new"}}}
	groupsData, err := common.Marshal(remoteGroups)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where(commonKeyCol+" = ?", ModelAliasGroupsOptionKey).
		Update("value", string(groupsData)).Error)
	require.NoError(t, DB.Model(&Option{}).Where(commonKeyCol+" = ?", modelAliasScanRevisionOptionKey).
		Update("value", "remote-revision").Error)
	require.NoError(t, DB.Create(newModelAliasTestChannel("新配置渠道", "vendor/new", nil)).Error)

	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.PendingCount)
	assert.True(t, summary.IsCurrent())

	counts := getModelAliasPendingCounts()
	assert.Equal(t, 1, counts["alias"])
}

func TestModelAliasScanTreatsEmptyRevisionAsCurrent(t *testing.T) {
	setupModelAliasGroupTest(t)
	_, err := SaveModelAliasConfiguration([]ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/model"}},
	}, true, 30)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where(commonKeyCol+" = ?", modelAliasScanRevisionOptionKey).
		Update("value", "").Error)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[modelAliasScanRevisionOptionKey] = ""
	common.OptionMapRWMutex.Unlock()

	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.True(t, summary.IsCurrent())
}

func TestModelAliasScanRevisionChangesOnEverySave(t *testing.T) {
	setupModelAliasGroupTest(t)
	groups := []ModelAliasGroup{{Alias: "alias", Models: []string{"vendor/a"}}}
	_, err := SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)

	summary, err := ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	assert.True(t, summary.IsCurrent())

	_, err = SaveModelAliasConfiguration(groups, false, 45)
	require.NoError(t, err)
	assert.False(t, summary.IsCurrent())
}

func newModelAliasTestChannel(name string, models string, mapping map[string]string) *Channel {
	channel := &Channel{Name: name, Models: models, Group: "default", Status: common.ChannelStatusEnabled, Key: "test-key"}
	if mapping == nil {
		return channel
	}
	data, _ := common.Marshal(mapping)
	text := string(data)
	channel.ModelMapping = &text
	return channel
}

func newModelAliasTestChannelWithRawMapping(name string, models string, mapping string) *Channel {
	channel := newModelAliasTestChannel(name, models, nil)
	channel.ModelMapping = &mapping
	return channel
}

func assertModelAliasChannel(t *testing.T, channelID int, expectedTarget string, expectAliasAbility bool) {
	t.Helper()
	var channel Channel
	require.NoError(t, DB.First(&channel, channelID).Error)
	assert.Contains(t, channel.GetModels(), "deepseek-v4-pro")
	require.NotNil(t, channel.ModelMapping)
	var mapping map[string]string
	require.NoError(t, common.UnmarshalJsonStr(*channel.ModelMapping, &mapping))
	assert.Equal(t, expectedTarget, mapping["deepseek-v4-pro"])

	var abilityCount int64
	require.NoError(t, DB.Model(&Ability{}).
		Where("channel_id = ? AND model = ?", channelID, "deepseek-v4-pro").
		Count(&abilityCount).Error)
	if expectAliasAbility {
		assert.EqualValues(t, 1, abilityCount)
	} else {
		assert.Zero(t, abilityCount)
	}
}
