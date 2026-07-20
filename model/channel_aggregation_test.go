package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelAggregationTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	dsn := fmt.Sprintf("file:channel-aggregation-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = oldDB
		common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	})
}

func insertAggregationSource(t *testing.T, channel *Channel) {
	t.Helper()
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))
}

func TestPrepareChannelAggregationPreservesOrderAndDeduplicatesKeys(t *testing.T) {
	baseURLWithSlash := " https://api.example.com/ "
	baseURL := "https://api.example.com"
	channels := []*Channel{
		{
			Id:      9,
			Type:    1,
			BaseURL: &baseURLWithSlash,
			Key:     "key-c\nkey-a",
			ChannelInfo: ChannelInfo{
				IsMultiKey:         true,
				MultiKeySize:       2,
				MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
			},
		},
		{Id: 3, Type: 1, BaseURL: &baseURL, Key: "key-a"},
	}

	prepared, err := PrepareChannelAggregation(channels)
	require.NoError(t, err)
	assert.Equal(t, []int{3, 9}, []int{prepared.Channels[0].Id, prepared.Channels[1].Id})
	assert.Equal(t, []string{"key-a", "key-c", "key-a"}, prepared.SourceKeys)
	assert.Equal(t, []string{"key-a", "key-c"}, prepared.Keys)
	assert.Equal(t, "key-a\nkey-c", prepared.KeyText)
	assert.Equal(t, baseURL, prepared.BaseURL)
}

func TestPrepareChannelAggregationRejectsInvalidSources(t *testing.T) {
	assert.ErrorIs(t, ValidateChannelAggregationSourceIDs([]int{1}), ErrChannelAggregationInvalidSources)
	assert.ErrorIs(t, ValidateChannelAggregationSourceIDs([]int{1, 1}), ErrChannelAggregationInvalidSources)
	assert.ErrorIs(t, ValidateChannelAggregationSourceIDs([]int{0, 1}), ErrChannelAggregationInvalidSources)

	firstBaseURL := "https://one.example.com"
	secondBaseURL := "https://two.example.com"
	_, err := PrepareChannelAggregation([]*Channel{
		{Id: 1, Type: 1, BaseURL: &firstBaseURL, Key: "key-a"},
		{Id: 2, Type: 1, BaseURL: &secondBaseURL, Key: "key-b"},
	})
	assert.ErrorIs(t, err, ErrChannelAggregationDifferentGroup)

	_, err = PrepareChannelAggregation([]*Channel{
		{Id: 1, Type: 1, Key: "key-a"},
		{Id: 2, Type: 1, Key: "  "},
	})
	assert.ErrorIs(t, err, ErrChannelAggregationEmptyKey)
}

func TestPrepareChannelAggregationSerializesVertexKeys(t *testing.T) {
	channels := []*Channel{
		{Id: 1, Type: constant.ChannelTypeVertexAi, Key: "{\n\"client_email\": \"one@example.com\"\n}"},
		{
			Id:   2,
			Type: constant.ChannelTypeVertexAi,
			Key:  "{\"client_email\":\"two@example.com\"}\n{\"client_email\":\"one@example.com\"}",
			ChannelInfo: ChannelInfo{
				IsMultiKey:   true,
				MultiKeySize: 2,
			},
		},
	}

	prepared, err := PrepareChannelAggregation(channels)
	require.NoError(t, err)
	assert.JSONEq(t,
		`[{"client_email":"one@example.com"},{"client_email":"two@example.com"}]`,
		prepared.KeyText,
	)
	parsed, err := ParseChannelAggregationKeyText(constant.ChannelTypeVertexAi, prepared.KeyText)
	require.NoError(t, err)
	assert.Equal(t, prepared.Keys, parsed)
}

func TestChannelAggregationEligibility(t *testing.T) {
	assert.Equal(t, ChannelAggregationReasonCodex, GetChannelAggregationDisabledReason(&Channel{Type: constant.ChannelTypeCodex}))

	vertexAPIKey := &Channel{Type: constant.ChannelTypeVertexAi}
	vertexAPIKey.SetOtherSettings(dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeAPIKey})
	assert.Equal(t, ChannelAggregationReasonVertexAPIKey, GetChannelAggregationDisabledReason(vertexAPIKey))
	assert.Equal(t, ChannelAggregationReasonInvalid, GetChannelAggregationDisabledReason(&Channel{
		Type: constant.ChannelTypeVertexAi, OtherSettings: "{",
	}))
	assert.Empty(t, GetChannelAggregationDisabledReason(&Channel{Type: 1}))
}

func TestAggregateChannelsCreatesDestinationAndDeletesSources(t *testing.T) {
	setupChannelAggregationTestDB(t)
	baseURLWithSlash := "https://api.example.com/"
	baseURL := "https://api.example.com"
	first := &Channel{
		Type:    1,
		Name:    "first",
		Key:     "key-a",
		BaseURL: &baseURLWithSlash,
		Models:  "gpt-4o",
		Group:   "default",
		Status:  common.ChannelStatusEnabled,
	}
	second := &Channel{
		Type:    1,
		Name:    "second",
		Key:     "key-b\nkey-a",
		BaseURL: &baseURL,
		Models:  "gpt-4o",
		Group:   "default",
		Status:  common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:                 true,
			MultiKeySize:               2,
			MultiKeyStatusList:         map[int]int{0: common.ChannelStatusManuallyDisabled},
			MultiKeyDisabledReason:     map[int]string{0: "manual"},
			MultiKeyDisabledTime:       map[int]int64{0: 123},
			MultiKeyDisabledStatusCode: map[int]int{0: 401},
		},
	}
	insertAggregationSource(t, first)
	insertAggregationSource(t, second)

	prepared, err := PrepareChannelAggregationByIDs([]int{second.Id, first.Id})
	require.NoError(t, err)
	snapshotToken, err := prepared.SnapshotToken()
	require.NoError(t, err)
	destination := &Channel{
		Name:   "aggregated",
		Models: "gpt-4o",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			MultiKeyMode: constant.MultiKeyModePolling,
		},
	}
	result, err := AggregateChannels([]int{second.Id, first.Id}, snapshotToken, destination)
	require.NoError(t, err)
	assert.Equal(t, 2, result.DeletedCount)

	var sourceCount int64
	require.NoError(t, DB.Model(&Channel{}).Where("id IN ?", []int{first.Id, second.Id}).Count(&sourceCount).Error)
	assert.Zero(t, sourceCount)

	var stored Channel
	require.NoError(t, DB.First(&stored, result.ChannelID).Error)
	assert.Equal(t, "key-a\nkey-b", stored.Key)
	assert.True(t, stored.ChannelInfo.IsMultiKey)
	assert.Equal(t, 2, stored.ChannelInfo.MultiKeySize)
	assert.Equal(t, constant.MultiKeyModePolling, stored.ChannelInfo.MultiKeyMode)
	assert.Nil(t, stored.ChannelInfo.MultiKeyStatusList)
	assert.Nil(t, stored.ChannelInfo.MultiKeyDisabledReason)

	var sourceAbilityCount int64
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id IN ?", []int{first.Id, second.Id}).Count(&sourceAbilityCount).Error)
	assert.Zero(t, sourceAbilityCount)
	var targetAbilityCount int64
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", stored.Id).Count(&targetAbilityCount).Error)
	assert.Equal(t, int64(1), targetAbilityCount)
}

func TestAggregateChannelsRejectsRemovedDuplicateSourceKey(t *testing.T) {
	setupChannelAggregationTestDB(t)
	first := &Channel{Type: 1, Name: "first", Key: "key-a", Models: "gpt-4o", Group: "default", Status: 1}
	second := &Channel{
		Type: 1, Name: "second", Key: "key-b\nkey-b", Models: "gpt-4o", Group: "default", Status: 1,
		ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 2},
	}
	insertAggregationSource(t, first)
	insertAggregationSource(t, second)
	prepared, err := PrepareChannelAggregationByIDs([]int{first.Id, second.Id})
	require.NoError(t, err)
	snapshotToken, err := prepared.SnapshotToken()
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", second.Id).Update("key", "key-b").Error)

	_, err = AggregateChannels([]int{first.Id, second.Id}, snapshotToken, &Channel{
		Name: "aggregated", Models: "gpt-4o", Group: "default", Status: 1,
	})
	assert.ErrorIs(t, err, ErrChannelAggregationChanged)

	var channelCount int64
	require.NoError(t, DB.Model(&Channel{}).Count(&channelCount).Error)
	assert.Equal(t, int64(2), channelCount)
}

func TestAggregateChannelsRejectsSourceKeyRedistribution(t *testing.T) {
	setupChannelAggregationTestDB(t)
	first := &Channel{
		Type: 1, Name: "first", Key: "key-a\nkey-b", Models: "gpt-4o", Group: "default", Status: 1,
		ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 2},
	}
	second := &Channel{
		Type: 1, Name: "second", Key: "key-c", Models: "gpt-4o", Group: "default", Status: 1,
		ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 1},
	}
	insertAggregationSource(t, first)
	insertAggregationSource(t, second)
	prepared, err := PrepareChannelAggregationByIDs([]int{first.Id, second.Id})
	require.NoError(t, err)
	snapshotToken, err := prepared.SnapshotToken()
	require.NoError(t, err)

	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", first.Id).Updates(map[string]interface{}{
		"key": "key-a",
	}).Error)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", second.Id).Updates(map[string]interface{}{
		"key": "key-b\nkey-c",
	}).Error)

	_, err = AggregateChannels([]int{first.Id, second.Id}, snapshotToken, &Channel{
		Name: "aggregated", Models: "gpt-4o", Group: "default", Status: 1,
	})
	assert.ErrorIs(t, err, ErrChannelAggregationChanged)
}

func TestAggregateChannelsRejectsChangedSourcesWithoutWriting(t *testing.T) {
	setupChannelAggregationTestDB(t)
	first := &Channel{Type: 1, Name: "first", Key: "key-a", Models: "gpt-4o", Group: "default", Status: 1}
	second := &Channel{Type: 1, Name: "second", Key: "key-b", Models: "gpt-4o", Group: "default", Status: 1}
	insertAggregationSource(t, first)
	insertAggregationSource(t, second)
	prepared, err := PrepareChannelAggregationByIDs([]int{first.Id, second.Id})
	require.NoError(t, err)
	snapshotToken, err := prepared.SnapshotToken()
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", second.Id).Update("key", "changed-key").Error)

	_, err = AggregateChannels([]int{first.Id, second.Id}, snapshotToken, &Channel{
		Name: "aggregated", Models: "gpt-4o", Group: "default", Status: 1,
	})
	assert.True(t, errors.Is(err, ErrChannelAggregationChanged))

	var channelCount int64
	require.NoError(t, DB.Model(&Channel{}).Count(&channelCount).Error)
	assert.Equal(t, int64(2), channelCount)
}

func TestAggregateChannelsRollsBackWhenAbilityCreationFails(t *testing.T) {
	setupChannelAggregationTestDB(t)
	first := &Channel{Type: 1, Name: "first", Key: "key-a", Models: "gpt-4o", Group: "default", Status: 1}
	second := &Channel{Type: 1, Name: "second", Key: "key-b", Models: "gpt-4o", Group: "default", Status: 1}
	insertAggregationSource(t, first)
	insertAggregationSource(t, second)
	prepared, err := PrepareChannelAggregationByIDs([]int{first.Id, second.Id})
	require.NoError(t, err)
	snapshotToken, err := prepared.SnapshotToken()
	require.NoError(t, err)
	require.NoError(t, DB.Exec(`CREATE TRIGGER fail_aggregate_ability BEFORE INSERT ON abilities BEGIN SELECT RAISE(FAIL, 'forced ability failure'); END`).Error)

	_, err = AggregateChannels([]int{first.Id, second.Id}, snapshotToken, &Channel{
		Name: "aggregated", Models: "gpt-4o", Group: "default", Status: 1,
	})
	require.Error(t, err)

	var channels []Channel
	require.NoError(t, DB.Order("id ASC").Find(&channels).Error)
	require.Len(t, channels, 2)
	assert.Equal(t, []int{first.Id, second.Id}, []int{channels[0].Id, channels[1].Id})
}
