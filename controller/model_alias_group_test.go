package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateModelAliasGroupsRejectsConflictingNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("PUT", "/api/option/model-alias-groups", strings.NewReader(`{
		"groups":[
			{"alias":"alias-a","models":["vendor/model"]},
			{"alias":"alias-b","models":["vendor/model"]}
		]
	}`))

	UpdateModelAliasGroups(context)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Contains(t, response.Message, "已在别名组")
}

func TestUpdateModelAliasGroupsRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("PUT", "/api/option/model-alias-groups", strings.NewReader(`{"groups":`))

	UpdateModelAliasGroups(context)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "无效的模型别名组配置", response.Message)
}

func TestUpdateModelAliasGroupsRequiresGroupsField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("PUT", "/api/option/model-alias-groups", strings.NewReader(`{
		"scan_enabled":false,
		"scan_interval_minutes":30
	}`))

	UpdateModelAliasGroups(context)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "模型别名组配置不能为空", response.Message)
}

func TestModelAliasGroupEndpointRequiresRootUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("model-alias-test"))))
	engine.GET("/api/option/model-alias-groups", middleware.RootAuth(), GetModelAliasGroups)
	engine.GET("/api/option/model-alias-groups/channels", middleware.RootAuth(), ListModelAliasGroupChannels)
	engine.DELETE("/api/option/model-alias-groups/channels/:channel_id/models", middleware.RootAuth(), RemoveModelAliasChannelModel)

	testCases := []struct {
		method string
		path   string
	}{
		{method: "GET", path: "/api/option/model-alias-groups"},
		{method: "GET", path: "/api/option/model-alias-groups/channels?alias=alias"},
		{method: "DELETE", path: "/api/option/model-alias-groups/channels/1/models"},
	}
	for _, testCase := range testCases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(testCase.method, testCase.path, nil)
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, 401, recorder.Code, testCase.path)
	}
}

func TestRemoveModelAliasChannelModelRejectsInvalidRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	invalidIDRecorder := httptest.NewRecorder()
	invalidIDContext, _ := gin.CreateTestContext(invalidIDRecorder)
	invalidIDContext.Params = gin.Params{{Key: "channel_id", Value: "invalid"}}
	invalidIDContext.Request = httptest.NewRequest("DELETE", "/api/option/model-alias-groups/channels/invalid/models", nil)
	RemoveModelAliasChannelModel(invalidIDContext)

	var invalidIDResponse struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(invalidIDRecorder.Body.Bytes(), &invalidIDResponse))
	assert.False(t, invalidIDResponse.Success)
	assert.Equal(t, "渠道 ID 无效", invalidIDResponse.Message)

	invalidJSONRecorder := httptest.NewRecorder()
	invalidJSONContext, _ := gin.CreateTestContext(invalidJSONRecorder)
	invalidJSONContext.Params = gin.Params{{Key: "channel_id", Value: "1"}}
	invalidJSONContext.Request = httptest.NewRequest("DELETE", "/api/option/model-alias-groups/channels/1/models", strings.NewReader("{"))
	RemoveModelAliasChannelModel(invalidJSONContext)

	var invalidJSONResponse struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(invalidJSONRecorder.Body.Bytes(), &invalidJSONResponse))
	assert.False(t, invalidJSONResponse.Success)
	assert.Equal(t, "无效的渠道模型删除参数", invalidJSONResponse.Message)
}

func TestSearchModelAliasCatalogReturnsModelNames(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.Channel{
		Id:     1,
		Type:   1,
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
		Name:   "catalog-channel",
		Models: "provider/deepseek-v4-pro,unrelated-model",
		Group:  "default",
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "provider/deepseek-v4-pro", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "unrelated-model", ChannelId: 1, Enabled: true},
	}).Error)
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		"GET",
		"/api/option/model-alias-groups/catalog?model_name=DEEPSEEK-V4",
		nil,
	)

	SearchModelAliasCatalog(context)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Models []string `json:"models"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.Empty(t, response.Message)
	assert.Equal(t, []string{"provider/deepseek-v4-pro"}, response.Data.Models)
}

func TestSearchModelAliasCatalogRejectsEmptyKeyword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("GET", "/api/option/model-alias-groups/catalog", nil)

	SearchModelAliasCatalog(context)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Contains(t, response.Message, "统一名称不能为空")
}

func TestPreviewModelAliasGroupRejectsEmptyAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("POST", "/api/option/model-alias-groups/preview", strings.NewReader(`{"alias":"  "}`))

	PreviewModelAliasGroup(context)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "统一名称不能为空", response.Message)
}

func TestUpdateOptionRejectsModelAliasGroupsKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("PUT", "/api/option", strings.NewReader(`{"key":"ModelAliasGroups","value":"[]"}`))

	UpdateOption(context)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "该配置不允许通过通用设置接口修改", response.Message)
}

func TestUpdateModelAliasGroupsSavesScanSettingsAndRespectsScanSwitch(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.SystemTask{}, &model.SystemTaskLock{}))

	setupModelAliasControllerOptions(t, map[string]string{"ModelRatio": `{"alias":1}`})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("PUT", "/api/option/model-alias-groups", strings.NewReader(`{
		"groups":[{"alias":"alias","models":["vendor/model"]}],
		"scan_enabled":false,
		"scan_interval_minutes":45
	}`))

	UpdateModelAliasGroups(context)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Groups              []model.ModelAliasGroup `json:"groups"`
			ScanEnabled         bool                    `json:"scan_enabled"`
			ScanIntervalMinutes int                     `json:"scan_interval_minutes"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Groups, 1)
	assert.False(t, response.Data.ScanEnabled)
	assert.Equal(t, 45, response.Data.ScanIntervalMinutes)

	task, err := model.GetActiveSystemTask(model.SystemTaskTypeModelAliasScan)
	require.NoError(t, err)
	assert.Nil(t, task)

	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest("PUT", "/api/option/model-alias-groups", strings.NewReader(`{
		"groups":[{"alias":"alias","models":["vendor/model"]}],
		"scan_enabled":true,
		"scan_interval_minutes":45
	}`))
	UpdateModelAliasGroups(secondContext)
	var secondResponse struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(secondRecorder.Body.Bytes(), &secondResponse))
	require.True(t, secondResponse.Success)
	task, err = model.GetActiveSystemTask(model.SystemTaskTypeModelAliasScan)
	require.NoError(t, err)
	assert.Nil(t, task)

	thirdRecorder := httptest.NewRecorder()
	thirdContext, _ := gin.CreateTestContext(thirdRecorder)
	thirdContext.Request = httptest.NewRequest("PUT", "/api/option/model-alias-groups", strings.NewReader(`{
		"groups":[{"alias":"alias","models":["vendor/model","vendor/second"]}],
		"scan_enabled":true,
		"scan_interval_minutes":45
	}`))
	UpdateModelAliasGroups(thirdContext)
	var thirdResponse struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(thirdRecorder.Body.Bytes(), &thirdResponse))
	require.True(t, thirdResponse.Success)
	task, err = model.GetActiveSystemTask(model.SystemTaskTypeModelAliasScan)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, model.SystemTaskStatusPending, task.Status)

	var taskCount int64
	require.NoError(t, db.Model(&model.SystemTask{}).
		Where(&model.SystemTask{Type: model.SystemTaskTypeModelAliasScan}).
		Count(&taskCount).Error)
	assert.EqualValues(t, 1, taskCount)
}

func TestUpdateModelAliasGroupsAllowsDeletingLastGroupWithoutEnqueue(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.SystemTask{}, &model.SystemTaskLock{}))

	setupModelAliasControllerOptions(t, map[string]string{"ModelRatio": `{"alias":1}`})
	_, err := model.SaveModelAliasConfiguration([]model.ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/model"}},
	}, true, 30)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("PUT", "/api/option/model-alias-groups", strings.NewReader(`{
		"groups":[],
		"scan_enabled":true,
		"scan_interval_minutes":30
	}`))
	UpdateModelAliasGroups(context)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Groups []model.ModelAliasGroup `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.Empty(t, response.Data.Groups)

	task, err := model.GetActiveSystemTask(model.SystemTaskTypeModelAliasScan)
	require.NoError(t, err)
	assert.Nil(t, task)
}

func setupModelAliasControllerOptions(t *testing.T, pricingOverrides map[string]string) {
	t.Helper()
	pricingKeys := []string{
		"ModelRatio",
		"CompletionRatio",
		"CacheRatio",
		"CreateCacheRatio",
		"ImageRatio",
		"AudioRatio",
		"AudioCompletionRatio",
		"ModelPrice",
		"billing_setting.billing_mode",
		"billing_setting.billing_expr",
	}

	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	nextOptionMap := map[string]string{
		model.ModelAliasGroupsOptionKey:        "[]",
		model.ModelAliasScanEnabledOptionKey:   "true",
		model.ModelAliasScanIntervalOptionKey:  "30",
		model.ModelAliasPendingCountsOptionKey: "{}",
		model.ModelPricingLocksOptionKey:       "{}",
	}
	for _, key := range pricingKeys {
		nextOptionMap[key] = previousOptionMap[key]
		if nextOptionMap[key] == "" {
			nextOptionMap[key] = "{}"
		}
	}
	for key, value := range pricingOverrides {
		nextOptionMap[key] = value
	}
	common.OptionMap = nextOptionMap
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		for _, key := range pricingKeys {
			value := previousOptionMap[key]
			if value == "" {
				value = "{}"
			}
			require.NoError(t, model.UpdateOption(key, value), key)
		}
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})
}
