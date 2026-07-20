package model

import (
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/samber/lo"
	"gorm.io/gorm"
)

const MaxChannelAggregationSources = 1000

const (
	ChannelAggregationReasonCodex        = "codex_not_supported"
	ChannelAggregationReasonVertexAPIKey = "vertex_api_key_not_supported"
	ChannelAggregationReasonInvalid      = "invalid_settings"
)

var (
	ErrChannelAggregationInvalidSources = errors.New("聚合渠道必须包含 2 到 1000 个不重复的来源渠道")
	ErrChannelAggregationDifferentGroup = errors.New("来源渠道的类型或 API 地址不一致")
	ErrChannelAggregationChanged        = errors.New("来源渠道已发生变化，请重新打开聚合表单")
	ErrChannelAggregationEmptyKey       = errors.New("来源渠道包含空密钥")
)

type ChannelAggregationPrepared struct {
	Channels        []*Channel
	Type            int
	BaseURL         string
	sourceSnapshots []channelAggregationSourceSnapshot
	// SourceKeys 保留来源密钥的完整快照，Keys 是去重后用于创建目标渠道的密钥。
	SourceKeys []string
	Keys       []string
	KeyText    string
}

type channelAggregationSourceSnapshot struct {
	ID   int      `json:"id"`
	Keys []string `json:"keys"`
}

type ChannelAggregationResult struct {
	ChannelID    int
	DeletedCount int
}

func NormalizeChannelAggregationBaseURL(baseURL *string) string {
	if baseURL == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(*baseURL), "/")
}

func GetChannelAggregationDisabledReason(channel *Channel) string {
	if channel == nil {
		return ChannelAggregationReasonCodex
	}
	if channel.Type == constant.ChannelTypeCodex {
		return ChannelAggregationReasonCodex
	}
	if channel.Type == constant.ChannelTypeVertexAi {
		settings := dto.ChannelOtherSettings{}
		if channel.OtherSettings != "" {
			if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil {
				return ChannelAggregationReasonInvalid
			}
		}
		if settings.VertexKeyType == dto.VertexKeyTypeAPIKey {
			return ChannelAggregationReasonVertexAPIKey
		}
	}
	return ""
}

func GetChannelsForAggregation(includeKeys bool) ([]*Channel, error) {
	query := DB.Order("id ASC")
	if !includeKeys {
		query = query.Omit("key")
	}
	var channels []*Channel
	if err := query.Find(&channels).Error; err != nil {
		return nil, err
	}
	return channels, nil
}

func ValidateChannelAggregationSourceIDs(sourceIDs []int) error {
	if len(sourceIDs) < 2 || len(sourceIDs) > MaxChannelAggregationSources {
		return ErrChannelAggregationInvalidSources
	}
	seen := make(map[int]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		if id <= 0 {
			return ErrChannelAggregationInvalidSources
		}
		if _, exists := seen[id]; exists {
			return ErrChannelAggregationInvalidSources
		}
		seen[id] = struct{}{}
	}
	return nil
}

func PrepareChannelAggregationByIDs(sourceIDs []int) (*ChannelAggregationPrepared, error) {
	if err := ValidateChannelAggregationSourceIDs(sourceIDs); err != nil {
		return nil, err
	}
	channels, err := getChannelsByAggregationSourceIDs(DB, sourceIDs, false)
	if err != nil {
		return nil, err
	}
	if len(channels) != len(sourceIDs) {
		return nil, ErrChannelAggregationChanged
	}
	return PrepareChannelAggregation(channels)
}

func PrepareChannelAggregation(channels []*Channel) (*ChannelAggregationPrepared, error) {
	if len(channels) < 2 || len(channels) > MaxChannelAggregationSources {
		return nil, ErrChannelAggregationInvalidSources
	}

	sortedChannels := append([]*Channel(nil), channels...)
	sort.Slice(sortedChannels, func(i, j int) bool {
		return sortedChannels[i].Id < sortedChannels[j].Id
	})

	channelType := sortedChannels[0].Type
	baseURL := NormalizeChannelAggregationBaseURL(sortedChannels[0].BaseURL)
	keys := make([]string, 0, len(sortedChannels))
	sourceSnapshots := make([]channelAggregationSourceSnapshot, 0, len(sortedChannels))
	for _, channel := range sortedChannels {
		if channel.Type != channelType || NormalizeChannelAggregationBaseURL(channel.BaseURL) != baseURL {
			return nil, ErrChannelAggregationDifferentGroup
		}
		if reason := GetChannelAggregationDisabledReason(channel); reason != "" {
			return nil, fmt.Errorf("渠道 %d 不支持聚合: %s", channel.Id, reason)
		}
		channelKeys, err := getChannelAggregationSourceKeys(channel)
		if err != nil {
			return nil, fmt.Errorf("读取渠道 %d 密钥失败: %w", channel.Id, err)
		}
		sourceSnapshots = append(sourceSnapshots, channelAggregationSourceSnapshot{
			ID:   channel.Id,
			Keys: append([]string(nil), channelKeys...),
		})
		keys = append(keys, channelKeys...)
	}
	if len(keys) == 0 {
		return nil, errors.New("来源渠道没有可聚合的密钥")
	}

	uniqueKeys := make([]string, 0, len(keys))
	seenKeys := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, exists := seenKeys[key]; exists {
			continue
		}
		seenKeys[key] = struct{}{}
		uniqueKeys = append(uniqueKeys, key)
	}
	keyText, err := SerializeChannelAggregationKeys(channelType, uniqueKeys)
	if err != nil {
		return nil, err
	}
	return &ChannelAggregationPrepared{
		Channels:        sortedChannels,
		Type:            channelType,
		BaseURL:         baseURL,
		sourceSnapshots: sourceSnapshots,
		SourceKeys:      keys,
		Keys:            uniqueKeys,
		KeyText:         keyText,
	}, nil
}

func (prepared *ChannelAggregationPrepared) SnapshotToken() (string, error) {
	if prepared == nil || len(prepared.sourceSnapshots) == 0 {
		return "", errors.New("聚合快照不能为空")
	}
	payload := struct {
		Type    int                                `json:"type"`
		BaseURL string                             `json:"base_url"`
		Sources []channelAggregationSourceSnapshot `json:"sources"`
	}{
		Type:    prepared.Type,
		BaseURL: prepared.BaseURL,
		Sources: prepared.sourceSnapshots,
	}
	data, err := common.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("生成聚合快照失败: %w", err)
	}
	return common.GenerateHMAC(string(data)), nil
}

func ParseChannelAggregationKeyText(channelType int, keyText string) ([]string, error) {
	trimmed := strings.TrimSpace(keyText)
	if trimmed == "" {
		return nil, errors.New("聚合密钥不能为空")
	}
	if channelType == constant.ChannelTypeVertexAi {
		var values []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &values); err != nil {
			return nil, fmt.Errorf("Vertex AI 聚合密钥必须是 JSON 数组: %w", err)
		}
		keys := make([]string, 0, len(values))
		for _, value := range values {
			key, err := normalizeVertexAggregationKey(string(value))
			if err != nil {
				return nil, err
			}
			keys = append(keys, key)
		}
		if len(keys) == 0 {
			return nil, errors.New("Vertex AI 聚合密钥不能为空")
		}
		return keys, nil
	}

	keys := make([]string, 0)
	for _, key := range strings.Split(keyText, "\n") {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("聚合密钥不能为空")
	}
	return keys, nil
}

func SerializeChannelAggregationKeys(channelType int, keys []string) (string, error) {
	if channelType != constant.ChannelTypeVertexAi {
		return strings.Join(keys, "\n"), nil
	}
	values := make([]json.RawMessage, 0, len(keys))
	for _, key := range keys {
		normalized, err := normalizeVertexAggregationKey(key)
		if err != nil {
			return "", err
		}
		values = append(values, json.RawMessage(normalized))
	}
	data, err := common.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("序列化 Vertex AI 聚合密钥失败: %w", err)
	}
	return string(data), nil
}

func AggregateChannels(sourceIDs []int, snapshotToken string, destination *Channel) (*ChannelAggregationResult, error) {
	if err := ValidateChannelAggregationSourceIDs(sourceIDs); err != nil {
		return nil, err
	}
	if snapshotToken == "" || destination == nil {
		return nil, errors.New("聚合参数不能为空")
	}

	result := &ChannelAggregationResult{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		channels, err := getChannelsByAggregationSourceIDs(tx, sourceIDs, true)
		if err != nil {
			return err
		}
		if len(channels) != len(sourceIDs) {
			return ErrChannelAggregationChanged
		}
		prepared, err := PrepareChannelAggregation(channels)
		if err != nil {
			return err
		}
		currentSnapshotToken, err := prepared.SnapshotToken()
		if err != nil {
			return err
		}
		if !hmac.Equal([]byte(currentSnapshotToken), []byte(snapshotToken)) {
			return ErrChannelAggregationChanged
		}

		destination.Id = 0
		destination.Type = prepared.Type
		if prepared.BaseURL == "" {
			destination.BaseURL = nil
		} else {
			baseURL := prepared.BaseURL
			destination.BaseURL = &baseURL
		}
		destination.Key = strings.Join(prepared.Keys, "\n")
		destination.ChannelInfo.IsMultiKey = true
		destination.ChannelInfo.MultiKeySize = len(prepared.Keys)
		destination.ChannelInfo.MultiKeyStatusList = nil
		destination.ChannelInfo.MultiKeyDisabledReason = nil
		destination.ChannelInfo.MultiKeyDisabledTime = nil
		destination.ChannelInfo.MultiKeyDisabledStatusCode = nil
		destination.ChannelInfo.MultiKeyDisabledGeneration = nil
		destination.ChannelInfo.MultiKeyPollingIndex = 0
		destination.ChannelInfo.MultiKeyTestIndex = 0
		destination.ChannelInfo.MultiKeyGenerationCounter = 0

		if err := tx.Create(destination).Error; err != nil {
			return err
		}
		if err := destination.AddAbilities(tx); err != nil {
			return err
		}
		for _, chunk := range lo.Chunk(sourceIDs, 200) {
			if err := tx.Where("channel_id IN ?", chunk).Delete(&Ability{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", chunk).Delete(&Channel{}).Error; err != nil {
				return err
			}
		}

		result.ChannelID = destination.Id
		result.DeletedCount = len(sourceIDs)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func getChannelsByAggregationSourceIDs(db *gorm.DB, sourceIDs []int, lock bool) ([]*Channel, error) {

	// 固定锁获取顺序，避免并发请求以不同来源顺序分块加锁而产生死锁。
	orderedSourceIDs := append([]int(nil), sourceIDs...)
	slices.Sort(orderedSourceIDs)
	channels := make([]*Channel, 0, len(sourceIDs))
	for _, chunk := range lo.Chunk(orderedSourceIDs, 200) {
		query := db.Where("id IN ?", chunk)
		if lock {
			query = lockForUpdate(query)
		}
		var batch []*Channel
		if err := query.Find(&batch).Error; err != nil {
			return nil, err
		}
		channels = append(channels, batch...)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].Id < channels[j].Id })
	return channels, nil
}

func getChannelAggregationSourceKeys(channel *Channel) ([]string, error) {
	rawKeys := []string{channel.Key}
	if channel.ChannelInfo.IsMultiKey {
		rawKeys = channel.GetKeys()
	}
	if len(rawKeys) == 0 {
		return nil, ErrChannelAggregationEmptyKey
	}
	keys := make([]string, 0, len(rawKeys))
	for _, key := range rawKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, ErrChannelAggregationEmptyKey
		}
		if channel.Type == constant.ChannelTypeVertexAi {
			normalized, err := normalizeVertexAggregationKey(key)
			if err != nil {
				return nil, err
			}
			key = normalized
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func normalizeVertexAggregationKey(key string) (string, error) {
	var value map[string]any
	if err := common.Unmarshal([]byte(strings.TrimSpace(key)), &value); err != nil {
		return "", fmt.Errorf("Vertex AI 密钥必须是 JSON 对象: %w", err)
	}
	data, err := common.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("序列化 Vertex AI 密钥失败: %w", err)
	}
	return string(data), nil
}
