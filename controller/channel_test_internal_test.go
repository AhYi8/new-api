package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettleTestQuotaUsesTieredBilling(t *testing.T) {
	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:   "tiered_expr",
			ExprString:    `param("stream") == true ? tier("stream", p * 3) : tier("base", p * 2)`,
			ExprHash:      billingexpr.ExprHashString(`param("stream") == true ? tier("stream", p * 3) : tier("base", p * 2)`),
			GroupRatio:    1,
			EstimatedTier: "stream",
			QuotaPerUnit:  common.QuotaPerUnit,
			ExprVersion:   1,
		},
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{"stream":true}`),
		},
	}

	quota, result := settleTestQuota(info, types.PriceData{
		ModelRatio:      1,
		CompletionRatio: 2,
	}, &dto.Usage{
		PromptTokens: 1000,
	})

	require.Equal(t, 1500, quota)
	require.NotNil(t, result)
	require.Equal(t, "stream", result.MatchedTier)
}

func TestBuildTestLogOtherInjectsTieredInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr",
			ExprString:  `tier("base", p * 2)`,
		},
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	priceData := types.PriceData{
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	usage := &dto.Usage{
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 12,
		},
	}

	other := buildTestLogOther(ctx, info, priceData, usage, &billingexpr.TieredResult{
		MatchedTier: "base",
	})

	require.Equal(t, "tiered_expr", other["billing_mode"])
	require.Equal(t, "base", other["matched_tier"])
	require.NotEmpty(t, other["expr_b64"])
}

func TestResolveChannelTestUserIDUsesRequestUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("id", 2)

	userID, err := resolveChannelTestUserID(ctx)

	require.NoError(t, err)
	require.Equal(t, 2, userID)
}

func TestSelectChannelsForAutomaticTestPassiveRecoveryOnlyUsesAutoDisabled(t *testing.T) {
	channels := []*model.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled},
		{Id: 2, Status: common.ChannelStatusAutoDisabled},
		{Id: 3, Status: common.ChannelStatusManuallyDisabled},
	}

	selected := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModePassiveRecovery)

	require.Len(t, selected, 1)
	require.Equal(t, 2, selected[0].Id)
}

func TestSelectChannelsForAutomaticTestScheduledSkipsManualDisabled(t *testing.T) {
	channels := []*model.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled},
		{Id: 2, Status: common.ChannelStatusAutoDisabled},
		{Id: 3, Status: common.ChannelStatusManuallyDisabled},
	}

	selected := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModeScheduledAll)

	require.Len(t, selected, 2)
	require.Equal(t, 1, selected[0].Id)
	require.Equal(t, 2, selected[1].Id)
}

func TestCollectAutoDisabledMultiKeyCandidates(t *testing.T) {
	channel := &model.Channel{
		Status: common.ChannelStatusEnabled,
		Key:    "enabled-key\nmanual-key\nauto-key\nanother-auto-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
			MultiKeyStatusList: map[int]int{
				1: common.ChannelStatusManuallyDisabled,
				2: common.ChannelStatusAutoDisabled,
				3: common.ChannelStatusAutoDisabled,
			},
		},
	}

	candidates := collectAutoDisabledMultiKeyCandidates(channel, 0, nil)
	require.Len(t, candidates, 2)
	require.Equal(t, 2, candidates[0].index)
	require.Equal(t, "auto-key", candidates[0].key)
	require.Equal(t, 3, candidates[1].index)
	require.Equal(t, "another-auto-key", candidates[1].key)

	channel.ChannelInfo.MultiKeyTestIndex = 3
	candidates = collectAutoDisabledMultiKeyCandidates(channel, 1, nil)
	require.Len(t, candidates, 1)
	require.Equal(t, 3, candidates[0].index)

	channel.ChannelInfo.MultiKeyTestIndex = 4
	candidates = collectAutoDisabledMultiKeyCandidates(channel, 1, nil)
	require.Len(t, candidates, 1)
	require.Equal(t, 2, candidates[0].index)

	channel.Status = common.ChannelStatusManuallyDisabled
	require.Empty(t, collectAutoDisabledMultiKeyCandidates(channel, 0, nil))
}

func TestCollectAutoDisabledMultiKeyCandidatesSkipsConfiguredStatusCodes(t *testing.T) {
	skipRanges, err := operation_setting.ParseHTTPStatusCodeRanges("401,403")
	require.NoError(t, err)
	channel := &model.Channel{
		Status: common.ChannelStatusEnabled,
		Key:    "structured-401\nstatus-500\nhistorical-403\nunknown-status",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
				1: common.ChannelStatusAutoDisabled,
				2: common.ChannelStatusAutoDisabled,
				3: common.ChannelStatusAutoDisabled,
			},
			MultiKeyDisabledStatusCode: map[int]int{0: 401, 1: 500},
			MultiKeyDisabledReason: map[int]string{
				2: "status_code=403, invalid key",
				3: "network error",
			},
		},
	}

	candidates := collectAutoDisabledMultiKeyCandidates(channel, 0, skipRanges)
	require.Len(t, candidates, 2)
	assert.Equal(t, 1, candidates[0].index)
	assert.Equal(t, 3, candidates[1].index)

	candidates = collectAutoDisabledMultiKeyCandidates(channel, 1, skipRanges)
	require.Len(t, candidates, 1)
	assert.Equal(t, 1, candidates[0].index)

	candidates = collectAutoDisabledMultiKeyCandidates(channel, 0, nil)
	require.Len(t, candidates, 4)
}

func TestPerformAutoDisabledMultiKeyTestsDoesNotTestOrCountSkippedKeys(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	originalRequestInterval := common.RequestInterval
	common.AutomaticEnableChannelEnabled = false
	common.RequestInterval = 0
	t.Cleanup(func() {
		common.AutomaticEnableChannelEnabled = originalAutomaticEnable
		common.RequestInterval = originalRequestInterval
	})

	skipRanges, err := operation_setting.ParseHTTPStatusCodeRanges("401")
	require.NoError(t, err)
	channel := &model.Channel{
		Name:   "skip-status-channel",
		Status: common.ChannelStatusEnabled,
		Key:    "ignored-key\ntested-key\nremaining-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
				1: common.ChannelStatusAutoDisabled,
				2: common.ChannelStatusAutoDisabled,
			},
			MultiKeyDisabledStatusCode: map[int]int{0: 401, 1: 500, 2: 503},
		},
	}
	require.NoError(t, db.Create(channel).Error)

	testedIndexes := make([]int, 0, 1)
	summary, _ := performAutoDisabledMultiKeyTestsWithTester(
		context.Background(), []*model.Channel{channel}, 1, 1, skipRanges, nil,
		func(_ context.Context, _ *model.Channel, _ int, keyIndex int) testResult {
			testedIndexes = append(testedIndexes, keyIndex)
			return testResult{localErr: errors.New("test failed")}
		},
	)

	assert.Equal(t, []int{1}, testedIndexes)
	assert.Equal(t, 1, summary.KeyTested)
	assert.Equal(t, 1, summary.KeyFailed)
	assert.Zero(t, summary.KeySucceeded)
}

func TestPerformAutoDisabledMultiKeyTestsDoesNotRecoverNewDisableEvent(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	originalRequestInterval := common.RequestInterval
	common.AutomaticEnableChannelEnabled = true
	common.RequestInterval = 0
	t.Cleanup(func() {
		common.AutomaticEnableChannelEnabled = originalAutomaticEnable
		common.RequestInterval = originalRequestInterval
	})

	skipRanges, err := operation_setting.ParseHTTPStatusCodeRanges("401")
	require.NoError(t, err)
	channel := &model.Channel{
		Name:   "disable-generation-channel",
		Status: common.ChannelStatusAutoDisabled,
		Key:    "key-1",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:                 true,
			MultiKeySize:               1,
			MultiKeyStatusList:         map[int]int{0: common.ChannelStatusAutoDisabled},
			MultiKeyDisabledReason:     map[int]string{0: "status_code=500, first failure"},
			MultiKeyDisabledTime:       map[int]int64{0: 10},
			MultiKeyDisabledStatusCode: map[int]int{0: 500},
			MultiKeyDisabledGeneration: map[int]int64{0: 1},
		},
	}
	require.NoError(t, db.Create(channel).Error)

	summary, cacheChanged := performAutoDisabledMultiKeyTestsWithTester(
		context.Background(), []*model.Channel{channel}, 1, 0, skipRanges, nil,
		func(_ context.Context, current *model.Channel, _ int, _ int) testResult {
			// 模拟同一秒内发生状态码、原因、时间都完全相同的新一轮自动禁用。
			current.ChannelInfo.MultiKeyDisabledGeneration[0]++
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", current.Id).
				Update("channel_info", current.ChannelInfo).Error)
			return testResult{}
		},
	)

	assert.Equal(t, 1, summary.KeyTested)
	assert.Equal(t, 1, summary.KeySucceeded)
	assert.Zero(t, summary.KeyRecovered)
	assert.False(t, cacheChanged)

	updated, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, updated.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, 500, updated.ChannelInfo.MultiKeyDisabledStatusCode[0])
	assert.Equal(t, int64(2), updated.ChannelInfo.MultiKeyDisabledGeneration[0])
	assert.Equal(t, "status_code=500, first failure", updated.ChannelInfo.MultiKeyDisabledReason[0])
}

func TestPerformAutoDisabledMultiKeyTestsRecoversOnlySuccessfulKeys(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	originalRequestInterval := common.RequestInterval
	common.AutomaticEnableChannelEnabled = true
	common.RequestInterval = 0
	t.Cleanup(func() {
		common.AutomaticEnableChannelEnabled = originalAutomaticEnable
		common.RequestInterval = originalRequestInterval
	})

	channel := &model.Channel{
		Name:   "enabled-channel",
		Status: common.ChannelStatusEnabled,
		Key:    "enabled-key\nsuccess-key\nfailed-key\nmanual-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
			MultiKeyStatusList: map[int]int{
				1: common.ChannelStatusAutoDisabled,
				2: common.ChannelStatusAutoDisabled,
				3: common.ChannelStatusManuallyDisabled,
			},
		},
	}
	require.NoError(t, db.Create(channel).Error)

	testedIndexes := make([]int, 0, 2)
	reported := make([]int, 0, 2)
	summary, cacheChanged := performAutoDisabledMultiKeyTestsWithTester(context.Background(), []*model.Channel{channel}, 1, 0, nil,
		func(processed int) { reported = append(reported, processed) },
		func(_ context.Context, _ *model.Channel, _ int, keyIndex int) testResult {
			testedIndexes = append(testedIndexes, keyIndex)
			if keyIndex == 1 {
				return testResult{}
			}
			return testResult{localErr: errors.New("test failed")}
		})

	require.Equal(t, []int{1, 2}, testedIndexes)
	require.Equal(t, []int{1, 2}, reported)
	require.Equal(t, 2, summary.KeyTested)
	require.Equal(t, 1, summary.KeySucceeded)
	require.Equal(t, 1, summary.KeyFailed)
	require.Equal(t, 1, summary.KeyRecovered)
	require.Zero(t, summary.Enabled)
	require.True(t, cacheChanged)

	updated, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.NotContains(t, updated.ChannelInfo.MultiKeyStatusList, 1)
	require.Equal(t, common.ChannelStatusAutoDisabled, updated.ChannelInfo.MultiKeyStatusList[2])
	require.Equal(t, common.ChannelStatusManuallyDisabled, updated.ChannelInfo.MultiKeyStatusList[3])
	require.Zero(t, updated.ChannelInfo.MultiKeyTestIndex)
}

func TestPerformAutoDisabledMultiKeyTestsRespectsAutomaticEnableSwitch(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	originalRequestInterval := common.RequestInterval
	common.AutomaticEnableChannelEnabled = false
	common.RequestInterval = 0
	t.Cleanup(func() {
		common.AutomaticEnableChannelEnabled = originalAutomaticEnable
		common.RequestInterval = originalRequestInterval
	})

	channel := &model.Channel{
		Name:   "switch-controlled-channel",
		Status: common.ChannelStatusEnabled,
		Key:    "auto-disabled-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:         true,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled},
		},
	}
	require.NoError(t, db.Create(channel).Error)

	summary, cacheChanged := performAutoDisabledMultiKeyTestsWithTester(context.Background(), []*model.Channel{channel}, 1, 0, nil, nil,
		func(_ context.Context, _ *model.Channel, _ int, _ int) testResult { return testResult{} })

	require.Equal(t, 1, summary.KeyTested)
	require.Equal(t, 1, summary.KeySucceeded)
	require.Zero(t, summary.KeyRecovered)
	require.False(t, cacheChanged)

	updated, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, common.ChannelStatusAutoDisabled, updated.ChannelInfo.MultiKeyStatusList[0])
}

func TestPerformAutoDisabledMultiKeyTestsAppliesLimitAndAdvancesCursor(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	originalRequestInterval := common.RequestInterval
	common.AutomaticEnableChannelEnabled = false
	common.RequestInterval = 0
	t.Cleanup(func() {
		common.AutomaticEnableChannelEnabled = originalAutomaticEnable
		common.RequestInterval = originalRequestInterval
	})

	channel := &model.Channel{
		Name:   "limited-channel",
		Status: common.ChannelStatusEnabled,
		Key:    "first-key\nsecond-key\nthird-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
				1: common.ChannelStatusAutoDisabled,
				2: common.ChannelStatusAutoDisabled,
			},
		},
	}
	require.NoError(t, db.Create(channel).Error)

	testedIndexes := make([]int, 0, 2)
	summary, cacheChanged := performAutoDisabledMultiKeyTestsWithTester(context.Background(), []*model.Channel{channel}, 1, 2, nil, nil,
		func(_ context.Context, _ *model.Channel, _ int, keyIndex int) testResult {
			testedIndexes = append(testedIndexes, keyIndex)
			return testResult{localErr: errors.New("test failed")}
		})

	require.Equal(t, []int{0, 1}, testedIndexes)
	require.Equal(t, 2, summary.KeyTested)
	require.False(t, cacheChanged)

	updated, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, 2, updated.ChannelInfo.MultiKeyTestIndex)

	nextCandidates := collectAutoDisabledMultiKeyCandidates(updated, 2, nil)
	require.Len(t, nextCandidates, 2)
	require.Equal(t, 2, nextCandidates[0].index)
	require.Equal(t, 0, nextCandidates[1].index)
}

func TestPerformAutoDisabledMultiKeyTestsStopsAfterCancellationAndWritesCompletedResult(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	originalRequestInterval := common.RequestInterval
	common.AutomaticEnableChannelEnabled = true
	common.RequestInterval = 0
	t.Cleanup(func() {
		common.AutomaticEnableChannelEnabled = originalAutomaticEnable
		common.RequestInterval = originalRequestInterval
	})

	channel := &model.Channel{
		Name:   "cancelled-channel",
		Status: common.ChannelStatusEnabled,
		Key:    "first-key\nsecond-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
				1: common.ChannelStatusAutoDisabled,
			},
		},
	}
	require.NoError(t, db.Create(channel).Error)

	ctx, cancel := context.WithCancel(context.Background())
	testedIndexes := make([]int, 0, 1)
	summary, _ := performAutoDisabledMultiKeyTestsWithTester(ctx, []*model.Channel{channel}, 1, 0, nil, nil,
		func(_ context.Context, _ *model.Channel, _ int, keyIndex int) testResult {
			testedIndexes = append(testedIndexes, keyIndex)
			cancel()
			return testResult{}
		})

	require.Equal(t, []int{0}, testedIndexes)
	require.Equal(t, 1, summary.KeyTested)
	require.Equal(t, 1, summary.KeyRecovered)

	updated, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.NotContains(t, updated.ChannelInfo.MultiKeyStatusList, 0)
	require.Equal(t, common.ChannelStatusAutoDisabled, updated.ChannelInfo.MultiKeyStatusList[1])
}

func TestPerformAutoDisabledMultiKeyTestsSkipsCandidateAfterManualDisable(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	originalRequestInterval := common.RequestInterval
	common.AutomaticEnableChannelEnabled = true
	common.RequestInterval = 0
	t.Cleanup(func() {
		common.AutomaticEnableChannelEnabled = originalAutomaticEnable
		common.RequestInterval = originalRequestInterval
	})

	channel := &model.Channel{
		Name:   "manually-disabled-during-test",
		Status: common.ChannelStatusEnabled,
		Key:    "first-key\nsecond-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
				1: common.ChannelStatusAutoDisabled,
			},
		},
	}
	require.NoError(t, db.Create(channel).Error)

	testedIndexes := make([]int, 0, 1)
	summary, cacheChanged := performAutoDisabledMultiKeyTestsWithTester(context.Background(), []*model.Channel{channel}, 1, 0, nil, nil,
		func(_ context.Context, _ *model.Channel, _ int, keyIndex int) testResult {
			testedIndexes = append(testedIndexes, keyIndex)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).
				Update("status", common.ChannelStatusManuallyDisabled).Error)
			return testResult{}
		})

	require.Equal(t, []int{0}, testedIndexes)
	require.Equal(t, 1, summary.KeyTested)
	require.Equal(t, 1, summary.KeySucceeded)
	require.Zero(t, summary.KeyRecovered)
	require.False(t, cacheChanged)
}

func TestTestAllChannelsRejectsExistingActiveTask(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))

	existing, err := model.CreateSystemTask(model.SystemTaskTypeChannelTest, nil, nil)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/test", nil)

	TestAllChannels(ctx)

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), existing.TaskID)
	require.Contains(t, recorder.Body.String(), "已有通道测试任务正在运行或等待中")
}
