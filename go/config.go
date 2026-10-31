package isureal

import (
	"net"
	"net/url"
	"os"

	"github.com/jackc/pgx/v5"
)

// クエリの第1引数に渡すと、既定の simple protocol の代わりに prepared statement で実行する。
// simple protocol では bytea が hex のテキストになって転送量が2倍になり、
// パーティション数の多い image_objects を引くクエリは実行のたびにプランニングが走る。
const preparedQuery = pgx.QueryExecModeCacheStatement

type Config struct {
	Region     string
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	LogLevel   string
}

func ConfigFromEnv() *Config {
	return &Config{
		Region:     envOrDefault("ISUREAL_REGION", "asia"),
		DBHost:     envOrDefault("ISUREAL_DB_HOST", "127.0.0.1"),
		DBPort:     envOrDefault("ISUREAL_DB_PORT", "5432"),
		DBUser:     envOrDefault("ISUREAL_DB_USER", "isureal"),
		DBPassword: envOrDefault("ISUREAL_DB_PASSWORD", "isureal"),
		DBName:     envOrDefault("ISUREAL_DB_NAME", "isureal"),
		LogLevel:   envOrDefault("ISUREAL_LOG_LEVEL", "debug"),
	}
}

func (c *Config) DatabaseDSN() string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.DBUser, c.DBPassword),
		Host:     net.JoinHostPort(c.DBHost, c.DBPort),
		Path:     "/" + c.DBName,
		RawQuery: "default_query_exec_mode=simple_protocol",
	}
	return u.String()
}

func envOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
