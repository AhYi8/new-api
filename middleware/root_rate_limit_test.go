package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var rootRateLimitTestSequence atomic.Uint32

func nextRootRateLimitTestIdentity() (int, string) {
	sequence := rootRateLimitTestSequence.Add(1)
	userID := 100000 + int(sequence)
	thirdOctet := (sequence / 250) % 250
	fourthOctet := sequence%250 + 1
	return userID, fmt.Sprintf("198.18.%d.%d:12345", thirdOctet, fourthOctet)
}

func useMemoryRateLimiter(t *testing.T) {
	t.Helper()
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
	})
}

func useUnavailableRedisRateLimiter(t *testing.T) {
	t.Helper()
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = true
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})
}

func configureGlobalAPIRateLimit(t *testing.T, maximum int, duration int64) {
	t.Helper()
	previousEnabled := common.GlobalApiRateLimitEnable
	previousMaximum := common.GlobalApiRateLimitNum
	previousDuration := common.GlobalApiRateLimitDuration
	common.GlobalApiRateLimitEnable = true
	common.GlobalApiRateLimitNum = maximum
	common.GlobalApiRateLimitDuration = duration
	t.Cleanup(func() {
		common.GlobalApiRateLimitEnable = previousEnabled
		common.GlobalApiRateLimitNum = previousMaximum
		common.GlobalApiRateLimitDuration = previousDuration
	})
}

func configureCriticalRateLimit(t *testing.T, maximum int, duration int64) {
	t.Helper()
	previousEnabled := common.CriticalRateLimitEnable
	previousMaximum := common.CriticalRateLimitNum
	previousDuration := common.CriticalRateLimitDuration
	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = maximum
	common.CriticalRateLimitDuration = duration
	t.Cleanup(func() {
		common.CriticalRateLimitEnable = previousEnabled
		common.CriticalRateLimitNum = previousMaximum
		common.CriticalRateLimitDuration = previousDuration
	})
}

func configureSearchRateLimit(t *testing.T, maximum int, duration int64) {
	t.Helper()
	previousEnabled := common.SearchRateLimitEnable
	previousMaximum := common.SearchRateLimitNum
	previousDuration := common.SearchRateLimitDuration
	common.SearchRateLimitEnable = true
	common.SearchRateLimitNum = maximum
	common.SearchRateLimitDuration = duration
	t.Cleanup(func() {
		common.SearchRateLimitEnable = previousEnabled
		common.SearchRateLimitNum = previousMaximum
		common.SearchRateLimitDuration = previousDuration
	})
}

func startRateLimitSession(t *testing.T, router http.Handler, role int, userID int) []*http.Cookie {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/session?role="+strconv.Itoa(role)+"&id="+strconv.Itoa(userID), nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	return recorder.Result().Cookies()
}

func performRootRateLimitRequest(
	router http.Handler,
	path string,
	remoteAddr string,
	userID string,
	cookies []*http.Cookie,
	authorization ...string,
) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = remoteAddr
	if userID != "" {
		request.Header.Set("New-Api-User", userID)
	}
	if len(authorization) > 0 {
		request.Header.Set("Authorization", authorization[0])
	}
	for _, sessionCookie := range cookies {
		request.AddCookie(sessionCookie)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func newSessionRateLimitRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("root-rate-limit-test"))))
	router.GET("/session", func(c *gin.Context) {
		role, err := strconv.Atoi(c.Query("role"))
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		userID, err := strconv.Atoi(c.Query("id"))
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		session := sessions.Default(c)
		session.Set("id", userID)
		session.Set("username", fmt.Sprintf("rate-limit-user-%d", userID))
		session.Set("role", role)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		if err := session.Save(); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	return router
}

func TestGlobalAPIRateLimitBypassesMatchingRootSessionWithoutConsumingQuota(t *testing.T) {
	useMemoryRateLimiter(t)
	configureGlobalAPIRateLimit(t, 1, 60)
	router := newSessionRateLimitRouter(t)
	router.GET("/global", GlobalAPIRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	userID, remoteAddr := nextRootRateLimitTestIdentity()
	headerUserID := strconv.Itoa(userID)
	cookies := startRateLimitSession(t, router, common.RoleRootUser, userID)
	for range 3 {
		response := performRootRateLimitRequest(router, "/global", remoteAddr, headerUserID, cookies)
		assert.Equal(t, http.StatusNoContent, response.Code)
	}

	firstLimitedUserResponse := performRootRateLimitRequest(router, "/global", remoteAddr, "", nil)
	assert.Equal(t, http.StatusNoContent, firstLimitedUserResponse.Code)
	secondLimitedUserResponse := performRootRateLimitRequest(router, "/global", remoteAddr, "", nil)
	assert.Equal(t, http.StatusTooManyRequests, secondLimitedUserResponse.Code)
}

func TestGlobalAPIRateLimitRequiresCompleteRootSessionIdentity(t *testing.T) {
	useMemoryRateLimiter(t)
	configureGlobalAPIRateLimit(t, 1, 60)
	router := newSessionRateLimitRouter(t)
	router.GET("/global", GlobalAPIRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	testCases := []struct {
		name          string
		role          int
		headerMode    string
		authorization string
	}{
		{name: "缺少用户请求头", role: common.RoleRootUser, headerMode: "missing"},
		{name: "用户请求头不匹配", role: common.RoleRootUser, headerMode: "mismatch"},
		{name: "同时携带访问令牌", role: common.RoleRootUser, headerMode: "matching", authorization: "Bearer root-token"},
		{name: "管理员会话", role: common.RoleAdminUser, headerMode: "matching"},
		{name: "普通用户会话", role: common.RoleCommonUser, headerMode: "matching"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			userID, remoteAddr := nextRootRateLimitTestIdentity()
			headerUserID := strconv.Itoa(userID)
			switch testCase.headerMode {
			case "missing":
				headerUserID = ""
			case "mismatch":
				headerUserID = strconv.Itoa(userID + 1)
			}
			cookies := startRateLimitSession(t, router, testCase.role, userID)
			firstResponse := performRootRateLimitRequest(
				router, "/global", remoteAddr, headerUserID, cookies, testCase.authorization,
			)
			assert.Equal(t, http.StatusNoContent, firstResponse.Code)
			secondResponse := performRootRateLimitRequest(
				router, "/global", remoteAddr, headerUserID, cookies, testCase.authorization,
			)
			assert.Equal(t, http.StatusTooManyRequests, secondResponse.Code)
		})
	}
}

func TestGlobalAPIRateLimitWithoutSessionMiddlewareDoesNotPanic(t *testing.T) {
	useMemoryRateLimiter(t)
	configureGlobalAPIRateLimit(t, 1, 60)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/global", GlobalAPIRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	_, remoteAddr := nextRootRateLimitTestIdentity()
	assert.Equal(t, http.StatusNoContent, performRootRateLimitRequest(router, "/global", remoteAddr, "", nil).Code)
	assert.Equal(t, http.StatusTooManyRequests, performRootRateLimitRequest(router, "/global", remoteAddr, "", nil).Code)
}

func TestRootExemptCriticalRateLimitOnlyBypassesExplicitRootManagementRoute(t *testing.T) {
	useMemoryRateLimiter(t)
	configureCriticalRateLimit(t, 1, 60)
	router := newSessionRateLimitRouter(t)
	router.GET(
		"/management",
		RootAuth(),
		RootExemptCriticalRateLimit(),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)
	router.GET(
		"/security",
		RootAuth(),
		CriticalRateLimit(),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)
	router.GET("/limited", CriticalRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	userID, managementRemoteAddr := nextRootRateLimitTestIdentity()
	headerUserID := strconv.Itoa(userID)
	cookies := startRateLimitSession(t, router, common.RoleRootUser, userID)
	for range 3 {
		response := performRootRateLimitRequest(
			router, "/management", managementRemoteAddr, headerUserID, cookies,
		)
		assert.Equal(t, http.StatusNoContent, response.Code)
	}
	assert.Equal(t, http.StatusNoContent, performRootRateLimitRequest(
		router, "/limited", managementRemoteAddr, "", nil,
	).Code)
	assert.Equal(t, http.StatusTooManyRequests, performRootRateLimitRequest(
		router, "/limited", managementRemoteAddr, "", nil,
	).Code)

	_, securityRemoteAddr := nextRootRateLimitTestIdentity()
	assert.Equal(t, http.StatusNoContent, performRootRateLimitRequest(
		router, "/security", securityRemoteAddr, headerUserID, cookies,
	).Code)
	assert.Equal(t, http.StatusTooManyRequests, performRootRateLimitRequest(
		router, "/security", securityRemoteAddr, headerUserID, cookies,
	).Code)
}

func TestSearchRateLimitBypassesAuthenticatedRootWithoutConsumingUserQuota(t *testing.T) {
	useMemoryRateLimiter(t)
	configureSearchRateLimit(t, 1, 60)
	router := newSessionRateLimitRouter(t)
	router.GET(
		"/search",
		UserAuth(),
		SearchRateLimit(),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	userID, remoteAddr := nextRootRateLimitTestIdentity()
	headerUserID := strconv.Itoa(userID)
	rootCookies := startRateLimitSession(t, router, common.RoleRootUser, userID)
	for range 3 {
		response := performRootRateLimitRequest(router, "/search", remoteAddr, headerUserID, rootCookies)
		assert.Equal(t, http.StatusNoContent, response.Code)
	}

	commonUserCookies := startRateLimitSession(t, router, common.RoleCommonUser, userID)
	assert.Equal(t, http.StatusNoContent, performRootRateLimitRequest(
		router, "/search", remoteAddr, headerUserID, commonUserCookies,
	).Code)
	assert.Equal(t, http.StatusTooManyRequests, performRootRateLimitRequest(
		router, "/search", remoteAddr, headerUserID, commonUserCookies,
	).Code)
}

func TestRootRateLimitBypassesBeforeRedisAccess(t *testing.T) {
	useUnavailableRedisRateLimiter(t)
	configureGlobalAPIRateLimit(t, 1, 60)
	configureCriticalRateLimit(t, 1, 60)
	configureSearchRateLimit(t, 1, 60)
	router := newSessionRateLimitRouter(t)
	router.GET("/global", GlobalAPIRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET(
		"/critical",
		RootAuth(),
		RootExemptCriticalRateLimit(),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)
	router.GET(
		"/search",
		UserAuth(),
		SearchRateLimit(),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	userID, remoteAddr := nextRootRateLimitTestIdentity()
	headerUserID := strconv.Itoa(userID)
	cookies := startRateLimitSession(t, router, common.RoleRootUser, userID)
	for _, path := range []string{"/global", "/critical", "/search"} {
		response := performRootRateLimitRequest(router, path, remoteAddr, headerUserID, cookies)
		assert.Equal(t, http.StatusNoContent, response.Code)
	}
}
