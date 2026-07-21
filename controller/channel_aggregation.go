package controller

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type channelAggregationGroup struct {
	Key            string                        `json:"-"`
	Type           int                           `json:"type"`
	BaseURL        string                        `json:"base_url"`
	Eligible       bool                          `json:"eligible"`
	DisabledReason string                        `json:"disabled_reason,omitempty"`
	Channels       []channelAggregationCandidate `json:"channels"`
}

type channelAggregationCandidate struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Status         int    `json:"status"`
	IsMultiKey     bool   `json:"is_multi_key"`
	KeyCount       int    `json:"key_count"`
	Eligible       bool   `json:"eligible"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

// channelAggregationSourceConfig 只暴露聚合配置选择阶段需要的基础字段。
// 密钥、运行状态、余额和复杂 JSON 配置继续由聚合流程单独处理，避免把无关运行态带入新渠道。
type channelAggregationSourceConfig struct {
	ID                 int     `json:"id"`
	Name               string  `json:"name"`
	OpenAIOrganization *string `json:"openai_organization"`
	TestModel          *string `json:"test_model"`
	Weight             *uint   `json:"weight"`
	Group              string  `json:"group"`
	Priority           *int64  `json:"priority"`
	AutoBan            *int    `json:"auto_ban"`
	Tag                *string `json:"tag"`
	Remark             *string `json:"remark"`
	Other              string  `json:"other"`
	Proxy              string  `json:"proxy"`
	Models             string  `json:"models"`
}

type channelAggregationSourceRequest struct {
	SourceIDs []int `json:"source_ids"`
}

type channelAggregationRequest struct {
	SourceIDs     []int                 `json:"source_ids"`
	SnapshotToken string                `json:"snapshot_token"`
	MultiKeyMode  constant.MultiKeyMode `json:"multi_key_mode"`
	Channel       *model.Channel        `json:"channel"`
}

func GetChannelAggregationGroups(c *gin.Context) {
	channels, err := model.GetChannelsForAggregation(false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	groupsByKey := make(map[string]*channelAggregationGroup)
	for _, channel := range channels {
		baseURL := model.NormalizeChannelAggregationBaseURL(channel.BaseURL)
		key := fmt.Sprintf("%d\x00%s", channel.Type, baseURL)
		group, exists := groupsByKey[key]
		if !exists {
			group = &channelAggregationGroup{Key: key, Type: channel.Type, BaseURL: baseURL}
			groupsByKey[key] = group
		}

		reason := model.GetChannelAggregationDisabledReason(channel)
		keyCount := channel.ChannelInfo.MultiKeySize
		if keyCount < 1 {
			keyCount = 1
		}
		group.Channels = append(group.Channels, channelAggregationCandidate{
			ID:             channel.Id,
			Name:           channel.Name,
			Status:         channel.Status,
			IsMultiKey:     channel.ChannelInfo.IsMultiKey,
			KeyCount:       keyCount,
			Eligible:       reason == "",
			DisabledReason: reason,
		})
	}

	groups := make([]channelAggregationGroup, 0, len(groupsByKey))
	for _, group := range groupsByKey {
		sort.Slice(group.Channels, func(i, j int) bool { return group.Channels[i].ID < group.Channels[j].ID })
		eligibleCount := 0
		for _, channel := range group.Channels {
			if channel.Eligible {
				eligibleCount++
			}
		}
		group.Eligible = eligibleCount >= 2
		if !group.Eligible {
			group.DisabledReason = "not_enough_channels"
			if eligibleCount == 0 && len(group.Channels) > 0 {
				reason := group.Channels[0].DisabledReason
				allSameReason := reason != ""
				for _, channel := range group.Channels[1:] {
					if channel.DisabledReason != reason {
						allSameReason = false
						break
					}
				}
				if allSameReason {
					group.DisabledReason = reason
				}
			}
		}
		if len(group.Channels) < 2 {
			continue
		}
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Type != groups[j].Type {
			return groups[i].Type < groups[j].Type
		}
		return groups[i].BaseURL < groups[j].BaseURL
	})

	common.ApiSuccess(c, gin.H{"groups": groups})
}

func PrepareChannelAggregation(c *gin.Context) {
	request := channelAggregationSourceRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	prepared, err := model.PrepareChannelAggregationByIDs(request.SourceIDs)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	snapshotToken, err := prepared.SnapshotToken()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	sourceIDs := make([]int, 0, len(prepared.Channels))
	sources := make([]gin.H, 0, len(prepared.Channels))
	sourceConfigs := make([]channelAggregationSourceConfig, 0, len(prepared.Channels))
	for _, channel := range prepared.Channels {
		sourceIDs = append(sourceIDs, channel.Id)
		sources = append(sources, gin.H{"id": channel.Id, "name": channel.Name})

		config := dto.ChannelSettings{}
		if channel.Setting != nil && strings.TrimSpace(*channel.Setting) != "" {
			// 配置选择只读取代理字段，解析失败时按空代理处理，不能因为历史脏 JSON 阻断密钥聚合。
			_ = common.Unmarshal([]byte(*channel.Setting), &config)
		}
		sourceConfigs = append(sourceConfigs, channelAggregationSourceConfig{
			ID:                 channel.Id,
			Name:               channel.Name,
			OpenAIOrganization: channel.OpenAIOrganization,
			TestModel:          channel.TestModel,
			Weight:             channel.Weight,
			Group:              channel.Group,
			Priority:           channel.Priority,
			AutoBan:            channel.AutoBan,
			Tag:                channel.Tag,
			Remark:             channel.Remark,
			Other:              channel.Other,
			Proxy:              config.Proxy,
			Models:             channel.Models,
		})
	}
	recordManageAudit(c, "channel.aggregate_prepare", map[string]interface{}{
		"source_ids": sourceIDs,
		"type":       prepared.Type,
		"key_count":  len(prepared.Keys),
	})
	common.ApiSuccess(c, gin.H{
		"source_ids":     sourceIDs,
		"sources":        sources,
		"source_configs": sourceConfigs,
		"type":           prepared.Type,
		"base_url":       prepared.BaseURL,
		"key":            prepared.KeyText,
		"key_count":      len(prepared.Keys),
		"snapshot_token": snapshotToken,
	})
}

func AggregateChannels(c *gin.Context) {
	request := channelAggregationRequest{}
	if err := c.ShouldBindJSON(&request); err != nil || request.Channel == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "聚合参数错误"})
		return
	}
	if request.MultiKeyMode != constant.MultiKeyModeRandom && request.MultiKeyMode != constant.MultiKeyModePolling {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的多密钥策略"})
		return
	}

	prepared, err := model.PrepareChannelAggregationByIDs(request.SourceIDs)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	submittedKeys, err := model.ParseChannelAggregationKeyText(prepared.Type, request.Channel.Key)
	if err != nil || !slices.Equal(submittedKeys, prepared.Keys) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": model.ErrChannelAggregationChanged.Error()})
		return
	}
	if request.Channel.Type != prepared.Type || model.NormalizeChannelAggregationBaseURL(request.Channel.BaseURL) != prepared.BaseURL {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": model.ErrChannelAggregationDifferentGroup.Error()})
		return
	}

	destination := *request.Channel
	destination.Type = prepared.Type
	destination.Key = prepared.KeyText
	destination.ChannelInfo = model.ChannelInfo{
		IsMultiKey:   true,
		MultiKeySize: len(prepared.Keys),
		MultiKeyMode: request.MultiKeyMode,
	}
	if prepared.BaseURL == "" {
		destination.BaseURL = nil
	} else {
		baseURL := prepared.BaseURL
		destination.BaseURL = &baseURL
	}
	destination.CreatedTime = common.GetTimestamp()
	destination.Status = common.ChannelStatusEnabled
	destination.TestTime = 0
	destination.ResponseTime = 0
	destination.Balance = 0
	destination.BalanceUpdatedTime = 0
	destination.UsedQuota = 0
	destination.OtherInfo = ""
	if err := validateChannel(&destination, true); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	result, err := model.AggregateChannels(request.SourceIDs, request.SnapshotToken, &destination)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	model.InitChannelCache()
	service.ResetProxyClientCache()
	recordManageAudit(c, "channel.aggregate", map[string]interface{}{
		"source_ids":    request.SourceIDs,
		"target_id":     result.ChannelID,
		"type":          prepared.Type,
		"key_count":     len(prepared.Keys),
		"deleted_count": result.DeletedCount,
	})
	common.ApiSuccess(c, gin.H{"channel_id": result.ChannelID, "deleted_count": result.DeletedCount})
}
