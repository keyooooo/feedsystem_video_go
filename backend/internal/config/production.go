package config

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
)

func IsProduction() bool {
	return os.Getenv("FEEDSYSTEM_PRODUCTION") == "true"
}

func (cfg Config) ValidateProduction(usedDefault bool, api bool) error {
	if usedDefault || cfg.ObservabilityConfig.Pprof.Enabled {
		return fmt.Errorf("production requires an explicit config with pprof disabled")
	}
	if cfg.Database.User == "" || cfg.Database.User == "root" || cfg.RabbitMQ.Username == "" || cfg.RabbitMQ.Username == "guest" {
		return fmt.Errorf("production requires dedicated MySQL and RabbitMQ users")
	}
	secrets := []struct{ name, value string }{
		{"MYSQL_PASSWORD", cfg.Database.Password},
		{"REDIS_PASSWORD", cfg.Redis.Password},
		{"RABBITMQ_PASS", cfg.RabbitMQ.Password},
	}
	if api {
		if _, err := netip.ParseAddr(os.Getenv("APP_TRUSTED_PROXY")); err != nil {
			return fmt.Errorf("APP_TRUSTED_PROXY must be the frontend container IP")
		}
		secrets = append(secrets, struct{ name, value string }{"JWT_SECRET", os.Getenv("JWT_SECRET")})
	}
	seen := map[string]bool{}
	for _, secret := range secrets {
		if _, err := hex.DecodeString(secret.value); len(secret.value) != 64 || err != nil || seen[secret.value] {
			return fmt.Errorf("%s must be a distinct 32-byte hex secret", secret.name)
		}
		seen[secret.value] = true
	}
	return nil
}
