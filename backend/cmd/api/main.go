package main

import (
	"log"
	"os"
	"time"

	"dast/internal/api"
	"dast/internal/config"
	"dast/internal/database"
	"dast/internal/k8s"
	"dast/internal/model"
	"dast/internal/redisstream"
	"dast/internal/service/scheduler"
	"gorm.io/gorm"
)

func main() {
	cfg := config.Load()
	if loc, err := time.LoadLocation(cfg.Timezone); err == nil {
		time.Local = loc
	} else {
		log.Printf("WARN: invalid DAST_TIMEZONE=%s: %v", cfg.Timezone, err)
	}

	if cfg.JWTSecret == "dast-dev-secret-change-me" {
		log.Println("WARN: DAST_JWT_SECRET is default; set a strong value in production")
	}

	db, err := database.Open(cfg)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	if err := seedInitialAdmin(db); err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	rm, err := redisstream.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		log.Printf("WARN: redis init failed (will limit features): %v", err)
	}

	// K3s client 可能在本地缺少 kubeconfig 时初始化失败,允许后端在无集群下运行(部分接口受限)。
	var kc *k8s.Client
	if c, err := k8s.New(); err != nil {
		log.Printf("WARN: kube client init failed (cluster ops disabled): %v", err)
	} else {
		kc = c
		log.Printf("kubeconfig source: %s", kc.Source)
	}

	sched := scheduler.New(db, kc, rm)

	r := api.Build(cfg, db, kc, rm, sched)
	log.Printf("DAST backend listening on %s", cfg.HTTPListen)
	if err := r.Run(cfg.HTTPListen); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// seedInitialAdmin 初次启动时如果没有用户,基于环境变量创建默认管理员(明文密码,允许直接在 DB 改密)。
func seedInitialAdmin(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.User{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	user := os.Getenv("DAST_ADMIN_USER")
	pass := os.Getenv("DAST_ADMIN_PASS")
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = "admin"
	}
	return db.Create(&model.User{Username: user, Password: pass, Role: "admin"}).Error
}
