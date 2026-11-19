package config

import (
	"strings"
	"testing"
)

func TestProductionRequiresExplicitIndependentSecrets(t *testing.T) {
	t.Setenv("APP_TRUSTED_PROXY", "172.29.63.2")
	t.Setenv("JWT_SECRET", strings.Repeat("d", 64))
	cfg := Config{
		Database: DatabaseConfig{User: "feedsystem_app", Password: strings.Repeat("a", 64)},
		Redis:    RedisConfig{Password: strings.Repeat("b", 64)},
		RabbitMQ: RabbitMQConfig{Username: "feedsystem_app", Password: strings.Repeat("c", 64)},
	}
	if err := cfg.ValidateProduction(false, true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateProduction(true, true); err == nil {
		t.Fatal("accepted the development configuration fallback")
	}
	cfg.Redis.Password = cfg.Database.Password
	if err := cfg.ValidateProduction(false, false); err == nil {
		t.Fatal("accepted a reused service password")
	}
	cfg.Redis.Password = "123456"
	if err := cfg.ValidateProduction(false, true); err == nil {
		t.Fatal("accepted a development password")
	}
	cfg.Redis.Password = strings.Repeat("b", 64)
	t.Setenv("APP_TRUSTED_PROXY", "0.0.0.0/0")
	if err := cfg.ValidateProduction(false, true); err == nil {
		t.Fatal("accepted a network-wide reverse proxy trust setting")
	}
}
