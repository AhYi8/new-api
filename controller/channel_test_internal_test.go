package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
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

func TestShouldAutoDisableTestedChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalAutomaticDisable := common.AutomaticDisableChannelEnabled
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = originalAutomaticDisable
	})
	common.AutomaticDisableChannelEnabled = true

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	autoBan := 1
	channel := &model.Channel{
		Status:  common.ChannelStatusEnabled,
		AutoBan: &autoBan,
	}
	disablingError := types.NewError(errors.New("invalid key"), types.ErrorCodeChannelInvalidKey)

	require.True(t, shouldAutoDisableTestedChannel(channel, ctx, disablingError))

	channel.Status = common.ChannelStatusManuallyDisabled
	require.False(t, shouldAutoDisableTestedChannel(channel, ctx, disablingError))
	channel.Status = common.ChannelStatusEnabled

	autoBan = 0
	require.False(t, shouldAutoDisableTestedChannel(channel, ctx, disablingError))
	autoBan = 1

	require.False(t, shouldAutoDisableTestedChannel(channel, nil, disablingError))
	require.False(t, shouldAutoDisableTestedChannel(channel, ctx, nil))

	common.AutomaticDisableChannelEnabled = false
	require.False(t, shouldAutoDisableTestedChannel(channel, ctx, disablingError))
}

func TestNewChannelErrorDoesNotDefaultMissingMultiKeyIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "first-key")

	channel := &model.Channel{
		Id:   1,
		Key:  "first-key\nsecond-key",
		Name: "missing-index-channel",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	channelError := newChannelError(channel, ctx)

	assert.True(t, channelError.IsMultiKey)
	assert.Equal(t, "first-key", channelError.UsingKey)
	assert.Nil(t, channelError.UsingKeyIndex)
	assert.Nil(t, channelError.ChannelStateGeneration)
}

func TestChannelAutoDisablesExactMultiKeySynchronously(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticDisable := common.AutomaticDisableChannelEnabled
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalErrorLogEnabled := constant.ErrorLogEnabled
	originalDisableRanges := operation_setting.AutomaticDisableStatusCodeRanges
	common.AutomaticDisableChannelEnabled = true
	common.MemoryCacheEnabled = false
	constant.ErrorLogEnabled = false
	operation_setting.AutomaticDisableStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: http.StatusUnauthorized, End: http.StatusUnauthorized}}
	service.InitHttpClient()
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = originalAutomaticDisable
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		constant.ErrorLogEnabled = originalErrorLogEnabled
		operation_setting.AutomaticDisableStatusCodeRanges = originalDisableRanges
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key","type":"invalid_request_error","code":"invalid_api_key"}}`))
	}))
	t.Cleanup(upstream.Close)

	userSetting, err := common.Marshal(dto.UserSetting{AcceptUnsetRatioModel: true})
	require.NoError(t, err)
	user := model.User{
		Username: "channel-test-root",
		Password: "channel-test-password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
		Setting:  string(userSetting),
	}
	require.NoError(t, db.Create(&user).Error)

	testModel := "gpt-4o-mini"
	channel := model.Channel{
		Name:      "manual-test-duplicate-key",
		Type:      constant.ChannelTypeOpenAI,
		Key:       "same-key\nsame-key",
		Status:    common.ChannelStatusEnabled,
		BaseURL:   common.GetPointer(upstream.URL),
		Models:    testModel,
		TestModel: &testModel,
		AutoBan:   common.GetPointer(1),
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       2,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
		},
	}
	require.NoError(t, db.Create(&channel).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/test/"+strconv.Itoa(channel.Id)+"?model="+testModel, nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channel.Id)}}
	ctx.Set("id", user.Id)

	TestChannel(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success, recorder.Body.String())

	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[1], response.Message)
	assert.Equal(t, http.StatusUnauthorized, stored.ChannelInfo.MultiKeyDisabledStatusCode[1], response.Message)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status, response.Message)
}

func TestResolveChannelTestErrorUsesResponseTimeout(t *testing.T) {
	originalAutomaticDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = originalAutomaticDisable })

	timeoutError := resolveChannelTestError(testResult{}, 2000, 1000)
	require.NotNil(t, timeoutError)
	assert.Equal(t, types.ErrorCodeChannelResponseTimeExceeded, timeoutError.GetErrorCode())
	assert.Equal(t, http.StatusRequestTimeout, timeoutError.StatusCode)

	assert.Nil(t, resolveChannelTestError(testResult{}, 1000, 1000))

	originalError := types.NewError(errors.New("invalid key"), types.ErrorCodeChannelInvalidKey)
	assert.Same(t, originalError, resolveChannelTestError(testResult{newAPIError: originalError}, 2000, 1000))
}

func TestPerformChannelTestsRespectsAllowDisable(t *testing.T) {
	tests := []struct {
		name          string
		allowDisable  bool
		expectedState int
		expectedCount int
	}{
		{
			name:          "允许禁用时同步写入",
			allowDisable:  true,
			expectedState: common.ChannelStatusAutoDisabled,
			expectedCount: 1,
		},
		{
			name:          "被动恢复模式不禁用",
			allowDisable:  false,
			expectedState: common.ChannelStatusEnabled,
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupModelListControllerTestDB(t)
			originalAutomaticDisable := common.AutomaticDisableChannelEnabled
			originalMemoryCacheEnabled := common.MemoryCacheEnabled
			originalRequestInterval := common.RequestInterval
			originalErrorLogEnabled := constant.ErrorLogEnabled
			common.AutomaticDisableChannelEnabled = true
			common.MemoryCacheEnabled = false
			common.RequestInterval = 0
			constant.ErrorLogEnabled = false
			t.Cleanup(func() {
				common.AutomaticDisableChannelEnabled = originalAutomaticDisable
				common.MemoryCacheEnabled = originalMemoryCacheEnabled
				common.RequestInterval = originalRequestInterval
				constant.ErrorLogEnabled = originalErrorLogEnabled
			})

			channel := &model.Channel{
				Name:    "batch-test-disable",
				Key:     "test-key",
				Status:  common.ChannelStatusEnabled,
				AutoBan: common.GetPointer(1),
			}
			require.NoError(t, db.Create(channel).Error)

			testContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			testContext.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			common.SetContextKey(testContext, constant.ContextKeyChannelKey, channel.Key)
			common.SetContextKey(testContext, constant.ContextKeyChannelIsMultiKey, false)
			common.SetContextKey(testContext, constant.ContextKeyChannelStateGeneration, int64(0))
			channelError := types.NewError(errors.New("invalid key"), types.ErrorCodeChannelInvalidKey)

			summary := performChannelTestsWithTester(
				context.Background(),
				[]*model.Channel{channel},
				1,
				tt.allowDisable,
				nil,
				func(context.Context, *model.Channel, int, string, string, bool) testResult {
					return testResult{context: testContext, localErr: channelError, newAPIError: channelError}
				},
			)

			assert.Equal(t, tt.expectedCount, summary.Disabled)
			stored, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedState, stored.Status)
		})
	}
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

func TestReconcileAutoDisabledMultiKeyChannelsUsesLatestDatabaseState(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	common.AutomaticEnableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticEnableChannelEnabled = originalAutomaticEnable })

	channel := &model.Channel{
		Name:   "stale-channel-snapshot",
		Status: common.ChannelStatusAutoDisabled,
		Key:    "key-1",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:         true,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled},
		},
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: channel.Id, Enabled: false}).Error)

	latestInfo := channel.ChannelInfo
	latestInfo.MultiKeyStatusList = nil
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).
		Update("channel_info", latestInfo).Error)

	enabled := reconcileAutoDisabledMultiKeyChannels(context.Background(), []*model.Channel{channel})

	require.Equal(t, 1, enabled)
	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	var ability model.Ability
	require.NoError(t, db.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)
}

func TestReconcileAutoDisabledMultiKeyChannelsCountsRepairsAndRespectsSwitch(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	t.Cleanup(func() { common.AutomaticEnableChannelEnabled = originalAutomaticEnable })

	channels := []*model.Channel{
		{Name: "first", Status: common.ChannelStatusAutoDisabled, Key: "key-1", ChannelInfo: model.ChannelInfo{IsMultiKey: true}},
		{Name: "second", Status: common.ChannelStatusAutoDisabled, Key: "key-2\nkey-3", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled}}},
		{Name: "all-disabled", Status: common.ChannelStatusAutoDisabled, Key: "key-4", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled}}},
		{Name: "manual", Status: common.ChannelStatusManuallyDisabled, Key: "key-5", ChannelInfo: model.ChannelInfo{IsMultiKey: true}},
	}
	for _, channel := range channels {
		require.NoError(t, db.Create(channel).Error)
	}

	common.AutomaticEnableChannelEnabled = false
	assert.Zero(t, reconcileAutoDisabledMultiKeyChannels(context.Background(), channels))
	for _, channel := range channels {
		stored, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		assert.Equal(t, channel.Status, stored.Status)
	}

	common.AutomaticEnableChannelEnabled = true
	assert.Equal(t, 2, reconcileAutoDisabledMultiKeyChannels(context.Background(), channels))
	assert.Zero(t, reconcileAutoDisabledMultiKeyChannels(context.Background(), channels))

	for index, channel := range channels {
		stored, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		if index < 2 {
			assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
			continue
		}
		assert.Equal(t, channel.Status, stored.Status)
	}
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
