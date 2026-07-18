package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Channel struct {
	Id                 int     `json:"id"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"not null"`
	OpenAIOrganization *string `json:"openai_organization"`
	TestModel          *string `json:"test_model"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index"`
	Weight             *uint   `json:"weight" gorm:"default:0"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	TestTime           int64   `json:"test_time" gorm:"bigint"`
	ResponseTime       int     `json:"response_time"` // in milliseconds
	BaseURL            *string `json:"base_url" gorm:"column:base_url;default:''"`
	Other              string  `json:"other"`
	Balance            float64 `json:"balance"` // in USD
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"bigint"`
	Models             string  `json:"models"`
	Group              string  `json:"group" gorm:"type:varchar(64);default:'default'"`
	UsedQuota          int64   `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       *string `json:"model_mapping" gorm:"type:text"`
	//MaxInputTokens     *int    `json:"max_input_tokens" gorm:"default:0"`
	StatusCodeMapping *string `json:"status_code_mapping" gorm:"type:varchar(1024);default:''"`
	Priority          *int64  `json:"priority" gorm:"bigint;default:0"`
	AutoBan           *int    `json:"auto_ban" gorm:"default:1"`
	OtherInfo         string  `json:"other_info"`
	Tag               *string `json:"tag" gorm:"index"`
	Setting           *string `json:"setting" gorm:"type:text"` // 渠道额外设置
	ParamOverride     *string `json:"param_override" gorm:"type:text"`
	HeaderOverride    *string `json:"header_override" gorm:"type:text"`
	Remark            *string `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	// add after v0.8.5
	ChannelInfo ChannelInfo `json:"channel_info" gorm:"type:json"`

	OtherSettings string `json:"settings" gorm:"column:settings"` // 其他设置，存储azure版本等不需要检索的信息，详见dto.ChannelOtherSettings

	// cache info
	Keys []string `json:"-" gorm:"-"`
}

type ChannelInfo struct {
	IsMultiKey                 bool                  `json:"is_multi_key"`                             // 是否多Key模式
	MultiKeySize               int                   `json:"multi_key_size"`                           // 多Key模式下的Key数量
	MultiKeyStatusList         map[int]int           `json:"multi_key_status_list"`                    // key状态列表，key index -> status
	MultiKeyDisabledReason     map[int]string        `json:"multi_key_disabled_reason,omitempty"`      // key禁用原因列表，key index -> reason
	MultiKeyDisabledTime       map[int]int64         `json:"multi_key_disabled_time,omitempty"`        // key禁用时间列表，key index -> time
	MultiKeyDisabledStatusCode map[int]int           `json:"multi_key_disabled_status_code,omitempty"` // key自动禁用状态码，key index -> HTTP status code
	MultiKeyDisabledGeneration map[int]int64         `json:"multi_key_disabled_generation,omitempty"`  // key自动禁用代次，防止旧测试结果恢复新的禁用事件
	MultiKeyGenerationCounter  int64                 `json:"multi_key_generation_counter,omitempty"`   // 渠道级单调代次计数器，密钥元数据清理后仍保留
	MultiKeyPollingIndex       int                   `json:"multi_key_polling_index"`                  // 多Key模式下轮询的key索引
	MultiKeyTestIndex          int                   `json:"multi_key_test_index"`                     // 自动禁用密钥健康检查的轮转起点
	MultiKeyMode               constant.MultiKeyMode `json:"multi_key_mode"`
}

type ChannelSortOptions struct {
	SortBy    string
	SortOrder string
	IDSort    bool
}

var channelSortColumns = map[string]string{
	"id":            "id",
	"name":          "name",
	"priority":      "priority",
	"balance":       "balance",
	"response_time": "response_time",
	"test_time":     "test_time",
}

func NewChannelSortOptions(sortBy string, sortOrder string, idSort bool) ChannelSortOptions {
	normalizedSortBy := strings.ToLower(strings.TrimSpace(sortBy))
	normalizedSortOrder := strings.ToLower(strings.TrimSpace(sortOrder))
	if _, ok := channelSortColumns[normalizedSortBy]; !ok {
		normalizedSortBy = ""
		normalizedSortOrder = ""
	} else if normalizedSortOrder != "asc" {
		normalizedSortOrder = "desc"
	}

	return ChannelSortOptions{
		SortBy:    normalizedSortBy,
		SortOrder: normalizedSortOrder,
		IDSort:    idSort,
	}
}

func (options ChannelSortOptions) Apply(query *gorm.DB) *gorm.DB {
	if columnName, ok := channelSortColumns[options.SortBy]; ok {
		return query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: columnName},
			Desc:   options.SortOrder != "asc",
		})
	}
	if options.IDSort {
		return query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: "id"},
			Desc:   true,
		})
	}
	return query.Order(clause.OrderByColumn{
		Column: clause.Column{Name: "priority"},
		Desc:   true,
	})
}

func resolveChannelSortOptions(idSort bool, sortOptions []ChannelSortOptions) ChannelSortOptions {
	if len(sortOptions) == 0 {
		return NewChannelSortOptions("", "", idSort)
	}
	options := sortOptions[0]
	options.IDSort = options.IDSort || idSort
	return options
}

func NormalizeChannelGroupFilter(group string) string {
	group = strings.TrimSpace(group)
	if group == "" || strings.EqualFold(group, "all") || strings.EqualFold(group, "null") {
		return ""
	}
	return group
}

func channelGroupFilterCondition() string {
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		return `CONCAT(',', ` + commonGroupCol + `, ',') LIKE ? ESCAPE '!'`
	}
	return `(',' || ` + commonGroupCol + ` || ',') LIKE ? ESCAPE '!'`
}

func channelGroupFilterPattern(group string) string {
	group = strings.NewReplacer(
		"!", "!!",
		"%", "!%",
		"_", "!_",
	).Replace(group)
	return "%," + group + ",%"
}

func ApplyChannelGroupFilter(query *gorm.DB, group string) *gorm.DB {
	group = NormalizeChannelGroupFilter(group)
	if group == "" {
		return query
	}
	return query.Where(channelGroupFilterCondition(), channelGroupFilterPattern(group))
}

// Value implements driver.Valuer interface
func (c ChannelInfo) Value() (driver.Value, error) {
	return common.Marshal(&c)
}

// Scan implements sql.Scanner interface
func (c *ChannelInfo) Scan(value interface{}) error {
	bytesValue, _ := value.([]byte)
	return common.Unmarshal(bytesValue, c)
}

func (channel *Channel) GetKeys() []string {
	if channel.Key == "" {
		return []string{}
	}
	if len(channel.Keys) > 0 {
		return channel.Keys
	}
	trimmed := strings.TrimSpace(channel.Key)
	// If the key starts with '[', try to parse it as a JSON array (e.g., for Vertex AI scenarios)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
			res := make([]string, len(arr))
			for i, v := range arr {
				res[i] = string(v)
			}
			return res
		}
	}
	// Otherwise, fall back to splitting by newline
	keys := strings.Split(strings.Trim(channel.Key, "\n"), "\n")
	return keys
}

// GetMultiKeyDisabledStatusCode 返回密钥自动禁用时记录的 HTTP 状态码。
// 旧数据没有结构化字段时，兼容解析禁用原因中的稳定 status_code= 前缀。
func (channel *Channel) GetMultiKeyDisabledStatusCode(index int) int {
	if channel == nil || index < 0 {
		return 0
	}
	if code := channel.ChannelInfo.MultiKeyDisabledStatusCode[index]; code >= 100 && code <= 599 {
		return code
	}
	reason := strings.TrimSpace(channel.ChannelInfo.MultiKeyDisabledReason[index])
	const prefix = "status_code="
	if !strings.HasPrefix(reason, prefix) {
		return 0
	}
	value := strings.TrimPrefix(reason, prefix)
	if separator := strings.IndexByte(value, ','); separator >= 0 {
		value = value[:separator]
	}
	code, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || code < 100 || code > 599 {
		return 0
	}
	return code
}

func (channel *Channel) GetNextEnabledKey() (string, int, *types.NewAPIError) {
	// If not in multi-key mode, return the original key string directly.
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key, 0, nil
	}

	// Obtain all keys (split by \n)
	keys := channel.GetKeys()
	if len(keys) == 0 {
		// No keys available, return error, should disable the channel
		return "", 0, types.NewError(errors.New("no keys available"), types.ErrorCodeChannelNoAvailableKey)
	}

	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	statusList := channel.ChannelInfo.MultiKeyStatusList
	// helper to get key status, default to enabled when missing
	getStatus := func(idx int) int {
		if statusList == nil {
			return common.ChannelStatusEnabled
		}
		if status, ok := statusList[idx]; ok {
			return status
		}
		return common.ChannelStatusEnabled
	}

	// Collect indexes of enabled keys
	enabledIdx := make([]int, 0, len(keys))
	for i := range keys {
		if getStatus(i) == common.ChannelStatusEnabled {
			enabledIdx = append(enabledIdx, i)
		}
	}
	// If no specific status list or none enabled, return an explicit error so caller can
	// properly handle a channel with no available keys (e.g. mark channel disabled).
	// Returning the first key here caused requests to keep using an already-disabled key.
	if len(enabledIdx) == 0 {
		return "", 0, types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
	}

	switch channel.ChannelInfo.MultiKeyMode {
	case constant.MultiKeyModeRandom:
		// Randomly pick one enabled key
		selectedIdx := enabledIdx[rand.Intn(len(enabledIdx))]
		return keys[selectedIdx], selectedIdx, nil
	case constant.MultiKeyModePolling:
		// Use channel-specific lock to ensure thread-safe polling

		channelInfo, err := CacheGetChannelInfo(channel.Id)
		if err != nil {
			return "", 0, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		defer func() {
			if common.DebugEnabled {
				logger.LogDebug(nil, "channel %d polling index: %d", channel.Id, channel.ChannelInfo.MultiKeyPollingIndex)
			}
			if !common.MemoryCacheEnabled {
				_ = channel.SaveChannelInfo()
			} else {
				// CacheUpdateChannel(channel)
			}
		}()
		// Start from the saved polling index and look for the next enabled key
		start := channelInfo.MultiKeyPollingIndex
		if start < 0 || start >= len(keys) {
			start = 0
		}
		for i := 0; i < len(keys); i++ {
			idx := (start + i) % len(keys)
			if getStatus(idx) == common.ChannelStatusEnabled {
				// update polling index for next call (point to the next position)
				channel.ChannelInfo.MultiKeyPollingIndex = (idx + 1) % len(keys)
				return keys[idx], idx, nil
			}
		}
		// Fallback – should not happen, but return first enabled key
		return keys[enabledIdx[0]], enabledIdx[0], nil
	default:
		// Unknown mode, default to first enabled key (or original key string)
		return keys[enabledIdx[0]], enabledIdx[0], nil
	}
}

// GetMultiKeyByIndex 返回指定索引的多密钥。
// 该方法仅用于需要绕过启用状态进行健康检查的内部流程，正常请求仍应使用 GetNextEnabledKey。
func (channel *Channel) GetMultiKeyByIndex(index int) (string, *types.NewAPIError) {
	if !channel.ChannelInfo.IsMultiKey {
		return "", types.NewError(errors.New("channel is not in multi-key mode"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	keys := channel.GetKeys()
	if index < 0 || index >= len(keys) {
		return "", types.NewError(errors.New("multi-key index is out of range"), types.ErrorCodeChannelNoAvailableKey, types.ErrOptionWithSkipRetry())
	}
	if strings.TrimSpace(keys[index]) == "" {
		return "", types.NewError(errors.New("multi-key is empty"), types.ErrorCodeChannelNoAvailableKey, types.ErrOptionWithSkipRetry())
	}
	return keys[index], nil
}

func (channel *Channel) SaveChannelInfo() error {
	return DB.Model(channel).Update("channel_info", channel.ChannelInfo).Error
}

func (channel *Channel) GetModels() []string {
	if channel.Models == "" {
		return []string{}
	}
	return strings.Split(strings.Trim(channel.Models, ","), ",")
}

func (channel *Channel) GetGroups() []string {
	if channel.Group == "" {
		return []string{}
	}
	groups := strings.Split(strings.Trim(channel.Group, ","), ",")
	for i, group := range groups {
		groups[i] = strings.TrimSpace(group)
	}
	return groups
}

func (channel *Channel) GetOtherInfo() map[string]interface{} {
	otherInfo := make(map[string]interface{})
	if channel.OtherInfo != "" {
		err := common.Unmarshal([]byte(channel.OtherInfo), &otherInfo)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		}
	}
	return otherInfo
}

func (channel *Channel) SetOtherInfo(otherInfo map[string]interface{}) {
	otherInfoBytes, err := json.Marshal(otherInfo)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		return
	}
	channel.OtherInfo = string(otherInfoBytes)
}

func (channel *Channel) GetTag() string {
	if channel.Tag == nil {
		return ""
	}
	return *channel.Tag
}

func (channel *Channel) SetTag(tag string) {
	channel.Tag = &tag
}

func (channel *Channel) GetAutoBan() bool {
	if channel.AutoBan == nil {
		return false
	}
	return *channel.AutoBan == 1
}

func (channel *Channel) Save() error {
	return DB.Save(channel).Error
}

func (channel *Channel) SaveWithoutKey() error {
	if channel.Id == 0 {
		return errors.New("channel ID is 0")
	}
	return DB.Omit("key").Save(channel).Error
}

func GetAllChannels(startIdx int, num int, selectAll bool, idSort bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	var err error
	order := resolveChannelSortOptions(idSort, sortOptions)
	if selectAll {
		err = order.Apply(DB).Find(&channels).Error
	} else {
		err = order.Apply(DB).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	return channels, err
}

func GetChannelsByTag(tag string, idSort bool, selectAll bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	order := resolveChannelSortOptions(idSort, sortOptions)
	query := order.Apply(DB.Where("tag = ?", tag))
	if !selectAll {
		query = query.Omit("key")
	}
	err := query.Find(&channels).Error
	return channels, err
}

func SearchChannels(keyword string, group string, model string, idSort bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		baseURLCol = `"base_url"`
	}

	order := resolveChannelSortOptions(idSort, sortOptions)

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	whereClause := "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + model + "%"}
	baseQuery = ApplyChannelGroupFilter(baseQuery.Where(whereClause, args...), group)

	// 执行查询
	err := order.Apply(baseQuery).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	return channels, nil
}

func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := &Channel{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(channel, "id = ?", id).Error
	} else {
		err = DB.Omit("key").First(channel, "id = ?", id).Error
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func BatchInsertChannels(channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	for _, chunk := range lo.Chunk(channels, 50) {
		if err := tx.Create(&chunk).Error; err != nil {
			tx.Rollback()
			return err
		}
		for _, channel_ := range chunk {
			if err := channel_.AddAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit().Error
}

func BatchDeleteChannels(ids []int) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// 使用事务 分批删除channel表和abilities表
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	var deletedCount int64
	for _, chunk := range lo.Chunk(ids, 200) {
		result := tx.Where("id in (?)", chunk).Delete(&Channel{})
		if result.Error != nil {
			tx.Rollback()
			return 0, result.Error
		}
		deletedCount += result.RowsAffected
		if err := tx.Where("channel_id in (?)", chunk).Delete(&Ability{}).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return deletedCount, nil
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetWeight() int {
	if channel.Weight == nil {
		return 0
	}
	return int(*channel.Weight)
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL == nil {
		return ""
	}
	url := *channel.BaseURL
	if url == "" {
		url = constant.ChannelBaseURLs[channel.Type]
	}
	return url
}

func (channel *Channel) GetModelMapping() string {
	if channel.ModelMapping == nil {
		return ""
	}
	return *channel.ModelMapping
}

func (channel *Channel) GetStatusCodeMapping() string {
	if channel.StatusCodeMapping == nil {
		return ""
	}
	return *channel.StatusCodeMapping
}

func (channel *Channel) Insert() error {
	var err error
	err = DB.Create(channel).Error
	if err != nil {
		return err
	}
	err = channel.AddAbilities(nil)
	return err
}

func (channel *Channel) Update() error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current Channel
		if err := lockForUpdate(tx).Where("id = ?", channel.Id).First(&current).Error; err != nil {
			return err
		}
		if current.ChannelInfo.IsMultiKey &&
			current.ChannelInfo.MultiKeyGenerationCounter != channel.ChannelInfo.MultiKeyGenerationCounter {
			return errors.New("渠道密钥状态已变化，请刷新后重试")
		}
		if current.ChannelInfo.IsMultiKey || channel.ChannelInfo.IsMultiKey {
			currentGeneration := current.ChannelInfo.MultiKeyGenerationCounter
			for _, generation := range current.ChannelInfo.MultiKeyDisabledGeneration {
				if generation > currentGeneration {
					currentGeneration = generation
				}
			}
			channel.ChannelInfo.MultiKeyGenerationCounter = currentGeneration + 1
		}

		// 多密钥渠道更新时基于锁定后的最新密钥计算长度，避免旧快照覆盖并发状态。
		if channel.ChannelInfo.IsMultiKey {
			keyStr := channel.Key
			if keyStr == "" {
				keyStr = current.Key
			}
			keys := []string{}
			if keyStr != "" {
				trimmed := strings.TrimSpace(keyStr)
				if strings.HasPrefix(trimmed, "[") {
					var arr []json.RawMessage
					if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
						keys = make([]string, len(arr))
						for i, value := range arr {
							keys[i] = string(value)
						}
					}
				}
				if len(keys) == 0 {
					keys = strings.Split(strings.Trim(keyStr, "\n"), "\n")
				}
			}
			channel.ChannelInfo.MultiKeySize = len(keys)
			if channel.ChannelInfo.MultiKeyStatusList != nil {
				for idx := range channel.ChannelInfo.MultiKeyStatusList {
					if idx >= channel.ChannelInfo.MultiKeySize {
						delete(channel.ChannelInfo.MultiKeyStatusList, idx)
					}
				}
			}
			if channel.ChannelInfo.MultiKeyDisabledStatusCode != nil {
				for idx := range channel.ChannelInfo.MultiKeyDisabledStatusCode {
					if idx >= channel.ChannelInfo.MultiKeySize {
						delete(channel.ChannelInfo.MultiKeyDisabledStatusCode, idx)
					}
				}
			}
			if channel.ChannelInfo.MultiKeyDisabledGeneration != nil {
				for idx := range channel.ChannelInfo.MultiKeyDisabledGeneration {
					if idx >= channel.ChannelInfo.MultiKeySize {
						delete(channel.ChannelInfo.MultiKeyDisabledGeneration, idx)
					}
				}
			}
		}
		return tx.Model(channel).Updates(channel).Error
	})
	if err != nil {
		return err
	}
	DB.Model(channel).First(channel, "id = ?", channel.Id)
	err = channel.UpdateAbilities(nil)
	return err
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     common.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update response time: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) UpdateBalance(balance float64) {
	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
		BalanceUpdatedTime: common.GetTimestamp(),
		Balance:            balance,
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update balance: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) Delete() error {
	var err error
	err = DB.Delete(channel).Error
	if err != nil {
		return err
	}
	err = channel.DeleteAbilities()
	return err
}

// channelPollingLocks stores locks for each channel.id to ensure thread-safe polling
var channelPollingLocks sync.Map

// GetChannelPollingLock returns or creates a mutex for the given channel ID
func GetChannelPollingLock(channelId int) *sync.Mutex {
	if lock, exists := channelPollingLocks.Load(channelId); exists {
		return lock.(*sync.Mutex)
	}
	// Create new lock for this channel
	newLock := &sync.Mutex{}
	actual, _ := channelPollingLocks.LoadOrStore(channelId, newLock)
	return actual.(*sync.Mutex)
}

// CleanupChannelPollingLocks removes locks for channels that no longer exist
// This is optional and can be called periodically to prevent memory leaks
func CleanupChannelPollingLocks() {
	var activeChannelIds []int
	DB.Model(&Channel{}).Pluck("id", &activeChannelIds)

	activeChannelSet := make(map[int]bool)
	for _, id := range activeChannelIds {
		activeChannelSet[id] = true
	}

	channelPollingLocks.Range(func(key, value interface{}) bool {
		channelId := key.(int)
		if !activeChannelSet[channelId] {
			channelPollingLocks.Delete(channelId)
		}
		return true
	})
}

func handlerMultiKeyUpdate(channel *Channel, usingKey string, status int, reason string, disabledStatusCode int) bool {
	if status == common.ChannelStatusAutoDisabled && channel.Status == common.ChannelStatusManuallyDisabled {
		return false
	}
	keys := channel.GetKeys()
	if len(keys) == 0 {
		if channel.Status == status {
			return false
		}
		channel.Status = status
		return true
	} else {
		keyIndex := -1
		for i, key := range keys {
			if key == usingKey {
				keyIndex = i
				break
			}
		}
		if keyIndex < 0 {
			if usingKey != "" {
				common.SysLog(fmt.Sprintf("failed to update multi-key status: channel_id=%d, using key not found", channel.Id))
				return false
			}
			if channel.Status == status {
				return false
			}
			channel.Status = status
			info := channel.GetOtherInfo()
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			return true
		}
		currentKeyStatus := common.ChannelStatusEnabled
		if savedStatus, exists := channel.ChannelInfo.MultiKeyStatusList[keyIndex]; exists {
			currentKeyStatus = savedStatus
		}
		// 在途请求的自动禁用结果不得覆盖管理员刚设置的手动禁用状态。
		if status == common.ChannelStatusAutoDisabled && currentKeyStatus == common.ChannelStatusManuallyDisabled {
			return false
		}
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if status == common.ChannelStatusEnabled {
			delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
			if channel.ChannelInfo.MultiKeyDisabledReason != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledReason, keyIndex)
			}
			if channel.ChannelInfo.MultiKeyDisabledTime != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledTime, keyIndex)
			}
			if channel.ChannelInfo.MultiKeyDisabledStatusCode != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledStatusCode, keyIndex)
			}
			if channel.ChannelInfo.MultiKeyDisabledGeneration != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledGeneration, keyIndex)
			}
		} else {
			channel.ChannelInfo.MultiKeyStatusList[keyIndex] = status
			if channel.ChannelInfo.MultiKeyDisabledReason == nil {
				channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
			}
			if channel.ChannelInfo.MultiKeyDisabledTime == nil {
				channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
			}
			channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = reason
			channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
			if status == common.ChannelStatusAutoDisabled && disabledStatusCode >= 100 && disabledStatusCode <= 599 {
				if channel.ChannelInfo.MultiKeyDisabledStatusCode == nil {
					channel.ChannelInfo.MultiKeyDisabledStatusCode = make(map[int]int)
				}
				channel.ChannelInfo.MultiKeyDisabledStatusCode[keyIndex] = disabledStatusCode
			} else if channel.ChannelInfo.MultiKeyDisabledStatusCode != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledStatusCode, keyIndex)
			}
			if status == common.ChannelStatusAutoDisabled {
				if channel.ChannelInfo.MultiKeyDisabledGeneration == nil {
					channel.ChannelInfo.MultiKeyDisabledGeneration = make(map[int]int64)
				}
				for _, generation := range channel.ChannelInfo.MultiKeyDisabledGeneration {
					if generation > channel.ChannelInfo.MultiKeyGenerationCounter {
						channel.ChannelInfo.MultiKeyGenerationCounter = generation
					}
				}
				channel.ChannelInfo.MultiKeyGenerationCounter++
				channel.ChannelInfo.MultiKeyDisabledGeneration[keyIndex] = channel.ChannelInfo.MultiKeyGenerationCounter
			} else if channel.ChannelInfo.MultiKeyDisabledGeneration != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledGeneration, keyIndex)
			}
		}
		if !hasEnabledMultiKey(keys, channel.ChannelInfo.MultiKeyStatusList) {
			channel.Status = common.ChannelStatusAutoDisabled
			info := channel.GetOtherInfo()
			info["status_reason"] = "All keys are disabled"
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
		} else if status == common.ChannelStatusEnabled {
			channel.Status = common.ChannelStatusEnabled
		}
	}
	return true
}

func hasEnabledMultiKey(keys []string, statusList map[int]int) bool {
	for i := range keys {
		if statusList == nil {
			return true
		}
		status, ok := statusList[i]
		if !ok || status == common.ChannelStatusEnabled {
			return true
		}
	}
	return false
}

// MultiKeyRecoveryResult 描述一次多密钥健康检查写回的结果。
type MultiKeyRecoveryResult struct {
	Recovered      int
	ChannelEnabled bool
}

// MultiKeyRecoveryCandidate 保存发起网络测试前的自动禁用事件快照。
// 恢复时必须逐项比对，避免旧测试结果覆盖测试期间发生的新一轮自动禁用。
type MultiKeyRecoveryCandidate struct {
	Key                string
	DisabledReason     string
	DisabledTime       int64
	DisabledStatusCode int
	DisabledGeneration int64
}

// RecoverAutoDisabledMultiKeys 按测试时记录的索引和密钥文本恢复自动禁用密钥。
// 写回前重新锁定并校验当前渠道，避免覆盖测试期间发生的密钥编辑或手动禁用操作。
func RecoverAutoDisabledMultiKeys(channelID int, candidates map[int]MultiKeyRecoveryCandidate) (MultiKeyRecoveryResult, error) {
	result := MultiKeyRecoveryResult{}
	if len(candidates) == 0 {
		return result, nil
	}

	pollingLock := GetChannelPollingLock(channelID)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	err := DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := lockForUpdate(tx).Where("id = ?", channelID).First(&channel).Error; err != nil {
			return err
		}
		if !channel.ChannelInfo.IsMultiKey || channel.Status == common.ChannelStatusManuallyDisabled {
			return nil
		}

		keys := channel.GetKeys()
		statusList := channel.ChannelInfo.MultiKeyStatusList
		for index, candidate := range candidates {
			if index < 0 || index >= len(keys) || keys[index] != candidate.Key {
				continue
			}
			if statusList == nil || statusList[index] != common.ChannelStatusAutoDisabled {
				continue
			}
			if channel.ChannelInfo.MultiKeyDisabledReason[index] != candidate.DisabledReason ||
				channel.ChannelInfo.MultiKeyDisabledTime[index] != candidate.DisabledTime ||
				channel.ChannelInfo.MultiKeyDisabledStatusCode[index] != candidate.DisabledStatusCode ||
				channel.ChannelInfo.MultiKeyDisabledGeneration[index] != candidate.DisabledGeneration {
				continue
			}

			delete(statusList, index)
			if channel.ChannelInfo.MultiKeyDisabledReason != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledReason, index)
			}
			if channel.ChannelInfo.MultiKeyDisabledTime != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledTime, index)
			}
			if channel.ChannelInfo.MultiKeyDisabledStatusCode != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledStatusCode, index)
			}
			if channel.ChannelInfo.MultiKeyDisabledGeneration != nil {
				delete(channel.ChannelInfo.MultiKeyDisabledGeneration, index)
			}
			result.Recovered++
		}
		if result.Recovered == 0 {
			return nil
		}

		if len(statusList) == 0 {
			channel.ChannelInfo.MultiKeyStatusList = nil
		}
		if len(channel.ChannelInfo.MultiKeyDisabledReason) == 0 {
			channel.ChannelInfo.MultiKeyDisabledReason = nil
		}
		if len(channel.ChannelInfo.MultiKeyDisabledTime) == 0 {
			channel.ChannelInfo.MultiKeyDisabledTime = nil
		}
		if len(channel.ChannelInfo.MultiKeyDisabledStatusCode) == 0 {
			channel.ChannelInfo.MultiKeyDisabledStatusCode = nil
		}
		if len(channel.ChannelInfo.MultiKeyDisabledGeneration) == 0 {
			channel.ChannelInfo.MultiKeyDisabledGeneration = nil
		}

		beforeStatus := channel.Status
		if channel.Status == common.ChannelStatusAutoDisabled && hasEnabledMultiKey(keys, channel.ChannelInfo.MultiKeyStatusList) {
			channel.Status = common.ChannelStatusEnabled
			result.ChannelEnabled = true
		}
		for _, generation := range channel.ChannelInfo.MultiKeyDisabledGeneration {
			if generation > channel.ChannelInfo.MultiKeyGenerationCounter {
				channel.ChannelInfo.MultiKeyGenerationCounter = generation
			}
		}
		channel.ChannelInfo.MultiKeyGenerationCounter++

		if err := tx.Model(&Channel{}).Where("id = ?", channelID).Updates(map[string]any{
			"status":       channel.Status,
			"channel_info": channel.ChannelInfo,
		}).Error; err != nil {
			return err
		}

		if beforeStatus != channel.Status {
			if err := tx.Model(&Ability{}).Where("channel_id = ?", channelID).Select("enabled").Update("enabled", true).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return MultiKeyRecoveryResult{}, err
	}
	return result, nil
}

// UpdateMultiKeyTestIndex 保存自动禁用密钥健康检查的下一轮起点。
// 使用独立游标，避免批量健康检查改变正常请求的随机或轮询选钥顺序。
func UpdateMultiKeyTestIndex(channelID int, nextIndex int) error {
	pollingLock := GetChannelPollingLock(channelID)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	return DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := lockForUpdate(tx).Where("id = ?", channelID).First(&channel).Error; err != nil {
			return err
		}
		if !channel.ChannelInfo.IsMultiKey || channel.Status == common.ChannelStatusManuallyDisabled {
			return nil
		}
		keys := channel.GetKeys()
		if len(keys) == 0 {
			nextIndex = 0
		} else {
			nextIndex %= len(keys)
			if nextIndex < 0 {
				nextIndex += len(keys)
			}
		}
		channel.ChannelInfo.MultiKeyTestIndex = nextIndex
		return tx.Model(&Channel{}).Where("id = ?", channelID).Update("channel_info", channel.ChannelInfo).Error
	})
}

func UpdateChannelStatus(channelId int, usingKey string, status int, reason string) bool {
	return updateChannelStatus(channelId, usingKey, status, reason, 0)
}

// UpdateChannelStatusWithDisabledStatusCode 在自动禁用密钥时额外保存原始 HTTP 状态码。
func UpdateChannelStatusWithDisabledStatusCode(channelId int, usingKey string, status int, reason string, disabledStatusCode int) bool {
	return updateChannelStatus(channelId, usingKey, status, reason, disabledStatusCode)
}

func updateChannelStatus(channelId int, usingKey string, status int, reason string, disabledStatusCode int) bool {
	pollingLock := GetChannelPollingLock(channelId)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	var channel Channel
	beforeStatus := 0
	changed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ?", channelId).First(&channel).Error; err != nil {
			return err
		}
		beforeStatus = channel.Status

		if channel.ChannelInfo.IsMultiKey {
			beforeGeneration := channel.ChannelInfo.MultiKeyGenerationCounter
			if !handlerMultiKeyUpdate(&channel, usingKey, status, reason, disabledStatusCode) {
				return nil
			}
			if channel.ChannelInfo.MultiKeyGenerationCounter == beforeGeneration {
				channel.ChannelInfo.MultiKeyGenerationCounter++
			}
		} else {
			if channel.Status == status {
				return nil
			}
			info := channel.GetOtherInfo()
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			channel.Status = status
		}

		changed = true
		if err := tx.Omit("key").Save(&channel).Error; err != nil {
			return err
		}
		if beforeStatus != channel.Status {
			return tx.Model(&Ability{}).Where("channel_id = ?", channelId).
				Select("enabled").Update("enabled", channel.Status == common.ChannelStatusEnabled).Error
		}
		return nil
	})
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update channel status: channel_id=%d, status=%d, error=%v", channelId, status, err))
		return false
	}
	if !changed {
		return false
	}

	if common.MemoryCacheEnabled {
		if cached, err := CacheGetChannel(channelId); err == nil && cached != nil {
			channel.ChannelInfo.MultiKeyPollingIndex = cached.ChannelInfo.MultiKeyPollingIndex
		}
		if beforeStatus != channel.Status {
			CacheUpdateChannelStatus(channelId, channel.Status)
		}
		CacheUpdateChannel(&channel)
	}
	return true
}

func EnableChannelByTag(tag string) error {
	err := DB.Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusEnabled).Error
	if err != nil {
		return err
	}
	err = UpdateAbilityStatusByTag(tag, true)
	return err
}

func DisableChannelByTag(tag string) error {
	err := DB.Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusManuallyDisabled).Error
	if err != nil {
		return err
	}
	err = UpdateAbilityStatusByTag(tag, false)
	return err
}

func EditChannelByTag(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) error {
	updateData := Channel{}
	shouldReCreateAbilities := false
	updatedTag := tag
	// 如果 newTag 不为空且不等于 tag，则更新 tag
	if newTag != nil && *newTag != tag {
		updateData.Tag = newTag
		updatedTag = *newTag
	}
	if modelMapping != nil {
		updateData.ModelMapping = modelMapping
	}
	if models != nil && *models != "" {
		shouldReCreateAbilities = true
		updateData.Models = *models
	}
	if group != nil && *group != "" {
		shouldReCreateAbilities = true
		updateData.Group = *group
	}
	if priority != nil {
		updateData.Priority = priority
	}
	if weight != nil {
		updateData.Weight = weight
	}
	if paramOverride != nil {
		updateData.ParamOverride = paramOverride
	}
	if headerOverride != nil {
		updateData.HeaderOverride = headerOverride
	}

	err := DB.Model(&Channel{}).Where("tag = ?", tag).Updates(updateData).Error
	if err != nil {
		return err
	}
	if shouldReCreateAbilities {
		channels, err := GetChannelsByTag(updatedTag, false, false)
		if err == nil {
			for _, channel := range channels {
				err = channel.UpdateAbilities(nil)
				if err != nil {
					common.SysLog(fmt.Sprintf("failed to update abilities: channel_id=%d, tag=%s, error=%v", channel.Id, channel.GetTag(), err))
				}
			}
		}
	} else {
		err := UpdateAbilityByTag(tag, newTag, priority, weight)
		if err != nil {
			return err
		}
	}
	return nil
}

func UpdateChannelUsedQuota(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
		return
	}
	updateChannelUsedQuota(id, quota)
}

func updateChannelUsedQuota(id int, quota int) {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update channel used quota: channel_id=%d, delta_quota=%d, error=%v", id, quota, err))
	}
}

func DeleteChannelByStatus(status int64) (int64, error) {
	result := DB.Where("status = ?", status).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

func DeleteDisabledChannel() (int64, error) {
	result := DB.Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

func GetPaginatedTags(offset int, limit int) ([]*string, error) {
	return GetPaginatedChannelTags(DB.Model(&Channel{}), offset, limit)
}

func GetPaginatedChannelTags(query *gorm.DB, offset int, limit int) ([]*string, error) {
	var tags []*string
	err := query.
		Select("DISTINCT tag").
		Where("tag is not null AND tag != ''").
		Order(clause.OrderByColumn{Column: clause.Column{Name: "tag"}}).
		Offset(offset).
		Limit(limit).
		Find(&tags).Error
	return tags, err
}

func SearchTags(keyword string, group string, model string, idSort bool) ([]*string, error) {
	var tags []*string
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	whereClause := "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + model + "%"}
	baseQuery = ApplyChannelGroupFilter(baseQuery.Where(whereClause, args...), group)

	subQuery := baseQuery.
		Select("tag").
		Where("tag != ''").
		Order(order)

	err := DB.Table("(?) as sub", subQuery).
		Select("DISTINCT tag").
		Find(&tags).Error

	if err != nil {
		return nil, err
	}

	return tags, nil
}

func (channel *Channel) ValidateSettings() error {
	channelParams := &dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), channelParams)
		if err != nil {
			return err
		}
	}
	if _, err := common.ParseProxyURLStrict(channelParams.Proxy); err != nil {
		return fmt.Errorf("invalid channel proxy: %w", err)
	}
	channelOtherSettings := &dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, channelOtherSettings)
		if err != nil {
			return err
		}
	}
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if channelOtherSettings.AdvancedCustom == nil {
			return fmt.Errorf("advanced_custom is required")
		}
	}
	if channelOtherSettings.AdvancedCustom != nil {
		if err := channelOtherSettings.AdvancedCustom.Validate(); err != nil {
			return err
		}
	}
	if channel.Type == constant.ChannelTypeAdvancedCustom && channelOtherSettings.UpstreamModelUpdateCheckEnabled {
		if _, ok := channelOtherSettings.AdvancedCustom.ModelListRoute(); !ok {
			return fmt.Errorf("advanced custom channels require a %s route when upstream model update checks are enabled", dto.AdvancedCustomModelListPath)
		}
	}
	return nil
}

func (channel *Channel) GetSetting() dto.ChannelSettings {
	setting := dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.Setting = nil // 清空设置以避免后续错误
			_ = channel.Save()    // 保存修改
		}
	}
	return setting
}

func (channel *Channel) SetSetting(setting dto.ChannelSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.Setting = common.GetPointer[string](string(settingBytes))
}

func (channel *Channel) GetOtherSettings() dto.ChannelOtherSettings {
	setting := dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.OtherSettings = "{}" // 清空设置以避免后续错误
			_ = channel.Save()           // 保存修改
		}
	}
	return setting
}

func (channel *Channel) SetOtherSettings(setting dto.ChannelOtherSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.OtherSettings = string(settingBytes)
}

func (channel *Channel) GetParamOverride() map[string]interface{} {
	paramOverride := make(map[string]interface{})
	if channel.ParamOverride != nil && *channel.ParamOverride != "" {
		err := common.Unmarshal([]byte(*channel.ParamOverride), &paramOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal param override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return paramOverride
}

func (channel *Channel) GetHeaderOverride() map[string]interface{} {
	headerOverride := make(map[string]interface{})
	if channel.HeaderOverride != nil && *channel.HeaderOverride != "" {
		err := common.Unmarshal([]byte(*channel.HeaderOverride), &headerOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal header override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return headerOverride
}

func GetChannelsByIds(ids []int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("id in (?)", ids).Find(&channels).Error
	return channels, err
}

func BatchSetChannelTag(ids []int, tag *string) error {
	// 开启事务
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	// 更新标签
	err := tx.Model(&Channel{}).Where("id in (?)", ids).Update("tag", tag).Error
	if err != nil {
		tx.Rollback()
		return err
	}

	// update ability status
	channels, err := GetChannelsByIds(ids)
	if err != nil {
		tx.Rollback()
		return err
	}

	for _, channel := range channels {
		err = channel.UpdateAbilities(tx)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	// 提交事务
	return tx.Commit().Error
}

// CountAllChannels returns total channels in DB
func CountAllChannels() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Count(&total).Error
	return total, err
}

// CountAllTags returns number of non-empty distinct tags
func CountAllTags() (int64, error) {
	return CountChannelTags(DB.Model(&Channel{}))
}

func CountChannelTags(query *gorm.DB) (int64, error) {
	var total int64
	err := query.Where("tag is not null AND tag != ''").Distinct("tag").Count(&total).Error
	return total, err
}

// Get channels of specified type with pagination
func GetChannelsByType(startIdx int, num int, idSort bool, channelType int) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := DB.Where("type = ?", channelType).Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	return channels, err
}

// Count channels of specific type
func CountChannelsByType(channelType int) (int64, error) {
	var count int64
	err := DB.Model(&Channel{}).Where("type = ?", channelType).Count(&count).Error
	return count, err
}

// Return map[type]count for all channels
func CountChannelsGroupByType() (map[int64]int64, error) {
	type result struct {
		Type  int64 `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	var results []result
	err := DB.Model(&Channel{}).Select("type, count(*) as count").Group("type").Find(&results).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.Type] = r.Count
	}
	return counts, nil
}
