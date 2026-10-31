package isureal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ISUREAL_TEST_DB_HOST に PostgreSQL を指定したときだけ実行する。
// sql/001-schema.sql を適用した DB に利用者を追加し、initialize のテストは全データを初期化する。
type regionServers map[string]*httptest.Server

func newRegionServers(t *testing.T) (regionServers, *Config) {
	t.Helper()
	host := os.Getenv("ISUREAL_TEST_DB_HOST")
	if host == "" {
		t.Skip("ISUREAL_TEST_DB_HOST が未設定")
	}
	peerURLs := map[string]string{}
	servers := regionServers{}
	var config *Config
	for _, region := range []string{"asia", "europe", "america"} {
		config = ConfigFromEnv()
		config.Region = region
		config.DBHost = host
		config.LogLevel = "error"
		config.PeerURLs = peerURLs
		server := httptest.NewServer(NewServer(config))
		t.Cleanup(server.Close)
		peerURLs[region] = server.URL
		servers[region] = server
	}
	return servers, config
}

func requestAPI(t *testing.T, server *httptest.Server, method, path, token string, body any) (int, []byte) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var response bytes.Buffer
	if _, err := response.ReadFrom(res.Body); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, response.Bytes()
}

func expectStatus(t *testing.T, server *httptest.Server, method, path, token string, body any, want int) {
	t.Helper()
	if got, response := requestAPI(t, server, method, path, token, body); got != want {
		t.Fatalf("%s %s: status = %d, want %d: %s", method, path, got, want, response)
	}
}

type testAccount struct {
	Username  string
	SecretKey string
}

func registerAccount(t *testing.T, server *httptest.Server, region string) testAccount {
	t.Helper()
	username := uuid.NewString()
	status, response := requestAPI(t, server, http.MethodPost, "/api/auth/register", "", map[string]string{
		"username": username, "displayName": username, "region": region,
	})
	if status != http.StatusCreated {
		t.Fatalf("register: status = %d: %s", status, response)
	}
	var registered registerResponse
	if err := json.Unmarshal(response, &registered); err != nil {
		t.Fatal(err)
	}
	return testAccount{Username: username, SecretKey: registered.SecretKey}
}

func login(t *testing.T, server *httptest.Server, account testAccount) string {
	t.Helper()
	status, response := requestAPI(t, server, http.MethodPost, "/api/auth/login", "", loginRequest{Username: account.Username, SecretKey: account.SecretKey})
	if status != http.StatusOK {
		t.Fatalf("login: status = %d: %s", status, response)
	}
	var loggedIn loginResponse
	if err := json.Unmarshal(response, &loggedIn); err != nil {
		t.Fatal(err)
	}
	return loggedIn.Token
}

// 別のサーバで変更したあとも、キャッシュを持つ asia のサーバから変更前の結果が返らないこと。
func TestSessionCacheFollowsWrites(t *testing.T) {
	servers, _ := newRegionServers(t)
	asia, europe := servers["asia"], servers["europe"]

	tests := []struct {
		name  string
		write func(t *testing.T, account testAccount, token string)
		check func(t *testing.T, account testAccount, token string)
	}{
		{
			name: "別リージョンでの再ログイン",
			write: func(t *testing.T, account testAccount, token string) {
				login(t, europe, account)
			},
			check: func(t *testing.T, account testAccount, token string) {
				expectStatus(t, asia, http.MethodGet, "/api/me", token, nil, http.StatusUnauthorized)
			},
		},
		{
			name: "別リージョンでのログアウト",
			write: func(t *testing.T, account testAccount, token string) {
				expectStatus(t, europe, http.MethodPost, "/api/auth/logout", token, nil, http.StatusNoContent)
			},
			check: func(t *testing.T, account testAccount, token string) {
				expectStatus(t, asia, http.MethodGet, "/api/me", token, nil, http.StatusUnauthorized)
			},
		},
		{
			name: "別リージョンでの所属リージョンの変更",
			write: func(t *testing.T, account testAccount, token string) {
				expectStatus(t, europe, http.MethodPatch, "/api/me", token, map[string]string{"region": "europe"}, http.StatusOK)
			},
			check: func(t *testing.T, account testAccount, token string) {
				expectStatus(t, asia, http.MethodGet, "/api/me", token, nil, http.StatusForbidden)
				expectStatus(t, europe, http.MethodGet, "/api/me", token, nil, http.StatusOK)
			},
		},
		{
			name: "同じサーバでのログアウト",
			write: func(t *testing.T, account testAccount, token string) {
				expectStatus(t, asia, http.MethodPost, "/api/auth/logout", token, nil, http.StatusNoContent)
			},
			check: func(t *testing.T, account testAccount, token string) {
				expectStatus(t, asia, http.MethodGet, "/api/me", token, nil, http.StatusUnauthorized)
			},
		},
		{
			name: "退会",
			write: func(t *testing.T, account testAccount, token string) {
				expectStatus(t, asia, http.MethodPost, "/api/me/account-deletion", token, nil, http.StatusNoContent)
			},
			check: func(t *testing.T, account testAccount, token string) {
				expectStatus(t, asia, http.MethodGet, "/api/me", token, nil, http.StatusUnauthorized)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := registerAccount(t, asia, "asia")
			token := login(t, asia, account)
			expectStatus(t, asia, http.MethodGet, "/api/me", token, nil, http.StatusOK)

			tt.write(t, account, token)
			tt.check(t, account, token)
		})
	}
}

// DB だけを書き換えると、キャッシュを持つサーバと持たないサーバの違いが応答に現れる。
func TestSessionCacheHoldsOwnRegionUsersOnly(t *testing.T) {
	servers, config := newRegionServers(t)
	asia, europe := servers["asia"], servers["europe"]
	account := registerAccount(t, asia, "asia")
	token := login(t, asia, account)
	expectStatus(t, asia, http.MethodGet, "/api/me", token, nil, http.StatusOK)
	expectStatus(t, europe, http.MethodGet, "/api/me", token, nil, http.StatusForbidden)

	db, err := sqlx.Open("pgx", config.DatabaseDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE login_sessions SET active = false WHERE user_id = (SELECT id FROM users WHERE username = $1)`, account.Username); err != nil {
		t.Fatal(err)
	}

	expectStatus(t, asia, http.MethodGet, "/api/me", token, nil, http.StatusOK)
	expectStatus(t, europe, http.MethodGet, "/api/me", token, nil, http.StatusUnauthorized)
}

// DB から読んでいる間に破棄されたセッションが、キャッシュに残らないこと。
func TestSessionCacheDropsSessionReadDuringPurge(t *testing.T) {
	servers, _ := newRegionServers(t)
	asia, europe := servers["asia"], servers["europe"]
	account := registerAccount(t, asia, "asia")

	for range 30 {
		token := login(t, asia, account)
		stop := make(chan struct{})
		var readers sync.WaitGroup
		for range 8 {
			readers.Go(func() {
				for {
					select {
					case <-stop:
						return
					default:
						requestAPI(t, asia, http.MethodGet, "/api/me", token, nil)
					}
				}
			})
		}
		expectStatus(t, europe, http.MethodPost, "/api/auth/logout", token, nil, http.StatusNoContent)
		close(stop)
		readers.Wait()
		expectStatus(t, asia, http.MethodGet, "/api/me", token, nil, http.StatusUnauthorized)
	}
}

func TestInitializePurgesSessionCacheOfAllServers(t *testing.T) {
	servers, _ := newRegionServers(t)
	if _, err := os.Stat("../sql/init.sh"); err != nil {
		t.Skip(err)
	}
	account := registerAccount(t, servers["europe"], "europe")
	token := login(t, servers["europe"], account)
	expectStatus(t, servers["europe"], http.MethodGet, "/api/me", token, nil, http.StatusOK)

	expectStatus(t, servers["asia"], http.MethodPost, "/api/initialize", "", nil, http.StatusOK)

	expectStatus(t, servers["europe"], http.MethodGet, "/api/me", token, nil, http.StatusUnauthorized)
}
