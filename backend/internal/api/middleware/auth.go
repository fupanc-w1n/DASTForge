package middleware

import (
	"crypto/subtle"
	"strings"

	"dast/internal/auth"
	"dast/internal/config"
	"github.com/gin-gonic/gin"
)

const (
	ctxUserKey = "user"
)

// JWT 校验,通过 Authorization: Bearer 传入。
func JWT(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := ""
		if v := c.GetHeader("Authorization"); strings.HasPrefix(v, "Bearer ") {
			tok = strings.TrimPrefix(v, "Bearer ")
		}
		if tok == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}
		claims, err := auth.ParseToken(cfg.JWTSecret, tok)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid token"})
			return
		}
		c.Set(ctxUserKey, claims)
		c.Next()
	}
}

// JWTOrAPIToken JWT 优先,失败回退 X-DAST-Token。
func JWTOrAPIToken(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v := c.GetHeader("Authorization"); strings.HasPrefix(v, "Bearer ") {
			tok := strings.TrimPrefix(v, "Bearer ")
			if claims, err := auth.ParseToken(cfg.JWTSecret, tok); err == nil {
				c.Set(ctxUserKey, claims)
				c.Next()
				return
			}
		}
		if tok := c.GetHeader("X-DAST-Token"); tok != "" && cfg.APIToken != "" &&
			subtle.ConstantTimeCompare([]byte(tok), []byte(cfg.APIToken)) == 1 {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
	}
}

// CurrentUser 获取当前用户 claims
func CurrentUser(c *gin.Context) *auth.Claims {
	if v, ok := c.Get(ctxUserKey); ok {
		if claims, ok := v.(*auth.Claims); ok {
			return claims
		}
	}
	return nil
}
