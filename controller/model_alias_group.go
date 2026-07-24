package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type updateModelAliasGroupsRequest struct {
	Groups              *[]model.ModelAliasGroup `json:"groups"`
	ScanEnabled         *bool                    `json:"scan_enabled"`
	ScanIntervalMinutes *int                     `json:"scan_interval_minutes"`
}

type modelAliasGroupRequest struct {
	Alias              string         `json:"alias"`
	SelectedChannelIDs []int          `json:"selected_channel_ids"`
	TargetModels       map[int]string `json:"target_models"`
}

func GetModelAliasGroups(c *gin.Context) {
	configuration, err := model.GetModelAliasConfiguration()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": configuration})
}

func SearchModelAliasCatalog(c *gin.Context) {
	models, err := model.SearchModelAliasCatalog(c.Query("model_name"))
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"models": models})
}

func UpdateModelAliasGroups(c *gin.Context) {
	var request updateModelAliasGroupsRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的模型别名组配置")
		return
	}
	if request.Groups == nil {
		common.ApiErrorMsg(c, "模型别名组配置不能为空")
		return
	}
	scanEnabled := model.IsModelAliasScanEnabled()
	if request.ScanEnabled != nil {
		scanEnabled = *request.ScanEnabled
	}
	scanIntervalMinutes := model.GetModelAliasScanIntervalMinutes()
	if request.ScanIntervalMinutes != nil {
		scanIntervalMinutes = *request.ScanIntervalMinutes
	}
	configuration, err := model.SaveModelAliasConfiguration(*request.Groups, scanEnabled, scanIntervalMinutes)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	recordManageAudit(c, "model_alias_group.update", map[string]interface{}{
		"group_count":           len(configuration.Groups),
		"scan_enabled":          configuration.ScanEnabled,
		"scan_interval_minutes": configuration.ScanIntervalMinutes,
	})
	requestModelAliasScan()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": configuration})
}

func PreviewModelAliasGroup(c *gin.Context) {
	request, ok := bindModelAliasGroupRequest(c)
	if !ok {
		return
	}
	preview, err := model.PreviewModelAliasGroup(request.Alias)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": preview})
}

func ApplyModelAliasGroup(c *gin.Context) {
	request, ok := bindModelAliasGroupRequest(c)
	if !ok {
		return
	}
	result, err := model.ApplyModelAliasGroupWithSelection(request.Alias, model.ModelAliasApplySelection{
		SelectedChannelIDs: request.SelectedChannelIDs,
		TargetModels:       request.TargetModels,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err = model.InvalidateModelAliasPendingCount(request.Alias); err != nil {
		common.SysLog("模型别名待处理数量失效失败: " + err.Error())
	}
	requestModelAliasScan()
	recordManageAudit(c, "model_alias_group.apply", map[string]interface{}{
		"alias":         request.Alias,
		"applied_count": result.Applied,
		"failed_count":  len(result.Failed),
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": result})
}

func requestModelAliasScan() {
	groups, err := model.GetModelAliasGroups()
	if err != nil {
		common.SysLog("读取模型别名组失败，无法请求扫描: " + err.Error())
		return
	}
	if len(groups) == 0 {
		return
	}
	if _, _, err = service.EnqueueSystemTask(model.SystemTaskTypeModelAliasScan, nil); err != nil {
		common.SysLog("请求模型别名扫描任务失败: " + err.Error())
	}
}

func bindModelAliasGroupRequest(c *gin.Context) (modelAliasGroupRequest, bool) {
	var request modelAliasGroupRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的模型别名组参数")
		return modelAliasGroupRequest{}, false
	}
	request.Alias = strings.TrimSpace(request.Alias)
	if request.Alias == "" {
		common.ApiErrorMsg(c, "统一名称不能为空")
		return modelAliasGroupRequest{}, false
	}
	return request, true
}
