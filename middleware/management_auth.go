package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func CodexPoolManagementAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "missing Authorization header",
			})
			c.Abort()
			return
		}

		token := authHeader
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = strings.TrimSpace(token[7:])
		}
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "invalid bearer token",
			})
			c.Abort()
			return
		}

		managementToken := strings.TrimSpace(common.GetEnvOrDefaultString("CODEX_POOL_MANAGEMENT_TOKEN", ""))
		if managementToken != "" && token == managementToken {
			c.Set("use_management_token", true)
			c.Next()
			return
		}

		user := model.ValidateAccessToken(token)
		if user == nil || user.Username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "invalid api token",
			})
			c.Abort()
			return
		}
		if user.Status != common.UserStatusEnabled {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "user is disabled",
			})
			c.Abort()
			return
		}
		if user.Role < common.RoleAdminUser {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "admin permission required",
			})
			c.Abort()
			return
		}

		c.Set("id", user.Id)
		c.Set("username", user.Username)
		c.Set("role", user.Role)
		c.Set("use_access_token", true)
		c.Next()
	}
}
