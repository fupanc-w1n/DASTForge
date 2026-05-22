package config

import (
	"os"
	"testing"

	driver "github.com/go-sql-driver/mysql"
)

func TestLoadDefaultsUseLocalDevelopmentCredentials(t *testing.T) {
	unsetEnv(t,
		"DAST_DB_HOST",
		"DAST_DB_PORT",
		"DAST_DB_USER",
		"DAST_DB_PASS",
		"DAST_DB_NAME",
		"DAST_REDIS_ADDR",
		"DAST_REDIS_PASS",
		"DAST_REDIS_DB",
		"DAST_SCHEDULER_IP",
	)

	cfg := Load()

	if cfg.DBUser != "root" {
		t.Fatalf("DBUser = %q, want root", cfg.DBUser)
	}
	if cfg.DBPass != "root" {
		t.Fatalf("DBPass = %q, want root", cfg.DBPass)
	}
	if cfg.RedisPassword != "redis" {
		t.Fatalf("RedisPassword = %q, want redis", cfg.RedisPassword)
	}
	if cfg.SchedulerInternalIP != "10.0.0.1" {
		t.Fatalf("SchedulerInternalIP = %q, want 10.0.0.1", cfg.SchedulerInternalIP)
	}
}

func TestLoadUsesExplicitSchedulerIP(t *testing.T) {
	setEnv(t, "DAST_SCHEDULER_IP", "192.168.10.20")

	cfg := Load()

	if cfg.SchedulerInternalIP != "192.168.10.20" {
		t.Fatalf("SchedulerInternalIP = %q, want explicit env value", cfg.SchedulerInternalIP)
	}
}

func TestMySQLDSNIsParseableAndTargetsConfiguredDatabase(t *testing.T) {
	cfg := &Config{
		DBHost: "127.0.0.1",
		DBPort: 3306,
		DBUser: "root",
		DBPass: "root",
		DBName: "dast",
	}

	appDSN, err := driver.ParseDSN(cfg.MySQLDSN())
	if err != nil {
		t.Fatalf("parse app dsn: %v", err)
	}
	if appDSN.DBName != "dast" {
		t.Fatalf("app DBName = %q, want dast", appDSN.DBName)
	}
	if !appDSN.ParseTime {
		t.Fatal("app DSN ParseTime = false, want true")
	}
}

func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		old, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv(key, old)
				return
			}
			_ = os.Unsetenv(key)
		})
	}
}

func setEnv(t *testing.T, key, value string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}
