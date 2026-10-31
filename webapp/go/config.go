package isureal

import (
	"net"
	"net/url"
	"os"
	"strconv"
)

type Config struct {
	Region       string
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	LogLevel     string
	MaxIdleConns int
}

func ConfigFromEnv() *Config {
	maxIdleConns := 2
	if value, ok := os.LookupEnv("ISUREAL_DB_MAX_IDLE_CONNS"); ok {
		maxIdleConns = parseMaxIdleConns(value)
	}
	return &Config{
		Region:       envOrDefault("ISUREAL_REGION", "asia"),
		DBHost:       envOrDefault("ISUREAL_DB_HOST", "127.0.0.1"),
		DBPort:       envOrDefault("ISUREAL_DB_PORT", "5432"),
		DBUser:       envOrDefault("ISUREAL_DB_USER", "isureal"),
		DBPassword:   envOrDefault("ISUREAL_DB_PASSWORD", "isureal"),
		DBName:       envOrDefault("ISUREAL_DB_NAME", "isureal"),
		LogLevel:     envOrDefault("ISUREAL_LOG_LEVEL", "debug"),
		MaxIdleConns: maxIdleConns,
	}
}

func parseMaxIdleConns(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > 30 {
		panic("ISUREAL_DB_MAX_IDLE_CONNS must be an integer between 0 and 30")
	}
	return parsed
}

func (c *Config) DatabaseDSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword),
		Host:   net.JoinHostPort(c.DBHost, c.DBPort),
		Path:   "/" + c.DBName,
	}
	return u.String()
}

func envOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
