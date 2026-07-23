package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelAliasCatalogRouteRequiresRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("model-alias-route-test"))))
	engine.GET("/test-login/:role", func(c *gin.Context) {
		role := common.RoleAdminUser
		if c.Param("role") == "root" {
			role = common.RoleRootUser
		}
		session := sessions.Default(c)
		session.Set("username", c.Param("role"))
		session.Set("role", role)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	SetApiRouter(engine)

	requestCatalog := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/option/model-alias-groups/catalog", nil)
		if len(cookies) > 0 {
			request.Header.Set("New-Api-User", "1")
			for _, sessionCookie := range cookies {
				request.AddCookie(sessionCookie)
			}
		}
		engine.ServeHTTP(recorder, request)
		return recorder
	}
	login := func(role string) []*http.Cookie {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/test-login/"+role, nil)
		engine.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusNoContent, recorder.Code)
		return recorder.Result().Cookies()
	}

	assert.Equal(t, http.StatusUnauthorized, requestCatalog(nil).Code)

	adminResponse := requestCatalog(login("admin"))
	require.Equal(t, http.StatusOK, adminResponse.Code)
	assert.Contains(t, adminResponse.Body.String(), "auth.insufficient_privilege")

	rootResponse := requestCatalog(login("root"))
	require.Equal(t, http.StatusOK, rootResponse.Code)
	assert.Contains(t, rootResponse.Body.String(), "统一名称不能为空")
}
