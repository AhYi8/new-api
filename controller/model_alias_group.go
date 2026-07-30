package controller

import (
	"net/http"
	"strconv"
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

type removeModelAliasChannelModelRequest struct {
	Alias        string `json:"alias"`
	ModelName    string `json:"model_name"`
	Revision     string `json:"revision"`
	AllowCascade bool   `json:"allow_cascade"`
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

func ListModelAliasGroupChannels(c *gin.Context) {
	result, err := model.ListModelAliasGroupChannels(c.Query("alias"))
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, result)
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
	configuration, hasChangedGroups, err := model.SaveModelAliasConfigurationWithChanges(*request.Groups, scanEnabled, scanIntervalMinutes)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	recordManageAudit(c, "model_alias_group.update", map[string]interface{}{
		"group_count":           len(configuration.Groups),
		"scan_enabled":          configuration.ScanEnabled,
		"scan_interval_minutes": configuration.ScanIntervalMinutes,
	})
	if hasChangedGroups {
		requestModelAliasScan()
	}
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

func RemoveModelAliasChannelModel(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("channel_id"))
	if err != nil || channelID <= 0 {
		common.ApiErrorMsg(c, "渠道 ID 无效")
		return
	}
	var request removeModelAliasChannelModelRequest
	if err = common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的渠道模型删除参数")
		return
	}
	result, err := model.RemoveModelAliasChannelModel(
		request.Alias,
		channelID,
		request.ModelName,
		request.Revision,
		request.AllowCascade,
	)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	for _, affectedAlias := range result.AffectedAliases {
		if invalidateErr := model.InvalidateModelAliasPendingCount(affectedAlias); invalidateErr != nil {
			common.SysLog("模型别名待处理数量失效失败: " + invalidateErr.Error())
		}
	}
	requestModelAliasScan()
	recordManageAudit(c, "model_alias_group.channel_model.remove", map[string]interface{}{
		"alias":                strings.TrimSpace(request.Alias),
		"channel_id":           result.ChannelID,
		"channel_name":         result.ChannelName,
		"requested_model":      result.RequestedModel,
		"removed_models":       result.RemovedModels,
		"removed_mapping_keys": result.RemovedMappingKeys,
	})
	common.ApiSuccess(c, result)
}

func requestModelAliasScan() {
	if !model.IsModelAliasScanEnabled() {
		return
	}
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
