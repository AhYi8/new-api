package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateBillingConfigPublishesModeAndExpressionTogether(t *testing.T) {
	oldModes, err := common.Marshal(GetBillingModeCopy())
	require.NoError(t, err)
	oldExprs, err := common.Marshal(GetBillingExprCopy())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, UpdateBillingConfig(string(oldModes), string(oldExprs)))
	})

	require.NoError(t, UpdateBillingConfig(
		`{"snapshot-model":"tiered_expr"}`,
		`{"snapshot-model":"p * 2 + c * 3"}`,
	))
	mode, expr, hasExpr := GetBillingConfig("snapshot-model")
	assert.Equal(t, BillingModeTieredExpr, mode)
	assert.True(t, hasExpr)
	assert.Equal(t, "p * 2 + c * 3", expr)

	require.Error(t, UpdateBillingConfig(`{"snapshot-model":"ratio"}`, `{`))
	mode, expr, hasExpr = GetBillingConfig("snapshot-model")
	assert.Equal(t, BillingModeTieredExpr, mode)
	assert.True(t, hasExpr)
	assert.Equal(t, "p * 2 + c * 3", expr)
}
