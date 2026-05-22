package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
)

// Config 承载后端运行所需的全部配置,通过环境变量加载,避免泄漏明文凭据。
type Config struct {
	HTTPListen string
	JWTSecret  string
	APIToken   string

	DBHost string
	DBPort int
	DBUser string
	DBPass string
	DBName string

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// 默认 namespace,后端创建 K3s 资源时使用
	K8sNamespace string

	// 调度层对外暴露 Redis/MySQL 的内网地址,用于策略默认填充
	SchedulerInternalIP string
	SchedulerRedisPort  int
	SchedulerMySQLPort  int

	// 锁/PEL 默认时间(秒)
	LockTTLSeconds     int
	LockRenewSeconds   int
	PendingIdleSeconds int
	TargetBatchSize    int

	// 系统默认时区,影响后端写入 MySQL 的时间和 DSN loc 参数。
	Timezone string
}

// Load 从环境变量加载,提供一组合理的默认值,使本地启动可用。
func Load() *Config {
	c := &Config{
		HTTPListen: getEnv("DAST_LISTEN", ":8080"),
		JWTSecret:  getEnv("DAST_JWT_SECRET", "dast-dev-secret-change-me"),
		APIToken:   getEnv("DAST_API_TOKEN", ""),

		DBHost: getEnv("DAST_DB_HOST", "127.0.0.1"),
		DBPort: getEnvInt("DAST_DB_PORT", 3306),
		DBUser: getEnv("DAST_DB_USER", "root"),
		DBPass: getEnv("DAST_DB_PASS", "root"),
		DBName: getEnv("DAST_DB_NAME", "dast"),

		RedisAddr:     getEnv("DAST_REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: getEnv("DAST_REDIS_PASS", "redis"),
		RedisDB:       getEnvInt("DAST_REDIS_DB", 0),

		K8sNamespace: getEnv("DAST_K8S_NAMESPACE", "dast-system"),

		SchedulerInternalIP: getEnv("DAST_SCHEDULER_IP", "10.0.0.1"),
		SchedulerRedisPort:  getEnvInt("DAST_SCHEDULER_REDIS_PORT", 6379),
		SchedulerMySQLPort:  getEnvInt("DAST_SCHEDULER_MYSQL_PORT", 3306),

		LockTTLSeconds:     getEnvInt("DAST_LOCK_TTL_SECONDS", 360),
		LockRenewSeconds:   getEnvInt("DAST_LOCK_RENEW_SECONDS", 300),
		PendingIdleSeconds: getEnvInt("DAST_PENDING_IDLE_SECONDS", 120),
		TargetBatchSize:    getEnvInt("DAST_TARGET_BATCH_SIZE", 10),
		Timezone:           getEnv("DAST_TIMEZONE", "Asia/Shanghai"),
	}
	if c.TargetBatchSize <= 0 || c.TargetBatchSize > 10 {
		c.TargetBatchSize = 10
	}
	return c
}

// MySQLDSN 返回标准 GORM DSN
func (c *Config) MySQLDSN() string {
	tz := c.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	loc := url.QueryEscape(tz)
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=%s",
		c.DBUser, c.DBPass, c.DBHost, c.DBPort, c.DBName, loc)
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
