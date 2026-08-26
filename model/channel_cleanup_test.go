package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelCleanupTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	dsn := fmt.Sprintf("file:channel-cleanup-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = oldDB
		common.SetDatabaseTypes(oldMainType, oldLogType)
	})
}

// insertCleanupChannel 写入渠道并同步创建 ability，模拟真实渠道数据。
func insertCleanupChannel(t *testing.T, channel *Channel) {
	t.Helper()
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))
}

func TestDeleteLongAutoDisabledSingleKeyChannels(t *testing.T) {
	now := common.GetTimestamp()
	thirtyOneDaysAgo := now - 31*24*3600
	exactlyThirtyDaysAgo := now - 30*24*3600
	tenDaysAgo := now - 10*24*3600

	setupChannelCleanupTestDB(t)

	// 应被删除：单密钥 + 自动禁用 + 超期 31 天
	expired := &Channel{
		Id: 1, Type: 1, Key: "expired-key", Status: common.ChannelStatusAutoDisabled,
		Name: "expired", Models: "gpt-4o", Group: "default",
		OtherInfo:   fmt.Sprintf(`{"status_reason":"auto disabled","status_time":%d}`, thirtyOneDaysAgo),
		ChannelInfo: ChannelInfo{IsMultiKey: false},
	}
	insertCleanupChannel(t, expired)

	// 应保留：恰好 30 天，未"超过"阈值
	boundary := &Channel{
		Id: 7, Type: 1, Key: "boundary-key", Status: common.ChannelStatusAutoDisabled,
		Name: "boundary", Models: "gpt-4o", Group: "default",
		OtherInfo:   fmt.Sprintf(`{"status_time":%d}`, exactlyThirtyDaysAgo),
		ChannelInfo: ChannelInfo{IsMultiKey: false},
	}
	insertCleanupChannel(t, boundary)

	// 应保留：未到期
	notExpired := &Channel{
		Id: 2, Type: 1, Key: "recent-key", Status: common.ChannelStatusAutoDisabled,
		Name: "recent", Models: "gpt-4o", Group: "default",
		OtherInfo:   fmt.Sprintf(`{"status_time":%d}`, tenDaysAgo),
		ChannelInfo: ChannelInfo{IsMultiKey: false},
	}
	insertCleanupChannel(t, notExpired)

	// 应保留：手动禁用
	manual := &Channel{
		Id: 3, Type: 1, Key: "manual-key", Status: common.ChannelStatusManuallyDisabled,
		Name: "manual", Models: "gpt-4o", Group: "default",
		OtherInfo:   fmt.Sprintf(`{"status_time":%d}`, thirtyOneDaysAgo),
		ChannelInfo: ChannelInfo{IsMultiKey: false},
	}
	insertCleanupChannel(t, manual)

	// 应保留：多密钥渠道即使顶层自动禁用超期
	multiKey := &Channel{
		Id: 4, Type: 1, Key: "k1\nk2", Status: common.ChannelStatusAutoDisabled,
		Name: "multi-key", Models: "gpt-4o", Group: "default",
		OtherInfo: fmt.Sprintf(`{"status_time":%d}`, thirtyOneDaysAgo),
		ChannelInfo: ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       2,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusAutoDisabled},
		},
	}
	insertCleanupChannel(t, multiKey)

	// 应保留：旧数据缺少 status_time
	noTimestamp := &Channel{
		Id: 5, Type: 1, Key: "legacy-key", Status: common.ChannelStatusAutoDisabled,
		Name: "legacy", Models: "gpt-4o", Group: "default",
		OtherInfo:   `{"status_reason":"auto disabled"}`,
		ChannelInfo: ChannelInfo{IsMultiKey: false},
	}
	insertCleanupChannel(t, noTimestamp)

	// 应保留：other_info 为空字符串（GetOtherInfo 返回空 map）
	emptyOtherInfo := &Channel{
		Id: 8, Type: 1, Key: "empty-info-key", Status: common.ChannelStatusAutoDisabled,
		Name: "empty-info", Models: "gpt-4o", Group: "default",
		ChannelInfo: ChannelInfo{IsMultiKey: false},
	}
	insertCleanupChannel(t, emptyOtherInfo)

	// 应保留：other_info 非法 JSON（GetOtherInfo 解析失败返回空 map，无法判定时长）
	invalidOtherInfo := &Channel{
		Id: 9, Type: 1, Key: "invalid-info-key", Status: common.ChannelStatusAutoDisabled,
		Name: "invalid-info", Models: "gpt-4o", Group: "default",
		OtherInfo:   `{"status_time":`,
		ChannelInfo: ChannelInfo{IsMultiKey: false},
	}
	insertCleanupChannel(t, invalidOtherInfo)

	// 应保留：已恢复（status=1 且无 status_time）
	recovered := &Channel{
		Id: 6, Type: 1, Key: "recovered-key", Status: common.ChannelStatusEnabled,
		Name: "recovered", Models: "gpt-4o", Group: "default",
		ChannelInfo: ChannelInfo{IsMultiKey: false},
	}
	insertCleanupChannel(t, recovered)

	deleted, err := DeleteLongAutoDisabledSingleKeyChannels(30 * 24 * time.Hour)
	require.NoError(t, err)
	require.Len(t, deleted, 1)
	assert.Equal(t, 1, deleted[0].Id)

	// 渠道表与 abilities 表均无残留
	var count int64
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 1).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", 1).Count(&count).Error)
	assert.Zero(t, count)

	// 其余渠道全部保留，且 other_info 未被重写
	for _, id := range []int{2, 3, 4, 5, 6, 7, 8, 9} {
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", id).Count(&count).Error)
		assert.Equal(t, int64(1), count, "channel %d should remain", id)
	}
	var kept Channel
	require.NoError(t, DB.Where("id = ?", 5).First(&kept).Error)
	assert.JSONEq(t, `{"status_reason":"auto disabled"}`, kept.OtherInfo)
}

func TestDeleteLongAutoDisabledSingleKeyChannelsStringStatusTime(t *testing.T) {
	// 历史数据兼容：status_time 以字符串形式存储
	thirtyOneDaysAgo := common.GetTimestamp() - 31*24*3600

	setupChannelCleanupTestDB(t)
	insertCleanupChannel(t, &Channel{
		Id: 1, Type: 1, Key: "string-time-key", Status: common.ChannelStatusAutoDisabled,
		Name: "string-time", Models: "gpt-4o", Group: "default",
		OtherInfo:   fmt.Sprintf(`{"status_time":"%d"}`, thirtyOneDaysAgo),
		ChannelInfo: ChannelInfo{IsMultiKey: false},
	})

	deleted, err := DeleteLongAutoDisabledSingleKeyChannels(30 * 24 * time.Hour)
	require.NoError(t, err)
	require.Len(t, deleted, 1)
	assert.Equal(t, 1, deleted[0].Id)
}
