package model

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelPricingLocksOptionKey = "ModelPricingLocks"

var modelPricingSyncOptionKeys = []string{
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

var modelPricingSyncFieldOptionKeys = map[string]string{
	"model_ratio":            "ModelRatio",
	"completion_ratio":       "CompletionRatio",
	"cache_ratio":            "CacheRatio",
	"create_cache_ratio":     "CreateCacheRatio",
	"image_ratio":            "ImageRatio",
	"audio_ratio":            "AudioRatio",
	"audio_completion_ratio": "AudioCompletionRatio",
	"model_price":            "ModelPrice",
	"billing_mode":           "billing_setting.billing_mode",
	"billing_expr":           "billing_setting.billing_expr",
}

var modelPricingRatioOptionKeys = []string{
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

// 同步应用和锁变更必须连同内存配置发布一起串行，避免并发请求交错刷新缓存。
var modelPricingMutationMutex sync.Mutex
var modelPricingRuntimeRWMutex sync.RWMutex

type ModelPricingRuntimeSnapshot struct {
	ModelPrice              float64
	HasModelPrice           bool
	ModelRatio              float64
	HasModelRatio           bool
	MatchedModelName        string
	CompletionRatio         float64
	CacheRatio              float64
	HasCacheRatio           bool
	CreateCacheRatio        float64
	HasCreateCacheRatio     bool
	ImageRatio              float64
	HasImageRatio           bool
	AudioRatio              float64
	HasAudioRatio           bool
	AudioCompletionRatio    float64
	HasAudioCompletionRatio bool
	BillingMode             string
	BillingExpr             string
	HasBillingExpr          bool
}

type ModelPricingSyncResult struct {
	AppliedModels       []string
	IgnoredLockedModels []string
}

type ModelPricingLockUpdateResult struct {
	LockedModels  []string
	ChangedModels []string
}

type modelAliasPricingSyncResult struct {
	SyncedGroups  int
	SyncedModels  int
	SkippedGroups []ModelAliasPriceSyncSkip
}

func parseModelPricingLocks(value string) (map[string]bool, error) {
	locks := make(map[string]bool)
	if value == "" {
		return locks, nil
	}
	if err := common.UnmarshalJsonStr(value, &locks); err != nil {
		return nil, err
	}
	for modelName, locked := range locks {
		if !locked {
			delete(locks, modelName)
		}
	}
	return locks, nil
}

func marshalModelPricingLocks(locks map[string]bool) (string, error) {
	data, err := common.Marshal(locks)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func sortedLockedModelNames(locks map[string]bool) []string {
	models := make([]string, 0, len(locks))
	for modelName := range locks {
		models = append(models, modelName)
	}
	sort.Strings(models)
	return models
}

func optionDefaultValue(key string) string {
	switch key {
	case ModelPricingLocksOptionKey, ModelAliasPendingCountsOptionKey:
		return "{}"
	case ModelAliasGroupsOptionKey:
		return "[]"
	case ModelAliasScanEnabledOptionKey:
		return "true"
	case ModelAliasScanIntervalOptionKey:
		return "30"
	case modelAliasScanRevisionOptionKey:
		return ""
	}
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap[key]
}

func ensureOptionRows(tx *gorm.DB, keys []string) error {
	for _, key := range keys {
		option := Option{Key: key, Value: optionDefaultValue(key)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&option).Error; err != nil {
			return err
		}
	}
	return nil
}

func getOptionForUpdate(tx *gorm.DB, key string) (Option, error) {
	var option Option
	err := lockForUpdate(tx).Where(commonKeyCol+" = ?", key).First(&option).Error
	return option, err
}

// getOptionsForUpdate 统一按 Option 键名排序并逐行加锁，避免多实例事务因锁顺序不同发生死锁。
func getOptionsForUpdate(tx *gorm.DB, keys []string) (map[string]Option, error) {
	keySet := make(map[string]struct{}, len(keys))
	orderedKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, exists := keySet[key]; exists {
			continue
		}
		keySet[key] = struct{}{}
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	if err := ensureOptionRows(tx, orderedKeys); err != nil {
		return nil, err
	}
	options := make(map[string]Option, len(orderedKeys))
	for _, key := range orderedKeys {
		option, err := getOptionForUpdate(tx, key)
		if err != nil {
			return nil, err
		}
		options[key] = option
	}
	return options, nil
}

func GetModelPricingLocks() (map[string]bool, error) {
	var option Option
	err := DB.Where(commonKeyCol+" = ?", ModelPricingLocksOptionKey).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseModelPricingLocks(option.Value)
}

func SetModelPricingLock(modelName string, locked bool) ([]string, error) {
	result, err := SetModelPricingLocks([]string{modelName}, locked)
	if err != nil {
		return nil, err
	}
	return result.LockedModels, nil
}

// SetModelPricingLocks 在同一事务中更新整批锁，避免逐条请求产生部分成功或缓存交错。
func SetModelPricingLocks(modelNames []string, locked bool) (ModelPricingLockUpdateResult, error) {
	modelPricingMutationMutex.Lock()
	defer modelPricingMutationMutex.Unlock()
	var serialized string
	var lockedModels []string
	changedModels := make([]string, 0, len(modelNames))
	err := DB.Transaction(func(tx *gorm.DB) error {
		keys := []string{ModelPricingLocksOptionKey}
		if !locked {
			keys = append(keys, ModelAliasGroupsOptionKey)
		}
		options, err := getOptionsForUpdate(tx, keys)
		if err != nil {
			return err
		}
		option := options[ModelPricingLocksOptionKey]
		locks, err := parseModelPricingLocks(option.Value)
		if err != nil {
			return err
		}
		protectedModels := make(map[string]bool)
		if !locked {
			groups, parseErr := parseModelAliasGroups(options[ModelAliasGroupsOptionKey].Value)
			if parseErr != nil {
				return parseErr
			}
			for _, group := range groups {
				protectedModels[group.Alias] = true
				for _, modelName := range group.Models {
					protectedModels[modelName] = true
				}
			}
		}
		for _, modelName := range modelNames {
			if locked {
				if locks[modelName] {
					continue
				}
				locks[modelName] = true
				changedModels = append(changedModels, modelName)
				continue
			}
			if protectedModels[modelName] {
				continue
			}
			if !locks[modelName] {
				continue
			}
			delete(locks, modelName)
			changedModels = append(changedModels, modelName)
		}
		lockedModels = sortedLockedModelNames(locks)
		if len(changedModels) == 0 {
			serialized = option.Value
			return nil
		}
		serialized, err = marshalModelPricingLocks(locks)
		if err != nil {
			return err
		}
		option.Value = serialized
		return tx.Save(&option).Error
	})
	if err != nil {
		return ModelPricingLockUpdateResult{}, err
	}
	if err := updateOptionMap(ModelPricingLocksOptionKey, serialized); err != nil {
		return ModelPricingLockUpdateResult{}, err
	}
	sort.Strings(changedModels)
	return ModelPricingLockUpdateResult{
		LockedModels:  lockedModels,
		ChangedModels: changedModels,
	}, nil
}

func parsePricingOptionValue(value string) (map[string]any, error) {
	parsed := make(map[string]any)
	if value == "" {
		return parsed, nil
	}
	if err := common.UnmarshalJsonStr(value, &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

// synchronizeModelAliasPricing 将统一模型名称的完整计费配置复制到组内模型，并补齐价格锁。
// 调用方负责在持有价格变更锁的事务中传入已经锁定的 Option 值。
func synchronizeModelAliasPricing(values map[string]string, groups []ModelAliasGroup, strict bool) (map[string]string, modelAliasPricingSyncResult, error) {
	pricingValues := make(map[string]map[string]any, len(modelPricingSyncOptionKeys))
	for _, key := range modelPricingSyncOptionKeys {
		parsed, err := parsePricingOptionValue(values[key])
		if err != nil {
			return nil, modelAliasPricingSyncResult{}, fmt.Errorf("解析模型价格配置 %s 失败: %w", key, err)
		}
		pricingValues[key] = parsed
	}
	locks, err := parseModelPricingLocks(values[ModelPricingLocksOptionKey])
	if err != nil {
		return nil, modelAliasPricingSyncResult{}, fmt.Errorf("解析模型价格锁失败: %w", err)
	}

	result := modelAliasPricingSyncResult{}
	for _, group := range groups {
		// 即使主模型临时缺少价格，也要恢复组内价格锁，防止上游同步覆盖成员的旧价格。
		locks[group.Alias] = true
		for _, modelName := range group.Models {
			locks[modelName] = true
		}

		template, templateErr := buildModelAliasPricingTemplate(pricingValues, group.Alias)
		if templateErr != nil {
			if strict {
				return nil, modelAliasPricingSyncResult{}, fmt.Errorf("别名组 %q 的主模型价格无效: %w", group.Alias, templateErr)
			}
			result.SkippedGroups = append(result.SkippedGroups, ModelAliasPriceSyncSkip{
				Alias:  group.Alias,
				Reason: templateErr.Error(),
			})
			continue
		}

		for _, modelName := range group.Models {
			for _, key := range modelPricingSyncOptionKeys {
				delete(pricingValues[key], modelName)
			}
			for key, value := range template {
				pricingValues[key][modelName] = value
			}
		}
		result.SyncedGroups++
		result.SyncedModels += len(group.Models)
	}

	updates := make(map[string]string, len(modelPricingSyncOptionKeys)+1)
	for _, key := range modelPricingSyncOptionKeys {
		data, err := common.Marshal(pricingValues[key])
		if err != nil {
			return nil, modelAliasPricingSyncResult{}, err
		}
		serialized := string(data)
		if serialized != values[key] {
			updates[key] = serialized
		}
	}
	serializedLocks, err := marshalModelPricingLocks(locks)
	if err != nil {
		return nil, modelAliasPricingSyncResult{}, err
	}
	if serializedLocks != values[ModelPricingLocksOptionKey] {
		updates[ModelPricingLocksOptionKey] = serializedLocks
	}
	return updates, result, nil
}

func buildModelAliasPricingTemplate(pricingValues map[string]map[string]any, modelName string) (map[string]any, error) {
	billingMode, hasBillingMode := pricingValues["billing_setting.billing_mode"][modelName]
	if hasBillingMode && billingMode == billing_setting.BillingModeTieredExpr {
		exprValue, hasExpr := pricingValues["billing_setting.billing_expr"][modelName]
		expr, validExpr := exprValue.(string)
		if !hasExpr || !validExpr || strings.TrimSpace(expr) == "" {
			return nil, errors.New("表达式计费模式缺少完整计费表达式")
		}
		if err := billing_setting.SmokeTestExpr(expr); err != nil {
			return nil, fmt.Errorf("计费表达式校验失败: %w", err)
		}

		template := map[string]any{
			"billing_setting.billing_mode": billing_setting.BillingModeTieredExpr,
			"billing_setting.billing_expr": expr,
		}
		for _, key := range append([]string{"ModelPrice"}, modelPricingRatioOptionKeys...) {
			value, exists := pricingValues[key][modelName]
			if !exists {
				continue
			}
			if err := validateStoredPricingNumber(key, value); err != nil {
				return nil, err
			}
			template[key] = value
		}
		return template, nil
	}

	if value, exists := pricingValues["ModelPrice"][modelName]; exists {
		if err := validateStoredPricingNumber("ModelPrice", value); err != nil {
			return nil, err
		}
		return map[string]any{"ModelPrice": value}, nil
	}
	if value, exists := pricingValues["ModelRatio"][modelName]; exists {
		if err := validateStoredPricingNumber("ModelRatio", value); err != nil {
			return nil, err
		}
		template := map[string]any{"ModelRatio": value}
		for _, key := range modelPricingRatioOptionKeys[1:] {
			value, exists := pricingValues[key][modelName]
			if !exists {
				continue
			}
			if err := validateStoredPricingNumber(key, value); err != nil {
				return nil, err
			}
			template[key] = value
		}
		return template, nil
	}
	return nil, errors.New("缺少明确的按次价格、按量倍率或完整表达式配置")
}

func validateStoredPricingNumber(field string, value any) error {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return fmt.Errorf("%s 必须是非负有限数字", field)
	}
	return nil
}

// GetModelPricingRuntimeSnapshot 在同一读锁内收集模型的完整运行时计费配置，
// 保证请求不会在批量发布期间组合新旧两个版本的字段。
func GetModelPricingRuntimeSnapshot(modelName string) ModelPricingRuntimeSnapshot {
	modelPricingRuntimeRWMutex.RLock()
	defer modelPricingRuntimeRWMutex.RUnlock()
	modelPrice, hasModelPrice := ratio_setting.GetModelPrice(modelName, false)
	modelRatio, hasModelRatio, matchedModelName := ratio_setting.GetModelRatio(modelName)
	cacheRatio, hasCacheRatio := ratio_setting.GetCacheRatio(modelName)
	createCacheRatio, hasCreateCacheRatio := ratio_setting.GetCreateCacheRatio(modelName)
	imageRatio, hasImageRatio := ratio_setting.GetImageRatio(modelName)
	billingMode, billingExpr, hasBillingExpr := billing_setting.GetBillingConfig(modelName)
	return ModelPricingRuntimeSnapshot{
		ModelPrice:              modelPrice,
		HasModelPrice:           hasModelPrice,
		ModelRatio:              modelRatio,
		HasModelRatio:           hasModelRatio,
		MatchedModelName:        matchedModelName,
		CompletionRatio:         ratio_setting.GetCompletionRatio(modelName),
		CacheRatio:              cacheRatio,
		HasCacheRatio:           hasCacheRatio,
		CreateCacheRatio:        createCacheRatio,
		HasCreateCacheRatio:     hasCreateCacheRatio,
		ImageRatio:              imageRatio,
		HasImageRatio:           hasImageRatio,
		AudioRatio:              ratio_setting.GetAudioRatio(modelName),
		HasAudioRatio:           ratio_setting.ContainsAudioRatio(modelName),
		AudioCompletionRatio:    ratio_setting.GetAudioCompletionRatio(modelName),
		HasAudioCompletionRatio: ratio_setting.ContainsAudioCompletionRatio(modelName),
		BillingMode:             billingMode,
		BillingExpr:             billingExpr,
		HasBillingExpr:          hasBillingExpr,
	}
}

// GetPricingSyncRuntimeData 返回同一运行时版本的上游价格同步数据。
func GetPricingSyncRuntimeData() map[string]any {
	modelPricingRuntimeRWMutex.RLock()
	defer modelPricingRuntimeRWMutex.RUnlock()
	data := billing_setting.GetPricingSyncData(map[string]any(ratio_setting.GetExposedData()))
	data["image_ratio"] = ratio_setting.GetImageRatioCopy()
	data["audio_ratio"] = ratio_setting.GetAudioRatioCopy()
	data["audio_completion_ratio"] = ratio_setting.GetAudioCompletionRatioCopy()
	return data
}

// publishModelPricingOptions 先发布后备价格字段，再原子替换表达式计费模式与表达式，
// 避免计费请求在两项配置更新之间观察到不可执行的半套状态。
func publishModelPricingOptions(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	updatePricingLock.Lock()
	modelPricingRuntimeRWMutex.Lock()
	err := func() error {
		for _, key := range modelPricingSyncOptionKeys {
			if key == "billing_setting.billing_mode" || key == "billing_setting.billing_expr" {
				continue
			}
			value, exists := values[key]
			if !exists {
				continue
			}
			if err := updateOptionMapUnlocked(key, value); err != nil {
				return err
			}
		}

		mode, modeChanged := values["billing_setting.billing_mode"]
		expr, exprChanged := values["billing_setting.billing_expr"]
		if modeChanged || exprChanged {
			common.OptionMapRWMutex.RLock()
			if !modeChanged {
				mode = common.OptionMap["billing_setting.billing_mode"]
			}
			if !exprChanged {
				expr = common.OptionMap["billing_setting.billing_expr"]
			}
			common.OptionMapRWMutex.RUnlock()
			if err := billing_setting.UpdateBillingConfig(mode, expr); err != nil {
				return err
			}
			common.OptionMapRWMutex.Lock()
			common.OptionMap["billing_setting.billing_mode"] = mode
			common.OptionMap["billing_setting.billing_expr"] = expr
			common.OptionMapRWMutex.Unlock()
		}
		invalidatePricingCacheUnlocked()
		return nil
	}()
	modelPricingRuntimeRWMutex.Unlock()
	updatePricingLock.Unlock()
	if err != nil {
		return err
	}
	ratio_setting.InvalidateExposedDataCache()
	return nil
}

func ApplyModelPricingSync(resolutions map[string]map[string]any) (ModelPricingSyncResult, error) {
	modelPricingMutationMutex.Lock()
	defer modelPricingMutationMutex.Unlock()
	result := ModelPricingSyncResult{}
	updatedValues := make(map[string]string)
	keys := append([]string{ModelPricingLocksOptionKey}, modelPricingSyncOptionKeys...)

	err := DB.Transaction(func(tx *gorm.DB) error {
		options, err := getOptionsForUpdate(tx, keys)
		if err != nil {
			return err
		}
		lockOption := options[ModelPricingLocksOptionKey]
		locks, err := parseModelPricingLocks(lockOption.Value)
		if err != nil {
			return err
		}

		pricingValues := make(map[string]map[string]any, len(modelPricingSyncOptionKeys))
		for _, key := range modelPricingSyncOptionKeys {
			option := options[key]
			parsed, err := parsePricingOptionValue(option.Value)
			if err != nil {
				return err
			}
			options[key] = option
			pricingValues[key] = parsed
		}

		for modelName, fields := range resolutions {
			if locks[modelName] {
				result.IgnoredLockedModels = append(result.IgnoredLockedModels, modelName)
				continue
			}
			hasPrice := false
			hasRatio := false
			for field := range fields {
				if field == "model_price" {
					hasPrice = true
				}
				for _, ratioKey := range modelPricingRatioOptionKeys {
					if modelPricingSyncFieldOptionKeys[field] == ratioKey {
						hasRatio = true
						break
					}
				}
			}
			if hasPrice {
				for _, key := range modelPricingRatioOptionKeys {
					delete(pricingValues[key], modelName)
				}
			}
			if hasRatio {
				delete(pricingValues["ModelPrice"], modelName)
			}
			for field, value := range fields {
				pricingValues[modelPricingSyncFieldOptionKeys[field]][modelName] = value
			}
			result.AppliedModels = append(result.AppliedModels, modelName)
		}

		for _, key := range modelPricingSyncOptionKeys {
			data, err := common.Marshal(pricingValues[key])
			if err != nil {
				return err
			}
			option := options[key]
			option.Value = string(data)
			if err := tx.Save(&option).Error; err != nil {
				return err
			}
			updatedValues[key] = option.Value
		}
		return nil
	})
	if err != nil {
		return ModelPricingSyncResult{}, err
	}

	if err := publishModelPricingOptions(updatedValues); err != nil {
		return ModelPricingSyncResult{}, err
	}
	sort.Strings(result.AppliedModels)
	sort.Strings(result.IgnoredLockedModels)
	return result, nil
}
