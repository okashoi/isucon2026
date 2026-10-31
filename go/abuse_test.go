package isureal

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

type abuseTestServer struct {
	t      *testing.T
	server *httptest.Server
	db     *sqlx.DB
}

// startAbuseTestServers は同じ DB を使う 2 台を起動する。POST /api/initialize でデータを消すので、
// ISUREAL_TEST_DB_HOST で検証用の DB を指定したときだけ動かす。sql/init.sh のために psql も必要になる。
func startAbuseTestServers(t *testing.T) (*abuseTestServer, *abuseTestServer) {
	t.Helper()
	host := envOrDefault("ISUREAL_TEST_DB_HOST", "")
	if host == "" {
		t.Skip("ISUREAL_TEST_DB_HOST is not set")
	}
	t.Setenv("ISUREAL_DB_HOST", host)
	t.Setenv("ISUREAL_LOG_LEVEL", "error")

	start := func(region string) (*httptest.Server, *Config) {
		t.Setenv("ISUREAL_POST_IMAGE_DIR", t.TempDir())
		t.Setenv("ISUREAL_IMAGE_CACHE_DIR", t.TempDir())
		config := ConfigFromEnv()
		config.Region = region
		server := httptest.NewServer(NewServer(config))
		t.Cleanup(server.Close)
		return server, config
	}
	asia, asiaConfig := start("asia")
	europe, europeConfig := start("europe")
	for _, config := range []*Config{asiaConfig, europeConfig} {
		config.PeerURLs["asia"] = asia.URL
		config.PeerURLs["europe"] = europe.URL
		config.PeerURLs["america"] = europe.URL
	}

	db, err := sqlx.Open("pgx", asiaConfig.DatabaseDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := &abuseTestServer{t: t, server: asia, db: db}
	s.initialize()
	return s, &abuseTestServer{t: t, server: europe, db: db}
}

func (s *abuseTestServer) initialize() {
	s.t.Helper()
	if status, body := s.send(http.MethodPost, "/api/initialize", "", "", "", nil); status != http.StatusOK {
		s.t.Fatalf("initialize: status = %d, body = %s", status, body)
	}
	// 他のサーバでも ip_bans のスナップショットが切り替わるのを待つ。
	time.Sleep(2 * ipBanReloadInterval)
}

func (s *abuseTestServer) send(method, path, ip, token, contentType string, body []byte) (int, string) {
	s.t.Helper()
	req, err := http.NewRequest(method, s.server.URL+path, bytes.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if ip != "" {
		req.Header.Set("X-Client-IP", ip)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return res.StatusCode, string(data)
}

func (s *abuseTestServer) failLogin(ip string) (int, string) {
	s.t.Helper()
	return s.send(http.MethodPost, "/api/auth/login", ip, "", "application/json", []byte(`{"username":"evil_login","secretKey":"x"}`))
}

func (s *abuseTestServer) failLogins(ip string, times int) {
	s.t.Helper()
	for range times {
		s.failLogin(ip)
	}
}

// isBanned は検知の対象外である不正な登録で確かめるので、失敗の回数を変えない。
func (s *abuseTestServer) isBanned(ip string) bool {
	s.t.Helper()
	status, body := s.send(http.MethodPost, "/api/auth/register", ip, "", "application/json", []byte(`{}`))
	return status == http.StatusForbidden && strings.Contains(body, "ip_banned")
}

// registerWithProfileImage は利用者を作り、トークンと署名付きのプロフィール画像 URL を返す。
func (s *abuseTestServer) registerWithProfileImage(username string) (string, string) {
	s.t.Helper()
	status, body := s.send(http.MethodPost, "/api/auth/register", "", "", "application/json",
		fmt.Appendf(nil, `{"username":%q,"displayName":"abuse","region":"asia"}`, username))
	if status != http.StatusCreated {
		s.t.Fatalf("register: status = %d, body = %s", status, body)
	}
	var registered registerResponse
	if err := json.Unmarshal([]byte(body), &registered); err != nil {
		s.t.Fatal(err)
	}
	status, body = s.send(http.MethodPost, "/api/auth/login", "", "", "application/json",
		fmt.Appendf(nil, `{"username":%q,"secretKey":%q}`, username, registered.SecretKey))
	if status != http.StatusOK {
		s.t.Fatalf("login: status = %d, body = %s", status, body)
	}
	var loggedIn loginResponse
	if err := json.Unmarshal([]byte(body), &loggedIn); err != nil {
		s.t.Fatal(err)
	}
	var valid bytes.Buffer
	if err := jpeg.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		s.t.Fatal(err)
	}
	status, body = s.send(http.MethodPut, "/api/me/profile-image", "", loggedIn.Token, "image/jpeg", valid.Bytes())
	if status != http.StatusNoContent {
		s.t.Fatalf("put profile image: status = %d, body = %s", status, body)
	}
	status, body = s.send(http.MethodGet, "/api/me", "", loggedIn.Token, "", nil)
	if status != http.StatusOK {
		s.t.Fatalf("get me: status = %d, body = %s", status, body)
	}
	var me struct {
		ProfileImageURL string `json:"profileImageUrl"`
	}
	if err := json.Unmarshal([]byte(body), &me); err != nil {
		s.t.Fatal(err)
	}
	return loggedIn.Token, me.ProfileImageURL
}

func forgeSignature(t *testing.T, signedURL string) string {
	t.Helper()
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("sig", base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func TestAbuseBan(t *testing.T) {
	s, peer := startAbuseTestServers(t)
	token, imageURL := s.registerWithProfileImage("abuse_test_user")
	forged := forgeSignature(t, imageURL)

	tests := []struct {
		name       string
		ip         string
		threshold  int
		wantStatus int
		attack     func(ip string) (int, string)
	}{
		{"login", "203.0.113.1", 10, http.StatusUnauthorized, s.failLogin},
		{"invalid_image", "203.0.113.2", 2, http.StatusBadRequest, func(ip string) (int, string) {
			return s.send(http.MethodPut, "/api/me/profile-image", ip, token, "image/jpeg", []byte("not a jpeg"))
		}},
		{"invalid_signature", "203.0.113.3", 3, http.StatusForbidden, func(ip string) (int, string) {
			return s.send(http.MethodGet, forged, ip, "", "", nil)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := 1; i <= tt.threshold; i++ {
				status, body := tt.attack(tt.ip)
				if status != tt.wantStatus || strings.Contains(body, "ip_banned") {
					t.Fatalf("attack %d: status = %d, body = %s", i, status, body)
				}
			}
			status, body := tt.attack(tt.ip)
			if status != http.StatusForbidden || !strings.Contains(body, "ip_banned") {
				t.Fatalf("after threshold: status = %d, body = %s", status, body)
			}

			var remaining float64
			deadline := time.Now().Add(2 * time.Second)
			for {
				err := s.db.Get(&remaining, `SELECT EXTRACT(EPOCH FROM banned_until - clock_timestamp())::float8 FROM ip_bans WHERE ip = $1`, tt.ip)
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("ip_bans has no row: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if remaining < 100 || remaining > 120 {
				t.Fatalf("remaining ban = %f seconds", remaining)
			}
		})
	}

	t.Run("below threshold", func(t *testing.T) {
		const ip = "203.0.113.4"
		s.failLogins(ip, 9)
		if s.isBanned(ip) {
			t.Fatal("banned before the threshold")
		}
		s.failLogin(ip)
		if !s.isBanned(ip) {
			t.Fatal("not banned at the threshold")
		}
	})

	t.Run("kinds are counted separately", func(t *testing.T) {
		const ip = "203.0.113.5"
		s.failLogins(ip, 9)
		s.send(http.MethodGet, forged, ip, "", "", nil)
		if s.isBanned(ip) {
			t.Fatal("banned by adding failures of different kinds")
		}
	})

	t.Run("other IP is unaffected", func(t *testing.T) {
		if s.isBanned("203.0.113.6") {
			t.Fatal("unrelated IP is banned")
		}
	})

	t.Run("counts are per server", func(t *testing.T) {
		const ip = "203.0.113.7"
		s.failLogins(ip, 5)
		peer.failLogins(ip, 5)
		if s.isBanned(ip) || peer.isBanned(ip) {
			t.Fatal("counts were shared between servers")
		}
	})

	t.Run("peer learns the ban from ip_bans", func(t *testing.T) {
		deadline := time.Now().Add(time.Second)
		for !peer.isBanned("203.0.113.1") {
			if time.Now().After(deadline) {
				t.Fatal("peer did not learn the ban")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	t.Run("initialize clears bans and counts on every server", func(t *testing.T) {
		const (
			bannedOnPeer  = "203.0.113.8"
			countedOnPeer = "203.0.113.9"
		)
		peer.failLogins(bannedOnPeer, 10)
		if !peer.isBanned(bannedOnPeer) {
			t.Fatal("not banned on peer")
		}
		s.failLogins(countedOnPeer, 9)
		peer.failLogins(countedOnPeer, 9)

		s.initialize()

		for _, server := range []*abuseTestServer{s, peer} {
			for _, tt := range tests {
				if server.isBanned(tt.ip) {
					t.Fatalf("%s: still banned after initialize", tt.name)
				}
			}
			if server.isBanned(bannedOnPeer) {
				t.Fatal("ban on peer survived initialize")
			}
			server.failLogin(countedOnPeer)
			if server.isBanned(countedOnPeer) {
				t.Fatal("count survived initialize")
			}
		}
	})
}
