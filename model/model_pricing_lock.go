package model

import (
	"errors"
	"sort"
	"sync"

	"github.com/QuantumNous/new-api/common"
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

type ModelPricingSyncResult struct {
	AppliedModels       []string
	IgnoredLockedModels []string
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
	if key == ModelPricingLocksOptionKey {
		return "{}"
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
	modelPricingMutationMutex.Lock()
	defer modelPricingMutationMutex.Unlock()
	var serialized string
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := ensureOptionRows(tx, []string{ModelPricingLocksOptionKey}); err != nil {
			return err
		}
		option, err := getOptionForUpdate(tx, ModelPricingLocksOptionKey)
		if err != nil {
			return err
		}
		locks, err := parseModelPricingLocks(option.Value)
		if err != nil {
			return err
		}
		if locked {
			locks[modelName] = true
		} else {
			delete(locks, modelName)
		}
		serialized, err = marshalModelPricingLocks(locks)
		if err != nil {
			return err
		}
		option.Value = serialized
		return tx.Save(&option).Error
	})
	if err != nil {
		return nil, err
	}
	if err := updateOptionMap(ModelPricingLocksOptionKey, serialized); err != nil {
		return nil, err
	}
	locks, err := parseModelPricingLocks(serialized)
	if err != nil {
		return nil, err
	}
	return sortedLockedModelNames(locks), nil
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

func ApplyModelPricingSync(resolutions map[string]map[string]any) (ModelPricingSyncResult, error) {
	modelPricingMutationMutex.Lock()
	defer modelPricingMutationMutex.Unlock()
	result := ModelPricingSyncResult{}
	updatedValues := make(map[string]string)
	keys := append([]string{ModelPricingLocksOptionKey}, modelPricingSyncOptionKeys...)

	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := ensureOptionRows(tx, keys); err != nil {
			return err
		}
		lockOption, err := getOptionForUpdate(tx, ModelPricingLocksOptionKey)
		if err != nil {
			return err
		}
		locks, err := parseModelPricingLocks(lockOption.Value)
		if err != nil {
			return err
		}

		options := make(map[string]Option, len(modelPricingSyncOptionKeys))
		pricingValues := make(map[string]map[string]any, len(modelPricingSyncOptionKeys))
		for _, key := range modelPricingSyncOptionKeys {
			option, err := getOptionForUpdate(tx, key)
			if err != nil {
				return err
			}
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

	for _, key := range modelPricingSyncOptionKeys {
		if err := updateOptionMap(key, updatedValues[key]); err != nil {
			return ModelPricingSyncResult{}, err
		}
	}
	sort.Strings(result.AppliedModels)
	sort.Strings(result.IgnoredLockedModels)
	return result, nil
}
