package api

import (
	"dast/internal/api/handler"
	"dast/internal/api/middleware"
	"dast/internal/config"
	"dast/internal/k8s"
	"dast/internal/redisstream"
	"dast/internal/service/scheduler"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Build 构造 Gin engine 并注册所有路由。
func Build(cfg *config.Config, db *gorm.DB, k8sClient *k8s.Client, rm *redisstream.Manager, sched *scheduler.Scheduler) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
		AllowHeaders:     []string{"*"},
		AllowCredentials: false,
		MaxAge:           3600,
	}))

	authH := &handler.AuthHandler{DB: db, Cfg: cfg}
	policyH := &handler.PolicyHandler{DB: db, Sched: sched, K8s: k8sClient, Namespace: cfg.K8sNamespace, Cfg: cfg}
	taskH := &handler.TaskHandler{DB: db, Sched: sched}
	resH := &handler.ResourceHandler{DB: db, K8s: k8sClient, Redis: rm, Namespace: cfg.K8sNamespace}

	root := r.Group("/api/v1")
	{
		root.GET("/health", resH.Health)

		// 鉴权
		root.POST("/auth/login", authH.Login)
		root.POST("/auth/logout", authH.Logout)
	}

	// 需要 JWT 或 X-DAST-Token 的接口
	auth := root.Group("")
	auth.Use(middleware.JWTOrAPIToken(cfg))
	{
		auth.GET("/auth/me", authH.Me)

		// 策略
		auth.GET("/policies/default-template", policyH.DefaultTemplate)
		auth.GET("/policies", policyH.List)
		auth.POST("/policies", policyH.Create)
		auth.GET("/policies/:id", policyH.Get)
		auth.PUT("/policies/:id", policyH.Update)
		auth.DELETE("/policies/:id", policyH.Delete)
		auth.POST("/policies/:id/enable", policyH.Enable)
		auth.POST("/policies/:id/disable", policyH.Disable)
		auth.POST("/policies/:id/deploy", policyH.Deploy)
		auth.POST("/policies/:id/modules/:module/scale", policyH.ScaleModule)
		auth.GET("/policies/:id/runtime", policyH.Runtime)

		// 任务
		auth.GET("/tasks", taskH.List)
		auth.POST("/tasks", taskH.Create)
		auth.GET("/tasks/:id", taskH.Get)
		auth.DELETE("/tasks/:id", taskH.Delete)
		auth.POST("/tasks/:id/pause", taskH.Pause)
		auth.POST("/tasks/:id/resume", taskH.Resume)
		auth.POST("/tasks/:id/restart", taskH.Restart)
		auth.POST("/tasks/:id/terminate", taskH.Terminate)
		auth.GET("/tasks/:id/events", taskH.Events)
		auth.GET("/tasks/:id/parts", taskH.Parts)
		auth.GET("/tasks/:id/results/ports", taskH.PortResults)
		auth.GET("/tasks/:id/results/services", taskH.ServiceResults)
		auth.GET("/tasks/:id/results/vulnerabilities", taskH.Vulnerabilities)
		auth.GET("/tasks/:id/results/weak-passwords", taskH.WeakPasswords)

		// 资源
		auth.GET("/resources/summary", resH.Summary)
		auth.GET("/resources/nodes", resH.Nodes)
		auth.GET("/resources/pods", resH.Pods)
		auth.GET("/resources/policies/:id/pods", resH.PolicyPods)
		auth.GET("/resources/streams", resH.Streams)
	}

	return r
}
