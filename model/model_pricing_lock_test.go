package model

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/utils/tests"
)

func TestModelPricingLockQueriesQuoteKeyColumn(t *testing.T) {
	oldMainDatabaseType := common.MainDatabaseType()
	oldLogDatabaseType := common.LogDatabaseType()
	t.Cleanup(func() {
		common.SetDatabaseTypes(oldMainDatabaseType, oldLogDatabaseType)
		initCol()
	})

	for _, testCase := range []struct {
		name      string
		database  common.DatabaseType
		quotedKey string
	}{
		{name: "mysql", database: common.DatabaseTypeMySQL, quotedKey: "`key`"},
		{name: "sqlite", database: common.DatabaseTypeSQLite, quotedKey: "`key`"},
		{name: "postgresql", database: common.DatabaseTypePostgreSQL, quotedKey: `"key"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			common.SetDatabaseTypes(testCase.database, common.DatabaseTypeSQLite)
			initCol()
			var output bytes.Buffer
			dummyDB, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{
				DryRun: true,
				Logger: logger.New(log.New(&output, "", 0), logger.Config{LogLevel: logger.Info}),
			})
			require.NoError(t, err)
			_, err = getOptionForUpdate(dummyDB, ModelPricingLocksOptionKey)
			require.NoError(t, err)
			assert.Contains(t, output.String(), testCase.quotedKey+" =")
		})
	}
}

func setupModelPricingLockTest(t *testing.T) {
	t.Helper()
	oldDB := DB
	dsn := fmt.Sprintf("file:model-pricing-lock-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))
	DB = db

	common.OptionMapRWMutex.Lock()
	oldOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()

	defaults := map[string]string{
		"ModelRatio":                   `{}`,
		"CompletionRatio":              `{}`,
		"CacheRatio":                   `{}`,
		"CreateCacheRatio":             `{}`,
		"ImageRatio":                   `{}`,
		"AudioRatio":                   `{}`,
		"AudioCompletionRatio":         `{}`,
		"ModelPrice":                   `{}`,
		"billing_setting.billing_mode": `{}`,
		"billing_setting.billing_expr": `{}`,
		ModelPricingLocksOptionKey:     `{}`,
	}
	for key, value := range defaults {
		require.NoError(t, updateOptionMap(key, value))
	}

	t.Cleanup(func() {
		DB = oldDB
		common.OptionMapRWMutex.Lock()
		common.OptionMap = make(map[string]string, len(oldOptionMap))
		common.OptionMapRWMutex.Unlock()
		for key, value := range oldOptionMap {
			require.NoError(t, updateOptionMap(key, value))
		}
	})
}

func TestModelPricingLockPersistsAndUnlocks(t *testing.T) {
	setupModelPricingLockTest(t)

	lockedModels, err := SetModelPricingLock("gpt-test", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"gpt-test"}, lockedModels)

	locks, err := GetModelPricingLocks()
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"gpt-test": true}, locks)

	lockedModels, err = SetModelPricingLock("gpt-test", false)
	require.NoError(t, err)
	assert.Empty(t, lockedModels)

	locks, err = GetModelPricingLocks()
	require.NoError(t, err)
	assert.Empty(t, locks)
}

func TestApplyModelPricingSyncSkipsLockedModelAndSwitchesUnlockedCategory(t *testing.T) {
	setupModelPricingLockTest(t)

	initialValues := map[string]string{
		"ModelPrice":                   `{"locked-model":1,"unlocked-model":2}`,
		"ModelRatio":                   `{"locked-model":3}`,
		"CompletionRatio":              `{"locked-model":4}`,
		"CacheRatio":                   `{"locked-model":5}`,
		"CreateCacheRatio":             `{"locked-model":6}`,
		"ImageRatio":                   `{"locked-model":7}`,
		"AudioRatio":                   `{"locked-model":8}`,
		"AudioCompletionRatio":         `{"locked-model":9}`,
		"billing_setting.billing_mode": `{"locked-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"locked-model":"old_expr"}`,
	}
	for key, value := range initialValues {
		require.NoError(t, UpdateOption(key, value))
	}
	_, err := SetModelPricingLock("locked-model", true)
	require.NoError(t, err)

	result, err := ApplyModelPricingSync(map[string]map[string]any{
		"locked-model": {
			"model_price":            99.0,
			"model_ratio":            99.0,
			"completion_ratio":       99.0,
			"cache_ratio":            99.0,
			"create_cache_ratio":     99.0,
			"image_ratio":            99.0,
			"audio_ratio":            99.0,
			"audio_completion_ratio": 99.0,
			"billing_mode":           "tiered_expr",
			"billing_expr":           "new_expr",
		},
		"unlocked-model": {
			"model_ratio":      5.0,
			"completion_ratio": 6.0,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"unlocked-model"}, result.AppliedModels)
	assert.Equal(t, []string{"locked-model"}, result.IgnoredLockedModels)

	lockedValues := map[string]any{
		"ModelPrice":                   float64(1),
		"ModelRatio":                   float64(3),
		"CompletionRatio":              float64(4),
		"CacheRatio":                   float64(5),
		"CreateCacheRatio":             float64(6),
		"ImageRatio":                   float64(7),
		"AudioRatio":                   float64(8),
		"AudioCompletionRatio":         float64(9),
		"billing_setting.billing_mode": "tiered_expr",
		"billing_setting.billing_expr": "old_expr",
	}
	for key, expected := range lockedValues {
		var values map[string]any
		require.NoError(t, common.UnmarshalJsonStr(common.OptionMap[key], &values))
		assert.Equal(t, expected, values["locked-model"], key)
	}

	var price map[string]float64
	require.NoError(t, common.UnmarshalJsonStr(common.OptionMap["ModelPrice"], &price))
	assert.NotContains(t, price, "unlocked-model")
	var ratio map[string]float64
	require.NoError(t, common.UnmarshalJsonStr(common.OptionMap["ModelRatio"], &ratio))
	assert.Equal(t, 5.0, ratio["unlocked-model"])
}

func TestManualPricingUpdateDoesNotRemoveLock(t *testing.T) {
	setupModelPricingLockTest(t)

	_, err := SetModelPricingLock("locked-model", true)
	require.NoError(t, err)
	require.NoError(t, UpdateOption("ModelPrice", `{"locked-model":12}`))
	require.NoError(t, UpdateOption("ModelRatio", `{"locked-model":1}`))

	locks, err := GetModelPricingLocks()
	require.NoError(t, err)
	assert.True(t, locks["locked-model"])

	var price map[string]float64
	require.NoError(t, common.UnmarshalJsonStr(common.OptionMap["ModelPrice"], &price))
	assert.Equal(t, 12.0, price["locked-model"])
}

func TestConcurrentPricingMutationsKeepDatabaseAndRuntimeStateConsistent(t *testing.T) {
	setupModelPricingLockTest(t)

	const modelCount = 12
	var waitGroup sync.WaitGroup
	for index := 0; index < modelCount; index++ {
		modelName := fmt.Sprintf("concurrent-model-%d", index)
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			_, err := SetModelPricingLock(modelName, true)
			assert.NoError(t, err)
		}()
		go func() {
			defer waitGroup.Done()
			_, err := ApplyModelPricingSync(map[string]map[string]any{
				modelName: {"model_ratio": 1.0},
			})
			assert.NoError(t, err)
		}()
	}
	waitGroup.Wait()

	keys := append([]string{ModelPricingLocksOptionKey}, modelPricingSyncOptionKeys...)
	for _, key := range keys {
		var option Option
		require.NoError(t, DB.Where(commonKeyCol+" = ?", key).First(&option).Error)
		common.OptionMapRWMutex.RLock()
		runtimeValue := common.OptionMap[key]
		common.OptionMapRWMutex.RUnlock()
		assert.JSONEq(t, option.Value, runtimeValue, key)
	}
}
