package billing_setting

import (
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio      = "ratio"
	BillingModeTieredExpr = "tiered_expr"
	BillingModeField      = "billing_mode"
	BillingExprField      = "billing_expr"
)

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr
type BillingSetting struct {
	BillingMode map[string]string `json:"billing_mode"`
	BillingExpr map[string]string `json:"billing_expr"`
}

var billingSetting = BillingSetting{
	BillingMode: make(map[string]string),
	BillingExpr: make(map[string]string),
}

var billingSettingMu sync.RWMutex

func init() {
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	mode, _, _ := GetBillingConfig(model)
	return mode
}

func GetBillingExpr(model string) (string, bool) {
	_, expr, ok := GetBillingConfig(model)
	return expr, ok
}

// GetBillingConfig 在同一读锁内返回模式与表达式，避免调用方读取到跨版本组合。
func GetBillingConfig(model string) (mode string, expr string, hasExpr bool) {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	mode = billingSetting.BillingMode[model]
	if mode == "" {
		mode = BillingModeRatio
	}
	expr, hasExpr = billingSetting.BillingExpr[model]
	return mode, expr, hasExpr
}

func GetBillingModeCopy() map[string]string {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	return lo.Assign(billingSetting.BillingMode)
}

func GetBillingExprCopy() map[string]string {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	return lo.Assign(billingSetting.BillingExpr)
}

func GetPricingSyncData(base map[string]any) map[string]any {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	extra := make(map[string]any, 2)
	if modes := lo.Assign(billingSetting.BillingMode); len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if exprs := lo.Assign(billingSetting.BillingExpr); len(exprs) > 0 {
		extra[BillingExprField] = exprs
	}
	return lo.Assign(base, extra)
}

// UpdateBillingConfig 在同一写锁内替换模式与表达式，保证热路径始终读取到同一版本。
func UpdateBillingConfig(modeJSON string, exprJSON string) error {
	modes := make(map[string]string)
	if err := common.UnmarshalJsonStr(modeJSON, &modes); err != nil {
		return err
	}
	exprs := make(map[string]string)
	if err := common.UnmarshalJsonStr(exprJSON, &exprs); err != nil {
		return err
	}
	billingSettingMu.Lock()
	billingSetting.BillingMode = modes
	billingSetting.BillingExpr = exprs
	billingSettingMu.Unlock()
	return nil
}

// UpdateBillingField 保持通用 Option 更新接口兼容，并让单字段更新也受并发保护。
func UpdateBillingField(field string, value string) error {
	parsed := make(map[string]string)
	if err := common.UnmarshalJsonStr(value, &parsed); err != nil {
		return err
	}
	billingSettingMu.Lock()
	defer billingSettingMu.Unlock()
	switch field {
	case BillingModeField:
		billingSetting.BillingMode = parsed
	case BillingExprField:
		billingSetting.BillingExpr = parsed
	}
	return nil
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 1000000, C: 1000000, Len: 1000000},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", v.P, v.C, result)
			}
		}
	}
	return nil
}
