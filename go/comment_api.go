package isureal

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

type createCommentRequest struct {
	PostID string `param:"postId" json:"-"`
	Body   string `json:"body"`
}

type createCommentResponse struct {
	ID        string                   `json:"id"`
	Author    relationshipUserResponse `json:"author"`
	Body      string                   `json:"body"`
	CreatedAt time.Time                `json:"createdAt"`
}

func newCommentResponse(comment Comment, user User) createCommentResponse {
	return createCommentResponse{ID: comment.ID, Author: newRelationshipUserResponse(user), Body: comment.Body, CreatedAt: comment.CreatedAt}
}

func createCommentHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input createCommentRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if !validUserText(input.Body, 1, 1000) {
			return NewAPIError(http.StatusBadRequest, "invalid_interaction")
		}
		ctx := c.Request().Context()
		comment, err := createComment(ctx, h.DB, GetSession(c).UserID, input.PostID, input.Body)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, newCommentResponse(comment.Comment, comment.Author))
	}
}

type getCommentsRequest struct {
	PostID         string     `param:"postId"`
	AfterCreatedAt *time.Time `query:"afterCreatedAt"`
	AfterID        string     `query:"afterId"`
}

type getCommentsResponse struct {
	Items   []createCommentResponse `json:"items"`
	HasMore bool                    `json:"hasMore"`
}

func getCommentsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getCommentsRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if (input.AfterCreatedAt == nil) != (input.AfterID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		allowed, err := canComment(ctx, h.ReadDB, GetSession(c).UserID, input.PostID)
		if err != nil {
			return err
		}
		if !allowed {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		comments, err := selectCommentsWithAuthor(ctx, h.ReadDB, `AND c.post_id=$1 AND ($2::timestamptz IS NULL OR (c.created_at,c.id)>($2,$3)) ORDER BY c.created_at,c.id LIMIT 51`, input.PostID, input.AfterCreatedAt, input.AfterID)
		if err != nil {
			return err
		}
		items := make([]createCommentResponse, 0, len(comments))
		for _, comment := range comments {
			items = append(items, newCommentResponse(comment.Comment, comment.Author))
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return c.JSON(http.StatusOK, getCommentsResponse{Items: items, HasMore: hasMore})
	}
}

type deleteCommentRequest struct {
	CommentID string `param:"commentId" json:"-"`
}

func deleteCommentHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input deleteCommentRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		var ok bool
		err := h.DB.GetContext(ctx, &ok, `WITH t AS (SELECT c.id,(c.author_id=$2 OR p.author_id=$2) AS ok FROM comments c JOIN posts p ON p.id=c.post_id WHERE c.id=$1 AND c.deleted_at IS NULL FOR UPDATE OF c), u AS (UPDATE comments SET deleted_at=clock_timestamp() WHERE id IN (SELECT id FROM t WHERE ok)) SELECT ok FROM t`, input.CommentID, actor)
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
