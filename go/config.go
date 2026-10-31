package isureal

import (
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

// クエリの第1引数に渡すと、既定の simple protocol の代わりに prepared statement で実行する。
// simple protocol では bytea が hex のテキストになって転送量が2倍になり、
// パーティション数の多い image_objects を引くクエリは実行のたびにプランニングが走る。
const preparedQuery = pgx.QueryExecModeCacheStatement

type Config struct {
	Region string
	DBHost string
	// DBReadHost が空なら読みも DBHost（primary）へ。s2/s3 では 127.0.0.1 を想定。
	DBReadHost string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	LogLevel   string
	// リージョンごとのアプリサーバ。カンマ区切りで複数可。通信はルーター経由。
	// asia は s1 と s4 の二台を想定する。
	PeerURLs map[string][]string
}

func ConfigFromEnv() *Config {
	return &Config{
		Region:     envOrDefault("ISUREAL_REGION", "asia"),
		DBHost:     envOrDefault("ISUREAL_DB_HOST", "127.0.0.1"),
		DBReadHost: os.Getenv("ISUREAL_DB_READ_HOST"),
		DBPort:     envOrDefault("ISUREAL_DB_PORT", "5432"),
		DBUser:     envOrDefault("ISUREAL_DB_USER", "isureal"),
		DBPassword: envOrDefault("ISUREAL_DB_PASSWORD", "isureal"),
		DBName:     envOrDefault("ISUREAL_DB_NAME", "isureal"),
		LogLevel:   envOrDefault("ISUREAL_LOG_LEVEL", "debug"),
		PeerURLs: map[string][]string{
			"asia":    splitPeerURLs(envOrDefault("ISUREAL_PEER_URL_ASIA", "http://192.168.1.10:8080,http://192.168.4.10:8080")),
			"europe":  splitPeerURLs(envOrDefault("ISUREAL_PEER_URL_EUROPE", "http://192.168.2.10:8080")),
			"america": splitPeerURLs(envOrDefault("ISUREAL_PEER_URL_AMERICA", "http://192.168.3.10:8080")),
		},
	}
}

func (c *Config) DatabaseDSN() string {
	return c.databaseDSN(c.DBHost)
}

func (c *Config) DatabaseReadDSN() string {
	host := c.DBReadHost
	if host == "" {
		host = c.DBHost
	}
	return c.databaseDSN(host)
}

func (c *Config) databaseDSN(host string) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.DBUser, c.DBPassword),
		Host:     net.JoinHostPort(host, c.DBPort),
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

func splitPeerURLs(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, strings.TrimRight(p, "/"))
		}
	}
	return out
}
