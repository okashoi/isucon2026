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
	Region string
	DBHost string
	// DBReadHost が空なら読みも DBHost（primary）へ。s2/s3 では 127.0.0.1 を想定。
	DBReadHost string
	// DBOffloadHost は s4 から逃がす重い読み取り（利用者検索）の宛先。空なら無効。
	// asia で読みも s4 へ送る構成では europe1 の同期 standby（remote_apply）を既定にする。
	DBOffloadHost string
	DBPort        string
	DBUser        string
	DBPassword    string
	DBName        string
	LogLevel      string
	// リージョンごとのアプリサーバ。この宛先への通信はルーターを経由する。
	PeerURLs map[string]string
}

func ConfigFromEnv() *Config {
	c := &Config{
		Region:     envOrDefault("ISUREAL_REGION", "asia"),
		DBHost:     envOrDefault("ISUREAL_DB_HOST", "127.0.0.1"),
		DBReadHost: os.Getenv("ISUREAL_DB_READ_HOST"),
		DBPort:     envOrDefault("ISUREAL_DB_PORT", "5432"),
		DBUser:     envOrDefault("ISUREAL_DB_USER", "isureal"),
		DBPassword: envOrDefault("ISUREAL_DB_PASSWORD", "isureal"),
		DBName:     envOrDefault("ISUREAL_DB_NAME", "isureal"),
		LogLevel:   envOrDefault("ISUREAL_LOG_LEVEL", "error"),
		PeerURLs: map[string]string{
			"asia":    envOrDefault("ISUREAL_PEER_URL_ASIA", "http://192.168.1.10:8080"),
			"europe":  envOrDefault("ISUREAL_PEER_URL_EUROPE", "http://192.168.2.10:8080"),
			"america": envOrDefault("ISUREAL_PEER_URL_AMERICA", "http://192.168.3.10:8080"),
		},
		DBOffloadHost: os.Getenv("ISUREAL_DB_OFFLOAD_HOST"),
	}
	if c.DBOffloadHost == "" && c.Region == "asia" && c.DBReadHost == "" {
		c.DBOffloadHost = "192.168.2.10"
	}
	return c
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
