package controller

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelAliasScanHandlerRunsAsScheduledSystemTask(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.SystemTask{}, &model.SystemTaskLock{}))

	setupModelAliasControllerOptions(t, map[string]string{"ModelRatio": `{"alias":1}`})

	_, err := model.SaveModelAliasConfiguration([]model.ModelAliasGroup{
		{Alias: "alias", Models: []string{"vendor/model"}},
	}, true, 45)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Channel{
		Name:   "测试渠道",
		Models: "vendor/model",
		Status: common.ChannelStatusManuallyDisabled,
		Group:  "default",
		Key:    "test-key",
	}).Error)

	handler := modelAliasScanHandler{}
	assert.Equal(t, model.SystemTaskTypeModelAliasScan, handler.Type())
	assert.True(t, handler.Enabled())
	assert.Equal(t, 45*time.Minute, handler.Interval())

	task, err := model.CreateSystemTask(model.SystemTaskTypeModelAliasScan, nil, nil)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "model-alias-test-runner", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, ok)
	handler.Run(context.Background(), claimed, "model-alias-test-runner")

	finished, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, finished)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	assert.Contains(t, finished.Result, `"pending_count":1`)
	assert.Contains(t, finished.Result, `"synced_groups":1`)
	assert.Contains(t, finished.Result, `"synced_models":1`)
	assert.NotContains(t, finished.Result, "channel_name")

	configuration, err := model.GetModelAliasConfiguration()
	require.NoError(t, err)
	require.NotNil(t, configuration.Groups[0].PendingCount)
	assert.Equal(t, 1, *configuration.Groups[0].PendingCount)

	_, err = model.SaveModelAliasConfiguration(configuration.Groups, false, 45)
	require.NoError(t, err)
	assert.False(t, handler.Enabled())
	_, err = model.SaveModelAliasConfiguration(nil, true, 45)
	require.NoError(t, err)
	assert.False(t, handler.Enabled())
}

func TestFinishModelAliasScanTaskEnqueuesNewRevision(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.SystemTask{}, &model.SystemTaskLock{}))

	setupModelAliasControllerOptions(t, map[string]string{"ModelRatio": `{"alias":1}`})

	groups := []model.ModelAliasGroup{{Alias: "alias", Models: []string{"vendor/model"}}}
	_, err := model.SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)
	task, err := model.CreateSystemTask(model.SystemTaskTypeModelAliasScan, nil, nil)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "model-alias-rerun-test", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, ok)

	summary, err := model.ScanModelAliasPendingCounts(context.Background(), nil)
	require.NoError(t, err)
	_, err = model.SaveModelAliasConfiguration(groups, true, 45)
	require.NoError(t, err)
	finishModelAliasScanTask(claimed, "model-alias-rerun-test", summary)

	finished, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	activeTask, err := model.GetActiveSystemTask(model.SystemTaskTypeModelAliasScan)
	require.NoError(t, err)
	require.NotNil(t, activeTask)
	assert.NotEqual(t, task.TaskID, activeTask.TaskID)
	assert.Equal(t, model.SystemTaskStatusPending, activeTask.Status)
}

func TestQueuedModelAliasScanStopsWhenScanningIsDisabled(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.SystemTask{}, &model.SystemTaskLock{}))
	setupModelAliasControllerOptions(t, map[string]string{"ModelPrice": `{"alias":1}`})
	groups := []model.ModelAliasGroup{{Alias: "alias", Models: []string{"vendor/model"}}}
	_, err := model.SaveModelAliasConfiguration(groups, true, 30)
	require.NoError(t, err)
	task, err := model.CreateSystemTask(model.SystemTaskTypeModelAliasScan, nil, nil)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "model-alias-disabled-test", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, model.UpdateOption("ModelPrice", `{"alias":2,"vendor/model":1}`))
	require.NoError(t, model.UpdateOption(model.ModelPricingLocksOptionKey, `{}`))
	_, err = model.SaveModelAliasConfiguration(groups, false, 30)
	require.NoError(t, err)

	modelAliasScanHandler{}.Run(context.Background(), claimed, "model-alias-disabled-test")

	finished, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	var priceMap map[string]float64
	require.NoError(t, common.UnmarshalJsonStr(common.OptionMap["ModelPrice"], &priceMap))
	assert.Equal(t, float64(1), priceMap["vendor/model"])
	locks, err := model.GetModelPricingLocks()
	require.NoError(t, err)
	assert.Empty(t, locks)
	activeTask, err := model.GetActiveSystemTask(model.SystemTaskTypeModelAliasScan)
	require.NoError(t, err)
	assert.Nil(t, activeTask)
}
