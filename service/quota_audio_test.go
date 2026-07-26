package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPreWssConsumeQuotaSkipsFixedPriceIncrementalCharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayInfo := &relaycommon.RelayInfo{
		PriceData: types.PriceData{UsePrice: true},
	}

	require.NoError(t, PreWssConsumeQuota(context, relayInfo, &dto.RealtimeUsage{}))
}
