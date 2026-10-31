package isureal

import (
	"os"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// アプリ3台の primary 向け合計が s4 の max_connections（300）に収まるようにする。
// 加点の並列を落とさないよう 100 のままにし、超user予約は運用で監視する。
const maxDBConns = 100

type HandlerContext struct {
	DB         *sqlx.DB // primary（書き込み・TX・initialize）
	ReadDB     *sqlx.DB // 読み取り。ISUREAL_DB_READ_HOST 未設定時は DB と同じ
	Config     *Config
	IPBans     *IPBanList
	Abuse      *AbuseDetector
	Sessions   *SessionCache
	Graph      *GraphCache
	PostImages *PostImageStore
	Windows    *PostingWindowHub
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
		Graph:      NewGraphCache(),
		PostImages: postImages,
		Windows:    NewPostingWindowHub(),
	}
	e.Use(IPBanMiddleware(handlerContext))
	e.Use(AbuseBanMiddleware(handlerContext))

	// 他のアプリサーバが :8080 へ直接送る。nginx は /api/ だけを転送するので、ルーターの公開先からは届かない。
	e.DELETE("/internal/session-cache", purgeAllSessionsHandler(handlerContext))
	e.DELETE("/internal/session-cache/:userId", purgeUserSessionHandler(handlerContext))
	e.DELETE("/internal/graph-cache", purgeAllGraphHandler(handlerContext))
	e.DELETE("/internal/graph-cache/:userId", purgeUserGraphHandler(handlerContext))
	e.DELETE("/internal/image-cache", purgeImageCacheHandler(handlerContext))
	e.DELETE("/internal/abuse-state", purgeAbuseStateHandler(handlerContext))
	e.GET("/internal/images/:imageId", getOriginImageHandler(handlerContext))
	// make post-images/sync が asia1 へ送る。
	e.POST("/internal/post-image-cache", fillPostImageCacheHandler(handlerContext))
	e.PUT("/internal/image-cache/:imageId", putImageCacheHandler(handlerContext))
	e.POST("/internal/posting-window-notify", notifyPostingWindowHandler(handlerContext))

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
