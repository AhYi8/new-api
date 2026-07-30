package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	ModelAliasGroupsOptionKey            = "ModelAliasGroups"
	ModelAliasScanEnabledOptionKey       = "ModelAliasScanEnabled"
	ModelAliasScanIntervalOptionKey      = "ModelAliasScanIntervalMinutes"
	ModelAliasPendingCountsOptionKey     = "ModelAliasPendingCounts"
	modelAliasScanRevisionOptionKey      = "ModelAliasScanRevision"
	DefaultModelAliasScanIntervalMinutes = 30
	MinimumModelAliasScanIntervalMinutes = 10
	// time.Duration 以纳秒存储，限制分钟数避免转换时溢出为负数。
	maxModelAliasScanIntervalMinutes int64 = (1<<63 - 1) / int64(time.Minute)

	maxModelAliasGroups      = 100
	maxModelAliasGroupModels = 100
	maxModelAliasNameLength  = 255
)

var modelAliasOptionsMu sync.Mutex

type ModelAliasGroup struct {
	Alias        string   `json:"alias"`
	Models       []string `json:"models"`
	PendingCount *int     `json:"pending_count,omitempty" gorm:"-"`
}

type ModelAliasConfiguration struct {
	Groups              []ModelAliasGroup `json:"groups"`
	ScanEnabled         bool              `json:"scan_enabled"`
	ScanIntervalMinutes int               `json:"scan_interval_minutes"`
}

type ModelAliasPreviewStatus string

const (
	ModelAliasPreviewStatusNew             ModelAliasPreviewStatus = "new"
	ModelAliasPreviewStatusUnchanged       ModelAliasPreviewStatus = "unchanged"
	ModelAliasPreviewStatusUpdated         ModelAliasPreviewStatus = "updated"
	ModelAliasPreviewStatusConflict        ModelAliasPreviewStatus = "conflict"
	ModelAliasPreviewStatusMultipleMatches ModelAliasPreviewStatus = "multiple_matches"
	ModelAliasPreviewStatusUnmatched       ModelAliasPreviewStatus = "unmatched"
)

type ModelAliasChannelPreview struct {
	ChannelID      int                     `json:"channel_id"`
	ChannelName    string                  `json:"channel_name"`
	ChannelStatus  int                     `json:"channel_status"`
	Status         ModelAliasPreviewStatus `json:"status"`
	Reason         string                  `json:"reason,omitempty"`
	MatchedModels  []string                `json:"matched_models"`
	CurrentTarget  string                  `json:"current_target,omitempty"`
	ProposedTarget string                  `json:"proposed_target,omitempty"`
}

type ModelAliasPreview struct {
	Alias  string                          `json:"alias"`
	Counts map[ModelAliasPreviewStatus]int `json:"counts"`
	Items  []ModelAliasChannelPreview      `json:"items"`
}

type ModelAliasApplyFailure struct {
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Error       string `json:"error"`
}

type ModelAliasApplyResult struct {
	Applied int                      `json:"applied"`
	Skipped int                      `json:"skipped"`
	Failed  []ModelAliasApplyFailure `json:"failed"`
}

type ModelAliasApplySelection struct {
	SelectedChannelIDs []int
	TargetModels       map[int]string
}

type ModelAliasChannelMatchSource string

const (
	ModelAliasChannelMatchSourceModels       ModelAliasChannelMatchSource = "models"
	ModelAliasChannelMatchSourceMappingKey   ModelAliasChannelMatchSource = "mapping_key"
	ModelAliasChannelMatchSourceMappingValue ModelAliasChannelMatchSource = "mapping_value"
)

type ModelAliasChannelRemovalPlan struct {
	RemovedModels      []string `json:"removed_models"`
	RemovedMappingKeys []string `json:"removed_mapping_keys"`
	RequiresConfirm    bool     `json:"requires_confirm"`
}

type ModelAliasChannelMatchedModel struct {
	Name        string                         `json:"name"`
	Sources     []ModelAliasChannelMatchSource `json:"sources"`
	RemovalPlan ModelAliasChannelRemovalPlan   `json:"removal_plan"`
}

type ModelAliasChannelMatch struct {
	ChannelID     int                             `json:"channel_id"`
	ChannelName   string                          `json:"channel_name"`
	ChannelStatus int                             `json:"channel_status"`
	Revision      string                          `json:"revision"`
	MappingError  string                          `json:"mapping_error,omitempty"`
	MatchedModels []ModelAliasChannelMatchedModel `json:"matched_models"`
}

type ModelAliasChannelMatches struct {
	Alias string                   `json:"alias"`
	Items []ModelAliasChannelMatch `json:"items"`
}

type ModelAliasChannelRemovalResult struct {
	ChannelID          int      `json:"channel_id"`
	ChannelName        string   `json:"channel_name"`
	RequestedModel     string   `json:"requested_model"`
	RemovedModels      []string `json:"removed_models"`
	RemovedMappingKeys []string `json:"removed_mapping_keys"`
	AffectedAliases    []string `json:"-"`
}

// ModelAliasPriceSyncSkip 记录定时同步因主模型缺少可用价格而跳过的别名组。
type ModelAliasPriceSyncSkip struct {
	Alias  string `json:"alias"`
	Reason string `json:"reason"`
}

type ModelAliasScanSummary struct {
	ScannedGroups   int                       `json:"scanned_groups"`
	ScannedChannels int                       `json:"scanned_channels"`
	PendingCount    int                       `json:"pending_count"`
	SyncedGroups    int                       `json:"synced_groups"`
	SyncedModels    int                       `json:"synced_models"`
	SkippedGroups   []ModelAliasPriceSyncSkip `json:"skipped_groups,omitempty"`
	revision        string
}

func (summary ModelAliasScanSummary) IsCurrent() bool {
	currentRevision, err := getModelAliasScanRevisionFromDatabase()
	return err == nil && currentRevision == summary.revision
}

func NormalizeModelAliasGroups(groups []ModelAliasGroup) ([]ModelAliasGroup, error) {
	if len(groups) > maxModelAliasGroups {
		return nil, fmt.Errorf("模型别名组不能超过 %d 个", maxModelAliasGroups)
	}

	normalized := make([]ModelAliasGroup, 0, len(groups))
	usedNames := make(map[string]string)
	for _, group := range groups {
		alias := strings.TrimSpace(group.Alias)
		if err := validateModelAliasName(alias, "统一名称"); err != nil {
			return nil, err
		}
		if owner, exists := usedNames[alias]; exists {
			return nil, fmt.Errorf("模型名称 %q 已在别名组 %q 中使用", alias, owner)
		}
		usedNames[alias] = alias

		if len(group.Models) == 0 {
			return nil, fmt.Errorf("别名组 %q 至少需要一个供应商模型名", alias)
		}
		if len(group.Models) > maxModelAliasGroupModels {
			return nil, fmt.Errorf("别名组 %q 的供应商模型名不能超过 %d 个", alias, maxModelAliasGroupModels)
		}

		models := make([]string, 0, len(group.Models))
		groupModelSet := make(map[string]struct{}, len(group.Models))
		for _, modelName := range group.Models {
			modelName = strings.TrimSpace(modelName)
			if err := validateModelAliasName(modelName, "供应商模型名"); err != nil {
				return nil, err
			}
			if _, exists := groupModelSet[modelName]; exists {
				continue
			}
			if owner, exists := usedNames[modelName]; exists {
				return nil, fmt.Errorf("模型名称 %q 已在别名组 %q 中使用", modelName, owner)
			}
			groupModelSet[modelName] = struct{}{}
			usedNames[modelName] = alias
			models = append(models, modelName)
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("别名组 %q 至少需要一个供应商模型名", alias)
		}
		normalized = append(normalized, ModelAliasGroup{Alias: alias, Models: models})
	}
	return normalized, nil
}

func validateModelAliasName(name string, field string) error {
	if name == "" {
		return fmt.Errorf("%s不能为空", field)
	}
	if len(name) > maxModelAliasNameLength {
		return fmt.Errorf("%s不能超过 %d 个字符", field, maxModelAliasNameLength)
	}
	if strings.ContainsAny(name, ",\r\n") {
		return fmt.Errorf("%s不能包含逗号或换行符", field)
	}
	return nil
}

// SearchModelAliasCatalog 从完整模型广场目录中查找可作为供应商名称的模型。
// 统一名称本身不能成为映射目标，因此按配置的精确匹配规则排除同名项。
func SearchModelAliasCatalog(keyword string) ([]string, error) {
	keyword = strings.TrimSpace(keyword)
	if err := validateModelAliasName(keyword, "统一名称"); err != nil {
		return nil, err
	}
	return filterModelAliasCatalog(GetPricing(), keyword), nil
}

func filterModelAliasCatalog(pricing []Pricing, keyword string) []string {
	lowerKeyword := strings.ToLower(keyword)
	matchedSet := make(map[string]struct{})
	for _, item := range pricing {
		modelName := strings.TrimSpace(item.ModelName)
		if modelName == "" || modelName == keyword {
			continue
		}
		if !strings.Contains(strings.ToLower(modelName), lowerKeyword) {
			continue
		}
		matchedSet[modelName] = struct{}{}
	}

	matched := make([]string, 0, len(matchedSet))
	for modelName := range matchedSet {
		matched = append(matched, modelName)
	}
	sort.Slice(matched, func(i, j int) bool {
		left := strings.ToLower(matched[i])
		right := strings.ToLower(matched[j])
		if left == right {
			return matched[i] < matched[j]
		}
		return left < right
	})
	return matched
}

func GetModelAliasGroups() ([]ModelAliasGroup, error) {
	common.OptionMapRWMutex.RLock()
	raw := common.Interface2String(common.OptionMap[ModelAliasGroupsOptionKey])
	common.OptionMapRWMutex.RUnlock()
	return parseModelAliasGroups(raw)
}

func parseModelAliasGroups(raw string) ([]ModelAliasGroup, error) {
	if strings.TrimSpace(raw) == "" {
		return []ModelAliasGroup{}, nil
	}

	groups := make([]ModelAliasGroup, 0)
	if err := common.UnmarshalJsonStr(raw, &groups); err != nil {
		return nil, fmt.Errorf("模型别名组配置格式无效: %w", err)
	}
	return NormalizeModelAliasGroups(groups)
}

func SaveModelAliasGroups(groups []ModelAliasGroup) ([]ModelAliasGroup, error) {
	configuration, err := SaveModelAliasConfiguration(
		groups,
		IsModelAliasScanEnabled(),
		GetModelAliasScanIntervalMinutes(),
	)
	if err != nil {
		return nil, err
	}
	return configuration.Groups, nil
}

func GetModelAliasConfiguration() (*ModelAliasConfiguration, error) {
	modelAliasOptionsMu.Lock()
	defer modelAliasOptionsMu.Unlock()

	groups, err := GetModelAliasGroups()
	if err != nil {
		return nil, err
	}
	counts := getModelAliasPendingCounts()
	for index := range groups {
		count, exists := counts[groups[index].Alias]
		if !exists {
			continue
		}
		groups[index].PendingCount = &count
	}
	return &ModelAliasConfiguration{
		Groups:              groups,
		ScanEnabled:         IsModelAliasScanEnabled(),
		ScanIntervalMinutes: GetModelAliasScanIntervalMinutes(),
	}, nil
}

func SaveModelAliasConfiguration(groups []ModelAliasGroup, scanEnabled bool, scanIntervalMinutes int) (*ModelAliasConfiguration, error) {
	configuration, _, err := SaveModelAliasConfigurationWithChanges(groups, scanEnabled, scanIntervalMinutes)
	return configuration, err
}

// SaveModelAliasConfigurationWithChanges 返回事务内判定的新增或内容变化标记，
// 供调用方决定是否立即排队扫描，避免使用事务外快照产生竞态。
func SaveModelAliasConfigurationWithChanges(groups []ModelAliasGroup, scanEnabled bool, scanIntervalMinutes int) (*ModelAliasConfiguration, bool, error) {
	normalized, err := NormalizeModelAliasGroups(groups)
	if err != nil {
		return nil, false, err
	}
	if scanIntervalMinutes < MinimumModelAliasScanIntervalMinutes {
		return nil, false, fmt.Errorf("模型别名扫描间隔不能小于 %d 分钟", MinimumModelAliasScanIntervalMinutes)
	}
	if int64(scanIntervalMinutes) > maxModelAliasScanIntervalMinutes {
		return nil, false, errors.New("模型别名扫描间隔过大")
	}
	data, err := common.Marshal(normalized)
	if err != nil {
		return nil, false, err
	}

	hasChangedGroups := false
	optionKeys := append([]string{
		ModelAliasGroupsOptionKey,
		ModelAliasScanEnabledOptionKey,
		ModelAliasScanIntervalOptionKey,
		ModelAliasPendingCountsOptionKey,
		modelAliasScanRevisionOptionKey,
		ModelPricingLocksOptionKey,
	}, modelPricingSyncOptionKeys...)
	err = mutateModelAliasPricingOptions(optionKeys, func(values map[string]string) (map[string]string, error) {
		currentGroups, parseErr := parseModelAliasGroups(values[ModelAliasGroupsOptionKey])
		if parseErr != nil {
			return nil, parseErr
		}
		counts, parseErr := parseModelAliasPendingCounts(values[ModelAliasPendingCountsOptionKey])
		if parseErr != nil {
			counts = make(map[string]int)
		}
		changedAliases := changedModelAliasGroups(currentGroups, normalized)
		for alias := range changedAliases {
			delete(counts, alias)
		}
		for alias := range counts {
			if !containsModelAliasGroup(normalized, alias) {
				delete(counts, alias)
			}
		}
		changedGroups := make([]ModelAliasGroup, 0, len(changedAliases))
		for _, group := range normalized {
			if _, changed := changedAliases[group.Alias]; changed {
				changedGroups = append(changedGroups, group)
			}
		}
		pricingUpdates := map[string]string{}
		if len(changedGroups) > 0 {
			var syncErr error
			pricingUpdates, _, syncErr = synchronizeModelAliasPricing(values, changedGroups, true)
			if syncErr != nil {
				return nil, syncErr
			}
			hasChangedGroups = true
		}
		countsData, marshalErr := common.Marshal(counts)
		if marshalErr != nil {
			return nil, marshalErr
		}
		updates := map[string]string{
			ModelAliasGroupsOptionKey:        string(data),
			ModelAliasScanEnabledOptionKey:   strconv.FormatBool(scanEnabled),
			ModelAliasScanIntervalOptionKey:  strconv.Itoa(scanIntervalMinutes),
			ModelAliasPendingCountsOptionKey: string(countsData),
			modelAliasScanRevisionOptionKey:  common.GetRandomString(24),
		}
		for key, value := range pricingUpdates {
			updates[key] = value
		}
		return updates, nil
	})
	if err != nil {
		return nil, false, err
	}
	configuration, err := GetModelAliasConfiguration()
	if err != nil {
		return nil, false, err
	}
	return configuration, hasChangedGroups, nil
}

func IsModelAliasScanEnabled() bool {
	common.OptionMapRWMutex.RLock()
	raw := common.Interface2String(common.OptionMap[ModelAliasScanEnabledOptionKey])
	common.OptionMapRWMutex.RUnlock()
	enabled, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return true
	}
	return enabled
}

func GetModelAliasScanIntervalMinutes() int {
	common.OptionMapRWMutex.RLock()
	raw := common.Interface2String(common.OptionMap[ModelAliasScanIntervalOptionKey])
	common.OptionMapRWMutex.RUnlock()
	interval, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || interval < MinimumModelAliasScanIntervalMinutes || int64(interval) > maxModelAliasScanIntervalMinutes {
		return DefaultModelAliasScanIntervalMinutes
	}
	return interval
}

func getModelAliasPendingCounts() map[string]int {
	common.OptionMapRWMutex.RLock()
	raw := common.Interface2String(common.OptionMap[ModelAliasPendingCountsOptionKey])
	common.OptionMapRWMutex.RUnlock()
	counts, err := parseModelAliasPendingCounts(raw)
	if err != nil {
		common.SysLog("模型别名待处理数量格式无效: " + err.Error())
		return make(map[string]int)
	}
	return counts
}

func parseModelAliasPendingCounts(raw string) (map[string]int, error) {
	counts := make(map[string]int)
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return counts, nil
	}
	if err := common.UnmarshalJsonStr(raw, &counts); err != nil {
		return nil, err
	}
	if counts == nil {
		return nil, errors.New("模型别名待处理数量必须是 JSON 对象")
	}
	for alias, count := range counts {
		if strings.TrimSpace(alias) == "" || count < 0 {
			return nil, errors.New("模型别名待处理数量包含无效数据")
		}
	}
	return counts, nil
}

func changedModelAliasGroups(current []ModelAliasGroup, next []ModelAliasGroup) map[string]struct{} {
	currentSignatures := modelAliasGroupSignatures(current)
	nextSignatures := modelAliasGroupSignatures(next)
	changed := make(map[string]struct{})
	for alias, signature := range currentSignatures {
		if nextSignatures[alias] != signature {
			changed[alias] = struct{}{}
		}
	}
	for alias, signature := range nextSignatures {
		if currentSignatures[alias] != signature {
			changed[alias] = struct{}{}
		}
	}
	return changed
}

func modelAliasGroupSignatures(groups []ModelAliasGroup) map[string]string {
	signatures := make(map[string]string, len(groups))
	for _, group := range groups {
		models := append([]string(nil), group.Models...)
		sort.Strings(models)
		signatures[group.Alias] = strings.Join(models, "\x00")
	}
	return signatures
}

func containsModelAliasGroup(groups []ModelAliasGroup, alias string) bool {
	for _, group := range groups {
		if group.Alias == alias {
			return true
		}
	}
	return false
}

// mutateModelAliasOptions 在同一事务内锁定并更新相关 Option，避免扫描与管理员操作互相覆盖。
func mutateModelAliasOptions(keys []string, mutate func(values map[string]string) (map[string]string, error)) error {
	// 事务提交和本地 OptionMap 更新必须保持同一顺序，避免并发操作把旧值回写到内存。
	modelAliasOptionsMu.Lock()
	defer modelAliasOptionsMu.Unlock()
	return mutateModelOptions(keys, mutate)
}

// mutateModelAliasPricingOptions 固定按价格锁、别名锁的顺序串行化跨配置事务。
func mutateModelAliasPricingOptions(keys []string, mutate func(values map[string]string) (map[string]string, error)) error {
	modelPricingMutationMutex.Lock()
	defer modelPricingMutationMutex.Unlock()
	modelAliasOptionsMu.Lock()
	defer modelAliasOptionsMu.Unlock()
	return mutateModelOptions(keys, mutate)
}

func mutateModelOptions(keys []string, mutate func(values map[string]string) (map[string]string, error)) error {
	updates := make(map[string]string)
	err := DB.Transaction(func(tx *gorm.DB) error {
		options, err := getOptionsForUpdate(tx, keys)
		if err != nil {
			return err
		}
		values := make(map[string]string, len(options))
		for key, option := range options {
			values[key] = option.Value
		}
		updates, err = mutate(values)
		if err != nil {
			return err
		}
		updateKeys := make([]string, 0, len(updates))
		for key := range updates {
			updateKeys = append(updateKeys, key)
		}
		sort.Strings(updateKeys)
		for _, key := range updateKeys {
			if err := tx.Model(&Option{}).Where(commonKeyCol+" = ?", key).Update("value", updates[key]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	updateKeys := make([]string, 0, len(updates))
	for key := range updates {
		updateKeys = append(updateKeys, key)
	}
	sort.Strings(updateKeys)
	pricingKeySet := make(map[string]struct{}, len(modelPricingSyncOptionKeys))
	for _, key := range modelPricingSyncOptionKeys {
		pricingKeySet[key] = struct{}{}
	}
	// 别名配置与价格锁没有额外运行时副作用，仍以一次内存写锁发布，避免读取到半套事务结果。
	common.OptionMapRWMutex.Lock()
	for _, key := range updateKeys {
		if _, isPricingOption := pricingKeySet[key]; !isPricingOption {
			common.OptionMap[key] = updates[key]
		}
	}
	common.OptionMapRWMutex.Unlock()
	pricingUpdates := make(map[string]string)
	for _, key := range updateKeys {
		if _, isPricingOption := pricingKeySet[key]; isPricingOption {
			pricingUpdates[key] = updates[key]
		}
	}
	if err := publishModelPricingOptions(pricingUpdates); err != nil {
		return err
	}
	return nil
}

func PreviewModelAliasGroup(alias string) (*ModelAliasPreview, error) {
	group, err := getModelAliasGroup(alias)
	if err != nil {
		return nil, err
	}
	channels, err := getChannelsForModelAlias()
	if err != nil {
		return nil, err
	}
	return buildModelAliasPreview(group, channels), nil
}

func ListModelAliasGroupChannels(alias string) (*ModelAliasChannelMatches, error) {
	group, err := getModelAliasGroup(alias)
	if err != nil {
		return nil, err
	}
	channels, err := getChannelsForModelAlias()
	if err != nil {
		return nil, err
	}

	result := &ModelAliasChannelMatches{
		Alias: group.Alias,
		Items: make([]ModelAliasChannelMatch, 0),
	}
	for _, channel := range channels {
		item, matched := buildModelAliasChannelMatch(group, channel)
		if matched {
			result.Items = append(result.Items, item)
		}
	}
	return result, nil
}

func RemoveModelAliasChannelModel(alias string, channelID int, modelName string, revision string, allowCascade bool) (*ModelAliasChannelRemovalResult, error) {
	alias = strings.TrimSpace(alias)
	modelName = strings.TrimSpace(modelName)
	revision = strings.TrimSpace(revision)
	if channelID <= 0 {
		return nil, errors.New("渠道 ID 无效")
	}
	if err := validateModelAliasName(alias, "统一名称"); err != nil {
		return nil, err
	}
	if err := validateModelAliasName(modelName, "模型名称"); err != nil {
		return nil, err
	}
	if len(revision) != sha256.Size*2 || strings.Trim(revision, "0123456789abcdef") != "" {
		return nil, errors.New("渠道配置版本无效")
	}

	result := &ModelAliasChannelRemovalResult{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		// 别名配置和渠道必须在同一事务内锁定，避免多实例并发修改后按旧归属删除模型。
		options, err := getOptionsForUpdate(tx, []string{ModelAliasGroupsOptionKey})
		if err != nil {
			return err
		}
		groups, err := parseModelAliasGroups(options[ModelAliasGroupsOptionKey].Value)
		if err != nil {
			return err
		}
		var group *ModelAliasGroup
		for index := range groups {
			if groups[index].Alias == alias {
				group = &groups[index]
				break
			}
		}
		if group == nil {
			return fmt.Errorf("模型别名组 %q 不存在", alias)
		}
		if modelName != group.Alias && !containsExactModel(group.Models, modelName) {
			return fmt.Errorf("模型名称 %q 不属于别名组 %q", modelName, alias)
		}

		var channel Channel
		if err := lockForUpdate(tx).Where("id = ?", channelID).First(&channel).Error; err != nil {
			return err
		}
		if modelAliasChannelRevision(&channel) != revision {
			return errors.New("渠道配置已变化，请刷新后重试")
		}

		mapping, err := parseModelAliasChannelMapping(channel.ModelMapping)
		if err != nil {
			return errors.New("渠道模型映射格式无效，请先在渠道设置中修复")
		}
		rawModels := channel.GetModels()
		models := normalizeModelAliasChannelModels(rawModels)
		if !modelAliasNameExists(models, mapping, modelName) {
			return fmt.Errorf("渠道 %d 中不存在模型名称 %q", channelID, modelName)
		}
		plan := planModelAliasChannelRemoval(models, mapping, modelName)
		if plan.RequiresConfirm && !allowCascade {
			return errors.New("删除会同时影响模型映射，请确认后重试")
		}

		removedModelSet := make(map[string]struct{}, len(plan.RemovedModels))
		for _, removedModel := range plan.RemovedModels {
			removedModelSet[removedModel] = struct{}{}
		}
		remainingModels := make([]string, 0, len(rawModels))
		for _, currentModel := range rawModels {
			trimmedModel := strings.TrimSpace(currentModel)
			if trimmedModel == "" {
				continue
			}
			if _, removed := removedModelSet[trimmedModel]; !removed {
				remainingModels = append(remainingModels, currentModel)
			}
		}

		removedMappingSet := make(map[string]struct{}, len(plan.RemovedMappingKeys))
		for _, mappingKey := range plan.RemovedMappingKeys {
			removedMappingSet[mappingKey] = struct{}{}
		}
		remainingMapping := make(map[string]string, len(mapping)-len(plan.RemovedMappingKeys))
		for key, value := range mapping {
			if _, removed := removedMappingSet[key]; !removed {
				remainingMapping[key] = value
			}
		}

		updates := map[string]any{}
		if len(plan.RemovedModels) > 0 {
			channel.Models = strings.Join(remainingModels, ",")
			updates["models"] = channel.Models
		}
		if len(plan.RemovedMappingKeys) > 0 {
			mappingData, marshalErr := common.Marshal(remainingMapping)
			if marshalErr != nil {
				return marshalErr
			}
			mappingText := string(mappingData)
			channel.ModelMapping = &mappingText
			updates["model_mapping"] = mappingText
		}
		if err = tx.Model(&Channel{}).Where("id = ?", channel.Id).Updates(updates).Error; err != nil {
			return err
		}
		if len(plan.RemovedModels) > 0 {
			if channel.Models == "" {
				if err = tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error; err != nil {
					return err
				}
			} else if err = channel.UpdateAbilities(tx); err != nil {
				return err
			}
		}

		result.ChannelID = channel.Id
		result.ChannelName = channel.Name
		result.RequestedModel = modelName
		result.RemovedModels = plan.RemovedModels
		result.RemovedMappingKeys = plan.RemovedMappingKeys
		result.AffectedAliases = affectedModelAliasGroups(groups, plan)
		if !containsExactModel(result.AffectedAliases, alias) {
			result.AffectedAliases = append(result.AffectedAliases, alias)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	InitChannelCache()
	return result, nil
}

func ApplyModelAliasGroup(alias string) (*ModelAliasApplyResult, error) {
	return ApplyModelAliasGroupWithSelection(alias, ModelAliasApplySelection{})
}

func ApplyModelAliasGroupWithSelection(alias string, selection ModelAliasApplySelection) (*ModelAliasApplyResult, error) {
	group, err := getModelAliasGroup(alias)
	if err != nil {
		return nil, err
	}
	channels, err := getChannelsForModelAlias()
	if err != nil {
		return nil, err
	}
	preview := buildModelAliasPreview(group, channels)
	result := &ModelAliasApplyResult{Failed: make([]ModelAliasApplyFailure, 0)}
	selectedChannelIDs, err := normalizeModelAliasSelectedChannelIDs(selection.SelectedChannelIDs)
	if err != nil {
		return nil, err
	}
	targetModels, err := normalizeModelAliasTargetModels(selection.TargetModels)
	if err != nil {
		return nil, err
	}
	hasSelectedChannels := len(selectedChannelIDs) > 0
	previewChannelIDs := make(map[int]struct{}, len(preview.Items))
	for _, item := range preview.Items {
		previewChannelIDs[item.ChannelID] = struct{}{}
	}
	for channelID := range selectedChannelIDs {
		if _, exists := previewChannelIDs[channelID]; !exists {
			return nil, fmt.Errorf("渠道 %d 不存在于当前预览中", channelID)
		}
	}
	for channelID := range targetModels {
		if _, exists := previewChannelIDs[channelID]; !exists {
			return nil, fmt.Errorf("渠道 %d 不存在于当前预览中", channelID)
		}
	}

	for _, item := range preview.Items {
		if hasSelectedChannels {
			if _, selected := selectedChannelIDs[item.ChannelID]; !selected {
				result.Skipped++
				continue
			}
		}
		selectedTarget := targetModels[item.ChannelID]
		if !isModelAliasApplyCandidate(item, selectedTarget) {
			result.Skipped++
			continue
		}
		applied, applyErr := applyModelAliasToChannel(item.ChannelID, group, selectedTarget)
		if applyErr != nil {
			result.Failed = append(result.Failed, ModelAliasApplyFailure{
				ChannelID:   item.ChannelID,
				ChannelName: item.ChannelName,
				Error:       applyErr.Error(),
			})
			continue
		}
		if applied {
			result.Applied++
		} else {
			result.Skipped++
		}
	}
	if result.Applied > 0 {
		InitChannelCache()
	}
	return result, nil
}

func normalizeModelAliasSelectedChannelIDs(channelIDs []int) (map[int]struct{}, error) {
	selected := make(map[int]struct{}, len(channelIDs))
	for _, channelID := range channelIDs {
		if channelID <= 0 {
			return nil, errors.New("渠道 ID 无效")
		}
		selected[channelID] = struct{}{}
	}
	return selected, nil
}

func normalizeModelAliasTargetModels(targets map[int]string) (map[int]string, error) {
	normalized := make(map[int]string, len(targets))
	for channelID, target := range targets {
		if channelID <= 0 {
			return nil, errors.New("渠道 ID 无效")
		}
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if err := validateModelAliasName(target, "目标模型名称"); err != nil {
			return nil, err
		}
		normalized[channelID] = target
	}
	return normalized, nil
}

func isModelAliasApplyCandidate(item ModelAliasChannelPreview, selectedTarget string) bool {
	if item.Status == ModelAliasPreviewStatusNew || item.Status == ModelAliasPreviewStatusUpdated {
		return true
	}
	if selectedTarget == "" || selectedTarget == item.CurrentTarget {
		return false
	}
	if item.Status != ModelAliasPreviewStatusMultipleMatches {
		return false
	}
	// 非法目标仍需进入行锁内校验，以便向调用方返回明确的失败原因。
	return true
}

func InvalidateModelAliasPendingCount(alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return errors.New("统一名称不能为空")
	}
	return mutateModelAliasOptions(
		[]string{ModelAliasPendingCountsOptionKey, modelAliasScanRevisionOptionKey},
		func(values map[string]string) (map[string]string, error) {
			counts, err := parseModelAliasPendingCounts(values[ModelAliasPendingCountsOptionKey])
			if err != nil {
				counts = make(map[string]int)
			}
			delete(counts, alias)
			data, err := common.Marshal(counts)
			if err != nil {
				return nil, err
			}
			return map[string]string{
				ModelAliasPendingCountsOptionKey: string(data),
				modelAliasScanRevisionOptionKey:  common.GetRandomString(24),
			}, nil
		},
	)
}

func ScanModelAliasPendingCounts(ctx context.Context, report func(processed, total int)) (ModelAliasScanSummary, error) {
	for {
		if err := ctx.Err(); err != nil {
			return ModelAliasScanSummary{}, err
		}
		groups, revision, enabled, err := getModelAliasScanSnapshot()
		if err != nil {
			return ModelAliasScanSummary{}, err
		}
		if !enabled {
			return ModelAliasScanSummary{revision: revision}, nil
		}
		channels, err := getChannelsForModelAliasContext(ctx)
		if err != nil {
			return ModelAliasScanSummary{}, err
		}
		counts := make(map[string]int, len(groups))
		summary := ModelAliasScanSummary{
			ScannedGroups:   len(groups),
			ScannedChannels: len(channels),
			revision:        revision,
		}
		if len(groups) == 0 && report != nil {
			report(0, 0)
		}
		for groupIndex, group := range groups {
			pendingCount := 0
			for channelIndex, channel := range channels {
				if channelIndex%100 == 0 {
					if err := ctx.Err(); err != nil {
						return ModelAliasScanSummary{}, err
					}
				}
				item, _ := classifyModelAliasChannel(channel, group)
				if isPendingModelAliasStatus(item.Status) {
					pendingCount++
				}
			}
			counts[group.Alias] = pendingCount
			summary.PendingCount += pendingCount
			if report != nil {
				report(groupIndex+1, len(groups))
			}
		}

		stored, syncResult, err := saveModelAliasScanResult(revision, groups, counts)
		if err != nil {
			return ModelAliasScanSummary{}, err
		}
		if !stored {
			continue
		}
		currentRevision, err := getModelAliasScanRevisionFromDatabase()
		if err != nil {
			return ModelAliasScanSummary{}, err
		}
		if currentRevision != revision {
			continue
		}
		summary.SyncedGroups = syncResult.SyncedGroups
		summary.SyncedModels = syncResult.SyncedModels
		summary.SkippedGroups = syncResult.SkippedGroups
		for _, skippedGroup := range summary.SkippedGroups {
			common.SysLog(fmt.Sprintf("模型别名组 %s 定时价格同步已跳过: %s", skippedGroup.Alias, skippedGroup.Reason))
		}
		return summary, nil
	}
}

func getModelAliasScanRevision() string {
	common.OptionMapRWMutex.RLock()
	revision := common.Interface2String(common.OptionMap[modelAliasScanRevisionOptionKey])
	common.OptionMapRWMutex.RUnlock()
	return revision
}

func getModelAliasScanRevisionFromDatabase() (string, error) {
	var option Option
	result := DB.Select("value").Where(commonKeyCol+" = ?", modelAliasScanRevisionOptionKey).Limit(1).Find(&option)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", nil
	}
	return option.Value, nil
}

func getModelAliasScanSnapshot() ([]ModelAliasGroup, string, bool, error) {
	defaults := map[string]string{
		ModelAliasGroupsOptionKey:       "[]",
		ModelAliasScanEnabledOptionKey:  "true",
		modelAliasScanRevisionOptionKey: "",
	}
	keys := []string{ModelAliasGroupsOptionKey, ModelAliasScanEnabledOptionKey, modelAliasScanRevisionOptionKey}
	var options []Option
	if err := DB.Select(commonKeyCol+", value").Where(commonKeyCol+" IN ?", keys).Find(&options).Error; err != nil {
		return nil, "", false, err
	}
	values := defaults
	for _, option := range options {
		values[option.Key] = option.Value
	}
	groups, err := parseModelAliasGroups(values[ModelAliasGroupsOptionKey])
	if err != nil {
		return nil, "", false, err
	}
	enabled, err := strconv.ParseBool(values[ModelAliasScanEnabledOptionKey])
	if err != nil {
		return nil, "", false, err
	}
	return groups, values[modelAliasScanRevisionOptionKey], enabled, nil
}

func saveModelAliasPendingCounts(revision string, counts map[string]int) (bool, error) {
	data, err := common.Marshal(counts)
	if err != nil {
		return false, err
	}
	stored := false
	err = mutateModelAliasOptions(
		[]string{ModelAliasPendingCountsOptionKey, modelAliasScanRevisionOptionKey},
		func(values map[string]string) (map[string]string, error) {
			if values[modelAliasScanRevisionOptionKey] != revision {
				return nil, nil
			}
			stored = true
			return map[string]string{ModelAliasPendingCountsOptionKey: string(data)}, nil
		},
	)
	return stored, err
}

func saveModelAliasScanResult(revision string, groups []ModelAliasGroup, counts map[string]int) (bool, modelAliasPricingSyncResult, error) {
	data, err := common.Marshal(counts)
	if err != nil {
		return false, modelAliasPricingSyncResult{}, err
	}
	stored := false
	syncResult := modelAliasPricingSyncResult{}
	keys := append([]string{
		ModelAliasScanEnabledOptionKey,
		ModelAliasPendingCountsOptionKey,
		modelAliasScanRevisionOptionKey,
		ModelPricingLocksOptionKey,
	}, modelPricingSyncOptionKeys...)
	err = mutateModelAliasPricingOptions(keys, func(values map[string]string) (map[string]string, error) {
		if values[modelAliasScanRevisionOptionKey] != revision || values[ModelAliasScanEnabledOptionKey] != "true" {
			return nil, nil
		}
		pricingUpdates, result, syncErr := synchronizeModelAliasPricing(values, groups, false)
		if syncErr != nil {
			return nil, syncErr
		}
		stored = true
		syncResult = result
		pricingUpdates[ModelAliasPendingCountsOptionKey] = string(data)
		return pricingUpdates, nil
	})
	return stored, syncResult, err
}

func isPendingModelAliasStatus(status ModelAliasPreviewStatus) bool {
	return status == ModelAliasPreviewStatusNew ||
		status == ModelAliasPreviewStatusUpdated ||
		status == ModelAliasPreviewStatusConflict ||
		status == ModelAliasPreviewStatusMultipleMatches
}

func getModelAliasGroup(alias string) (ModelAliasGroup, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return ModelAliasGroup{}, errors.New("统一名称不能为空")
	}
	groups, err := GetModelAliasGroups()
	if err != nil {
		return ModelAliasGroup{}, err
	}
	for _, group := range groups {
		if group.Alias == alias {
			return group, nil
		}
	}
	return ModelAliasGroup{}, fmt.Errorf("模型别名组 %q 不存在", alias)
}

func getChannelsForModelAlias() ([]*Channel, error) {
	return getChannelsForModelAliasContext(context.Background())
}

func getChannelsForModelAliasContext(ctx context.Context) ([]*Channel, error) {
	channels := make([]*Channel, 0)
	err := DB.WithContext(ctx).Select("id", "name", "status", "models", "model_mapping").Order("id ASC").Find(&channels).Error
	return channels, err
}

func buildModelAliasPreview(group ModelAliasGroup, channels []*Channel) *ModelAliasPreview {
	preview := &ModelAliasPreview{
		Alias:  group.Alias,
		Counts: make(map[ModelAliasPreviewStatus]int),
		Items:  make([]ModelAliasChannelPreview, 0, len(channels)),
	}
	for _, channel := range channels {
		item, _ := classifyModelAliasChannel(channel, group)
		preview.Counts[item.Status]++
		preview.Items = append(preview.Items, item)
	}
	return preview
}

func classifyModelAliasChannel(channel *Channel, group ModelAliasGroup) (ModelAliasChannelPreview, map[string]string) {
	// 预览只按渠道当前模型列表做精确匹配，避免把供应商的相似命名误判为同一模型。
	item := ModelAliasChannelPreview{
		ChannelID:     channel.Id,
		ChannelName:   channel.Name,
		ChannelStatus: channel.Status,
		MatchedModels: make([]string, 0),
	}
	channelModels := normalizeModelAliasChannelModels(channel.GetModels())
	channelModelSet := make(map[string]struct{}, len(channelModels))
	for _, modelName := range channelModels {
		channelModelSet[modelName] = struct{}{}
	}
	for _, modelName := range group.Models {
		if _, exists := channelModelSet[modelName]; exists {
			item.MatchedModels = append(item.MatchedModels, modelName)
		}
	}

	if len(item.MatchedModels) == 0 {
		item.Status = ModelAliasPreviewStatusUnmatched
		item.Reason = "no_matching_model"
		return item, nil
	}

	mapping, err := parseModelAliasChannelMapping(channel.ModelMapping)
	if err != nil {
		item.Status = ModelAliasPreviewStatusConflict
		item.Reason = "invalid_mapping"
		return item, nil
	}
	currentTarget, mappingExists := mapping[group.Alias]
	item.CurrentTarget = strings.TrimSpace(currentTarget)

	if len(item.MatchedModels) > 1 {
		// 多匹配状态表达候选歧义；已有有效映射只用于回填下拉框，不能改变该状态。
		if mappingExists && containsExactModel(item.MatchedModels, item.CurrentTarget) &&
			modelAliasMappingWouldCycle(mapping, group.Alias, item.CurrentTarget) {
			item.Status = ModelAliasPreviewStatusConflict
			item.Reason = "mapping_target_conflict"
			return item, mapping
		}
		item.Status = ModelAliasPreviewStatusMultipleMatches
		item.Reason = "multiple_matching_models"
		return item, mapping
	}

	item.ProposedTarget = item.MatchedModels[0]
	_, aliasInModels := channelModelSet[group.Alias]
	if modelAliasMappingWouldCycle(mapping, group.Alias, item.ProposedTarget) {
		item.Status = ModelAliasPreviewStatusConflict
		item.Reason = "mapping_target_conflict"
		return item, mapping
	}
	if !mappingExists {
		if aliasInModels {
			item.Status = ModelAliasPreviewStatusConflict
			item.Reason = "alias_already_in_models"
			return item, mapping
		}
		item.Status = ModelAliasPreviewStatusNew
		return item, mapping
	}
	if item.CurrentTarget == "" {
		item.Status = ModelAliasPreviewStatusConflict
		item.Reason = "empty_mapping_target"
		return item, mapping
	}
	if item.CurrentTarget == item.ProposedTarget {
		if aliasInModels {
			item.Status = ModelAliasPreviewStatusUnchanged
		} else {
			item.Status = ModelAliasPreviewStatusNew
		}
		return item, mapping
	}
	if containsExactModel(group.Models, item.CurrentTarget) {
		if _, stillAvailable := channelModelSet[item.CurrentTarget]; !stillAvailable {
			item.Status = ModelAliasPreviewStatusUpdated
			return item, mapping
		}
	}

	item.Status = ModelAliasPreviewStatusConflict
	item.Reason = "mapping_target_conflict"
	return item, mapping
}

func modelAliasMappingWouldCycle(mapping map[string]string, alias string, target string) bool {
	visited := map[string]struct{}{alias: {}}
	current := target
	for current != "" {
		if _, exists := visited[current]; exists {
			return true
		}
		visited[current] = struct{}{}
		next, exists := mapping[current]
		if !exists {
			return false
		}
		current = strings.TrimSpace(next)
	}
	return false
}

func applyModelAliasToChannel(channelID int, group ModelAliasGroup, selectedTarget string) (bool, error) {
	applied := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := lockForUpdate(tx).Where("id = ?", channelID).First(&channel).Error; err != nil {
			return err
		}
		// 预览和实际执行之间可能发生人工修改，因此必须在行锁内重新判断。
		item, mapping := classifyModelAliasChannel(&channel, group)
		if mapping == nil {
			mapping = make(map[string]string)
		}
		if selectedTarget != "" {
			if !containsExactModel(item.MatchedModels, selectedTarget) {
				return fmt.Errorf("渠道 %d 的目标模型不在匹配结果中", channelID)
			}
			item.ProposedTarget = selectedTarget
		}
		if !isModelAliasApplyCandidate(item, selectedTarget) {
			return nil
		}
		if modelAliasMappingWouldCycle(mapping, group.Alias, item.ProposedTarget) {
			return fmt.Errorf("渠道 %d 的目标模型会形成循环映射", channelID)
		}

		mapping[group.Alias] = item.ProposedTarget
		mappingData, err := common.Marshal(mapping)
		if err != nil {
			return err
		}
		models := normalizeModelAliasChannelModels(channel.GetModels())
		if !containsExactModel(models, group.Alias) {
			models = append(models, group.Alias)
		}
		channel.Models = strings.Join(models, ",")
		mappingText := string(mappingData)
		channel.ModelMapping = &mappingText

		if err = tx.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
			"models":        channel.Models,
			"model_mapping": mappingText,
		}).Error; err != nil {
			return err
		}
		if err = channel.UpdateAbilities(tx); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

func parseModelAliasChannelMapping(raw *string) (map[string]string, error) {
	mapping := make(map[string]string)
	if raw == nil || strings.TrimSpace(*raw) == "" || strings.TrimSpace(*raw) == "{}" {
		return mapping, nil
	}
	if err := common.UnmarshalJsonStr(*raw, &mapping); err != nil {
		return nil, err
	}
	if mapping == nil {
		return nil, errors.New("模型映射必须是 JSON 对象")
	}
	return mapping, nil
}

func buildModelAliasChannelMatch(group ModelAliasGroup, channel *Channel) (ModelAliasChannelMatch, bool) {
	item := ModelAliasChannelMatch{
		ChannelID:     channel.Id,
		ChannelName:   channel.Name,
		ChannelStatus: channel.Status,
		Revision:      modelAliasChannelRevision(channel),
		MatchedModels: make([]ModelAliasChannelMatchedModel, 0),
	}
	models := normalizeModelAliasChannelModels(channel.GetModels())
	mapping, err := parseModelAliasChannelMapping(channel.ModelMapping)
	if err != nil {
		item.MappingError = "invalid_mapping"
		mapping = nil
	}

	candidates := make([]string, 0, len(group.Models)+1)
	candidates = append(candidates, group.Alias)
	candidates = append(candidates, group.Models...)
	for _, candidate := range candidates {
		sources := make([]ModelAliasChannelMatchSource, 0, 3)
		if containsExactModel(models, candidate) {
			sources = append(sources, ModelAliasChannelMatchSourceModels)
		}
		if mapping != nil {
			if _, exists := mapping[candidate]; exists {
				sources = append(sources, ModelAliasChannelMatchSourceMappingKey)
			}
			for _, target := range mapping {
				if target == candidate {
					sources = append(sources, ModelAliasChannelMatchSourceMappingValue)
					break
				}
			}
		}
		if len(sources) == 0 {
			continue
		}
		matched := ModelAliasChannelMatchedModel{Name: candidate, Sources: sources}
		if mapping != nil {
			matched.RemovalPlan = planModelAliasChannelRemoval(models, mapping, candidate)
		}
		item.MatchedModels = append(item.MatchedModels, matched)
	}
	return item, len(item.MatchedModels) > 0
}

func planModelAliasChannelRemoval(models []string, mapping map[string]string, modelName string) ModelAliasChannelRemovalPlan {
	removedNames := map[string]struct{}{modelName: {}}
	removedMappingKeys := make(map[string]struct{})
	for {
		changed := false
		for key, value := range mapping {
			if _, removed := removedMappingKeys[key]; removed {
				continue
			}
			_, keyRemoved := removedNames[key]
			_, valueRemoved := removedNames[value]
			if !keyRemoved && !valueRemoved {
				continue
			}
			removedMappingKeys[key] = struct{}{}
			if _, exists := removedNames[key]; !exists {
				removedNames[key] = struct{}{}
			}
			changed = true
		}
		if !changed {
			break
		}
	}

	removedModels := make([]string, 0)
	for _, currentModel := range models {
		if _, removed := removedNames[currentModel]; removed {
			removedModels = append(removedModels, currentModel)
		}
	}
	removedKeys := make([]string, 0, len(removedMappingKeys))
	for key := range removedMappingKeys {
		removedKeys = append(removedKeys, key)
	}
	sort.Strings(removedKeys)
	return ModelAliasChannelRemovalPlan{
		RemovedModels:      removedModels,
		RemovedMappingKeys: removedKeys,
		RequiresConfirm:    len(removedKeys) > 0 || len(removedModels) > 1,
	}
}

func modelAliasNameExists(models []string, mapping map[string]string, modelName string) bool {
	if containsExactModel(models, modelName) {
		return true
	}
	if _, exists := mapping[modelName]; exists {
		return true
	}
	for _, target := range mapping {
		if target == modelName {
			return true
		}
	}
	return false
}

func modelAliasChannelRevision(channel *Channel) string {
	mapping := ""
	if channel.ModelMapping != nil {
		mapping = *channel.ModelMapping
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(channel.Models+"\x00"+mapping)))
}

func affectedModelAliasGroups(groups []ModelAliasGroup, plan ModelAliasChannelRemovalPlan) []string {
	affectedNames := make(map[string]struct{}, len(plan.RemovedModels)+len(plan.RemovedMappingKeys))
	for _, name := range plan.RemovedModels {
		affectedNames[name] = struct{}{}
	}
	for _, name := range plan.RemovedMappingKeys {
		affectedNames[name] = struct{}{}
	}
	affectedAliases := make([]string, 0)
	for _, group := range groups {
		if _, affected := affectedNames[group.Alias]; affected {
			affectedAliases = append(affectedAliases, group.Alias)
			continue
		}
		for _, providerModel := range group.Models {
			if _, affected := affectedNames[providerModel]; affected {
				affectedAliases = append(affectedAliases, group.Alias)
				break
			}
		}
	}
	return affectedAliases
}

func normalizeModelAliasChannelModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, modelName := range models {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, exists := seen[modelName]; exists {
			continue
		}
		seen[modelName] = struct{}{}
		normalized = append(normalized, modelName)
	}
	return normalized
}

func containsExactModel(models []string, target string) bool {
	for _, modelName := range models {
		if modelName == target {
			return true
		}
	}
	return false
}
