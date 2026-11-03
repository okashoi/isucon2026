package isureal

import (
	"context"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// アプリ3台の primary 向け合計が s4 の max_connections（300、うち 3 本は superuser 用）に収まるようにする。
// asia1 は書き込みと読み取りを別の pool にするので、ISUREAL_DB_MAX_CONNS で pool ごとの本数を下げる。
var maxDBConns = func() int {
	if n, err := strconv.Atoi(os.Getenv("ISUREAL_DB_MAX_CONNS")); err == nil && n > 0 {
		return n
	}
	return 95
}()

type HandlerContext struct {
	DB         *sqlx.DB // primary（書き込み・TX・initialize）
	ReadDB     *sqlx.DB // 読み取り。ISUREAL_DB_READ_HOST 未設定時は DB と同じ
	Config     *Config
	IPBans     *IPBanList
	Abuse      *AbuseDetector
	Sessions   *SessionCache
	PostImages *PostImageStore
	// OffloadDB は Config.DBOffloadHost への接続。つながるまでは nil で、offloadDB が ReadDB を返す。
	OffloadDB atomic.Pointer[sqlx.DB]
}

// offloadDB は s4 から逃がす読み取りの接続先を返す。remote_apply の同期 standby なので
// commit 済みの書き込みは見えるが、synchronous_commit off で書く login_sessions・friend_requests を読むクエリには使わない。
func (h *HandlerContext) offloadDB() *sqlx.DB {
	if db := h.OffloadDB.Load(); db != nil {
		return db
	}
	return h.ReadDB
}

// connectOffloadDB は起動を止めずに背景で接続し、失敗したら 10 秒ごとに再試行する。
func (h *HandlerContext) connectOffloadDB() {
	if h.Config.DBOffloadHost == "" {
		return
	}
	dsn := h.Config.databaseDSN(h.Config.DBOffloadHost)
	go func() {
		for {
			db, err := sqlx.Open("pgx", dsn)
			if err == nil {
				db.SetMaxOpenConns(30)
				db.SetMaxIdleConns(30)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				err = db.PingContext(ctx)
				cancel()
				if err == nil {
					h.OffloadDB.Store(db)
					return
				}
				db.Close()
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

func openDB(dsn string) *sqlx.DB {
	db, err := sqlx.Open("pgx", dsn)
	if err != nil {
		panic("failed to open database: " + err.Error())
	}
	db.SetMaxOpenConns(maxDBConns)
	// 接続の確立は WAN 越しの往復を要するため、確立済みの接続を閉じずに使い回す。
	db.SetMaxIdleConns(maxDBConns)
	if err := db.Ping(); err != nil {
		panic("failed to ping database: " + err.Error())
	}
	return db
}

func NewServer(config *Config) *echo.Echo {
	if !validRegion(config.Region) {
		panic("invalid server region: " + config.Region)
	}
	e := echo.New()
	e.IPExtractor = extractClientIP

	e.Use(ActionLogMiddleware(config.LogLevel, os.Stdout))
	e.Use(middleware.Recover())
	e.HTTPErrorHandler = APIErrorHandler

	db := openDB(config.DatabaseDSN())
	readDB := db
	if config.DBReadHost != "" {
		readDB = openDB(config.DatabaseReadDSN())
	}

	postImages, err := NewPostImageStore(config.Region, config.PeerURLs)
	if err != nil {
		panic("failed to prepare post image directory: " + err.Error())
	}
	handlerContext := &HandlerContext{
		DB:         db,
		ReadDB:     readDB,
		Config:     config,
		IPBans:     &IPBanList{},
		Abuse:      NewAbuseDetector(),
		Sessions:   NewSessionCache(),
		PostImages: postImages,
	}
	handlerContext.connectOffloadDB()
	e.Use(IPBanMiddleware(handlerContext))
	e.Use(AbuseBanMiddleware(handlerContext))

	// 他のアプリサーバが :8080 へ直接送る。nginx は /api/ だけを転送するので、ルーターの公開先からは届かない。
	e.DELETE("/internal/session-cache", purgeAllSessionsHandler(handlerContext))
	e.DELETE("/internal/session-cache/:userId", purgeUserSessionHandler(handlerContext))
	e.DELETE("/internal/image-cache", purgeImageCacheHandler(handlerContext))
	e.DELETE("/internal/abuse-state", purgeAbuseStateHandler(handlerContext))
	e.GET("/internal/images/:imageId", getOriginImageHandler(handlerContext))
	// make post-images/sync が asia1 へ送る。
	e.POST("/internal/post-image-cache", fillPostImageCacheHandler(handlerContext))

	e.POST("/api/initialize", initializeHandler(handlerContext))
	e.POST("/api/auth/register", registerHandler(handlerContext))
	e.POST("/api/auth/login", loginHandler(handlerContext))
	e.POST("/api/auth/logout", logoutHandler(handlerContext), SessionMiddleware(handlerContext, true))
	e.GET("/api/users", searchUsersHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/users/:userId", getUserHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/me/export", exportPostsHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/me", getMeHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.PATCH("/api/me", updateMeHandler(handlerContext), SessionMiddleware(handlerContext, true))
	e.PUT("/api/me/profile-image", putProfileImageHandler(handlerContext), SessionMiddleware(handlerContext, true))
	e.DELETE("/api/me/profile-image", deleteProfileImageHandler(handlerContext), SessionMiddleware(handlerContext, true))
	e.POST("/api/me/account-deletion", deleteAccountHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/posts", createPostHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posting-window/current", getCurrentPostingWindowHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posts/me/current", getMyCurrentPostHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posts/:postId", getPostHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.PATCH("/api/posts/:postId", updatePostHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/posts/:postId", deletePostHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/feeds/:feed", getFeedHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/memories", getMemoriesHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/memories/:date", getMemoriesByDateHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/posts/:postId/comments", createCommentHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posts/:postId/comments", getCommentsHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/comments/:commentId", deleteCommentHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/posts/:postId/realmojis", replaceRealMojiHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/posts/:postId/realmojis", getRealMojisHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/realmojis/:realMojiId", deleteRealMojiHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/images/:imageId", getImageHandler(handlerContext))
	e.GET("/api/notifications", getNotificationsHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/friend-requests", createFriendRequestHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/friend-requests/:requestId/:action", transitionFriendRequestHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.GET("/api/relationships/:kind", getRelationshipsHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/friendships/:userId", deleteFriendshipHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.PUT("/api/follows/:userId", followHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/follows/:userId", unfollowHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.PUT("/api/blocks/:userId", blockHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.DELETE("/api/blocks/:userId", unblockHandler(handlerContext), SessionMiddleware(handlerContext, true), RegionMiddleware(handlerContext))
	e.POST("/api/admin/auth/login", adminLoginHandler(handlerContext))
	e.POST("/api/admin/auth/logout", adminLogoutHandler(handlerContext), AdminSessionMiddleware(handlerContext, true))
	e.PUT("/api/admin/users/:userId/certification", certifyUserHandler(handlerContext), AdminSessionMiddleware(handlerContext, true))
	e.DELETE("/api/admin/users/:userId/certification", deleteUserCertificationHandler(handlerContext), AdminSessionMiddleware(handlerContext, true))
	e.POST("/api/admin/posting-window", openPostingWindowHandler(handlerContext), AdminSessionMiddleware(handlerContext, true))

	return e
}
