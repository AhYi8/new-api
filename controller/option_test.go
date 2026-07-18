package controller

import (
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateOptionRejectsInvalidMultiKeyTestLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testCases := []string{
		`{"key":"monitor_setting.multi_key_auto_disabled_test_limit","value":-1}`,
		`{"key":"monitor_setting.multi_key_auto_disabled_test_limit","value":1.5}`,
	}

	for _, body := range testCases {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest("PUT", "/api/option", strings.NewReader(body))

		UpdateOption(context)

		var response struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.False(t, response.Success)
		assert.Equal(t, "自动禁用密钥测试批量数必须是非负整数", response.Message)
	}
}

func TestUpdateOptionAcceptsNonNegativeMultiKeyTestLimit(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})
	originalLimit := operation_setting.GetMonitorSetting().MultiKeyAutoDisabledTestLimit
	t.Cleanup(func() {
		require.NoError(t, model.UpdateOption(
			"monitor_setting.multi_key_auto_disabled_test_limit",
			strconv.Itoa(originalLimit),
		))
	})

	for _, value := range []int{0, 3} {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		body := fmt.Sprintf(`{"key":"monitor_setting.multi_key_auto_disabled_test_limit","value":%d}`, value)
		context.Request = httptest.NewRequest("PUT", "/api/option", strings.NewReader(body))

		UpdateOption(context)

		var response struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.True(t, response.Success)
		assert.Equal(t, value, operation_setting.GetMonitorSetting().MultiKeyAutoDisabledTestLimit)

		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", "monitor_setting.multi_key_auto_disabled_test_limit").Error)
		assert.Equal(t, strconv.Itoa(value), option.Value)
	}
}

func TestUpdateOptionRejectsInvalidMultiKeyTestSkipStatusCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, value := range []string{"99", "600", "500-400", "401 403", "invalid"} {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		body := fmt.Sprintf(
			`{"key":"monitor_setting.multi_key_auto_disabled_test_skip_status_codes","value":%q}`,
			value,
		)
		context.Request = httptest.NewRequest("PUT", "/api/option", strings.NewReader(body))

		UpdateOption(context)

		var response struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.False(t, response.Success, value)
	}
}

func TestUpdateOptionAcceptsMultiKeyTestSkipStatusCodes(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})
	originalValue := operation_setting.GetMonitorSetting().MultiKeyAutoDisabledTestSkipStatusCodes
	t.Cleanup(func() {
		require.NoError(t, model.UpdateOption(
			"monitor_setting.multi_key_auto_disabled_test_skip_status_codes",
			originalValue,
		))
	})

	for _, value := range []string{"", "401", "401,403,500-599"} {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		body := fmt.Sprintf(
			`{"key":"monitor_setting.multi_key_auto_disabled_test_skip_status_codes","value":%q}`,
			value,
		)
		context.Request = httptest.NewRequest("PUT", "/api/option", strings.NewReader(body))

		UpdateOption(context)

		var response struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.True(t, response.Success, value)
		assert.Equal(t, value, operation_setting.GetMonitorSetting().MultiKeyAutoDisabledTestSkipStatusCodes)

		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", "monitor_setting.multi_key_auto_disabled_test_skip_status_codes").Error)
		assert.Equal(t, value, option.Value)
	}
}
