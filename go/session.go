package isureal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type Session struct {
	Region    string    `db:"region" json:"-"`
	UserID    string    `db:"user_id"`
	TokenHash string    `db:"token_hash"`
	CreatedAt time.Time `db:"created_at"`
}

func GetSession(c *echo.Context) *Session {
	sess, ok := c.Get("session").(*Session)
	if !ok {
		return nil
	}
	return sess
}

func getSessionFromHeader(c *echo.Context, h *HandlerContext) (*Session, error) {
	authHeader := c.Request().Header.Get(echo.HeaderAuthorization)
	if authHeader == "" {
		return nil, nil
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		c.Logger().Warn("Invalid authorization header format(not started with Bearer)")
		return nil, NewAPIError(http.StatusUnauthorized, "invalid_session")
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")

	tokenHashBytes := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(tokenHashBytes[:])
	if session, ok := h.Sessions.Get(tokenHash); ok {
		return session, nil
	}
	generation := h.Sessions.Generation()
	var session Session
	err := h.ReadDB.GetContext(c.Request().Context(), &session, `
		SELECT s.user_id, s.token_hash, s.created_at, u.region
		FROM login_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.active = true AND u.status = 'active'
	`, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	if session.Region == h.Config.Region {
		h.Sessions.Add(generation, session)
	}
	return &session, nil
}

func SessionMiddleware(handlerContext *HandlerContext, authRequired bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			session, err := getSessionFromHeader(c, handlerContext)
			if err != nil {
				return err
			}
			if session == nil && authRequired {
				return NewAPIError(http.StatusUnauthorized, "invalid_session")
			}
			c.Set("session", session)
			return next(c)
		}
	}
}

// InvalidateLoginSession は users 行のロックの下で読んだ所属リージョンを返す。
func InvalidateLoginSession(db *sqlx.DB, userID string, tokenHash string) (string, error) {
	var region string
	if err := db.Get(&region, `SELECT logout_user($1, $2)`, userID, tokenHash); err != nil {
		return "", fmt.Errorf("failed to invalidate sessions: %w", err)
	}
	return region, nil
}

// loginUser は認証に成功したら、有効なセッションを新しいものに置き換えてトークンを返す。
// 認証に失敗したら sql.ErrNoRows を返す。
func loginUser(ctx context.Context, db sqlx.QueryerContext, username, secretKey string) (User, string, error) {
	secretKeyHash := sha256.Sum256([]byte(secretKey))
	token := uuid.NewString()
	tokenHash := sha256.Sum256([]byte(token))
	var user User
	err := sqlx.GetContext(ctx, db, &user, `SELECT `+userColumnList+` FROM login_user($1, $2, $3)`,
		username, hex.EncodeToString(secretKeyHash[:]), hex.EncodeToString(tokenHash[:]))
	return user, token, err
}
