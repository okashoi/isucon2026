package isureal

import (
	"database/sql"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

type replaceRealMojiRequest struct {
	PostID       string                `param:"postId" json:"-"`
	Image        *multipart.FileHeader `form:"image"`
	ReactionType string                `form:"reactionType"`
}

type replaceRealMojiResponse struct {
	ID           string                   `json:"id"`
	Reactor      relationshipUserResponse `json:"reactor"`
	ReactionType string                   `json:"reactionType"`
	ImageURL     string                   `json:"imageUrl"`
	CreatedAt    time.Time                `json:"createdAt"`
}

func newRealMojiResponse(reaction RealMoji, user User) replaceRealMojiResponse {
	return replaceRealMojiResponse{ID: reaction.ID, Reactor: newRelationshipUserResponse(user), ReactionType: reaction.ReactionType, ImageURL: signedImageURL(user, reaction.ImageID), CreatedAt: reaction.CreatedAt}
}

func replaceRealMojiHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input replaceRealMojiRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if input.Image == nil {
			return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
		}
		switch input.ReactionType {
		case "like", "smile", "surprise", "heart", "laugh", "instant":
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
		}
		file, err := input.Image.Open()
		if err != nil {
			return err
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		width, height, ok := jpegSize(data)
		if !ok || width > 512 || height > 512 {
			return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
		}

		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		// 画像のバイト列は DB に書かず、このサーバのローカルにだけ置く。コミットより先に置く（投稿と同じ）。
		imageID := uuid.NewString()
		if err := h.PostImages.SaveOrigin(imageID, data); err != nil {
			return err
		}
		keepImage := false
		defer func() {
			if !keepImage {
				h.PostImages.Remove(imageID)
			}
		}()
		reaction, err := replaceRealMoji(ctx, h.DB, actor, input.PostID, imageID, h.Config.Region, input.ReactionType)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			switch pgErr.Message {
			case "interaction_forbidden":
				return NewAPIError(http.StatusForbidden, "interaction_forbidden")
			case "relationship_not_found":
				return NewAPIError(http.StatusNotFound, "relationship_not_found")
			}
		}
		// ほかの失敗は確定したかどうか分からないので、ここからは画像を消さない。
		keepImage = true
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, newRealMojiResponse(reaction.RealMoji, reaction.Reactor))
	}
}

type getRealMojisRequest struct {
	PostID          string     `param:"postId"`
	BeforeCreatedAt *time.Time `query:"beforeCreatedAt"`
	BeforeID        string     `query:"beforeId"`
}

type getRealMojisResponse struct {
	Items   []replaceRealMojiResponse `json:"items"`
	HasMore bool                      `json:"hasMore"`
}

func getRealMojisHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getRealMojisRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if (input.BeforeCreatedAt == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		allowed, err := canViewPost(ctx, h.ReadDB, GetSession(c).UserID, input.PostID)
		if err != nil {
			return err
		}
		if !allowed {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		reactions, err := selectCurrentRealMojis(ctx, h.ReadDB, `AND r.post_id=$1 AND ($2::timestamptz IS NULL OR (r.created_at,r.id)<($2,$3)) ORDER BY r.created_at DESC,r.id DESC LIMIT 51`, input.PostID, input.BeforeCreatedAt, input.BeforeID)
		if err != nil {
			return err
		}
		items := make([]replaceRealMojiResponse, 0, len(reactions))
		for _, reaction := range reactions {
			items = append(items, newRealMojiResponse(reaction.RealMoji, reaction.Reactor))
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return c.JSON(http.StatusOK, getRealMojisResponse{Items: items, HasMore: hasMore})
	}
}

type deleteRealMojiRequest struct {
	RealMojiID string `param:"realMojiId" json:"-"`
}

func deleteRealMojiHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input deleteRealMojiRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		var ok bool
		err := h.DB.GetContext(ctx, &ok, `WITH t AS (SELECT r.id,(r.reactor_id=$2 OR p.author_id=$2) AS ok FROM realmojis r JOIN posts p ON p.id=r.post_id WHERE r.id=$1 AND r.deleted_at IS NULL FOR UPDATE OF r), u AS (UPDATE realmojis SET deleted_at=clock_timestamp() WHERE id IN (SELECT id FROM t WHERE ok)) SELECT ok FROM t`, input.RealMojiID, actor)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "interaction_not_found")
		}
		if err != nil {
			return err
		}
		if !ok {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		return c.NoContent(http.StatusNoContent)
	}
}
