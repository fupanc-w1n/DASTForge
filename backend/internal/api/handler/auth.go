package handler

import (
	"net/http"
	"time"

	"dast/internal/api/middleware"
	"dast/internal/auth"
	"dast/internal/config"
	"dast/internal/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AuthHandler 登录/当前用户/登出
type AuthHandler struct {
	DB  *gorm.DB
	Cfg *config.Config
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login 颁发 JWT
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var u model.User
	if err := h.DB.Where("username = ?", req.Username).First(&u).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if !auth.VerifyPassword(u.Password, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	token, err := auth.IssueToken(h.Cfg.JWTSecret, u.ID, u.Username, u.Role, 24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{"id": u.ID, "username": u.Username, "role": u.Role},
	})
}

// Me 当前用户信息
func (h *AuthHandler) Me(c *gin.Context) {
	claims := middleware.CurrentUser(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":       claims.UserID,
		"username": claims.Username,
		"role":     claims.Role,
	})
}

// Logout 仅返回 OK,JWT 由前端丢弃
func (h *AuthHandler) Logout(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
