package isureal

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

type createPostRequest struct {
	Image        *multipart.FileHeader `form:"image"`
	Caption      string                `form:"caption"`
	Visibility   string                `form:"visibility"`
	LocationKind string                `form:"location_kind"`
	Latitude     *float64              `form:"latitude"`
	Longitude    *float64              `form:"longitude"`
	Locality     *string               `form:"locality"`
}

type postAuthorResponse struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"displayName"`
	ProfileImageURL *string `json:"profileImageUrl"`
	Bio             string  `json:"bio"`
	AccountType     string  `json:"accountType"`
}

type createPostResponse struct {
	ID              string             `json:"id"`
	Author          postAuthorResponse `json:"author"`
	PostingWindowID string             `json:"postingWindowId"`
	ImageURL        string             `json:"imageUrl"`
	Caption         string             `json:"caption"`
	Visibility      string             `json:"visibility"`
	Location        PostLocation       `json:"location"`
	PostedAt        time.Time          `json:"postedAt"`
	OnTime          bool               `json:"onTime"`
	LateSeconds     int64              `json:"lateSeconds"`
}

func currentWindowEvent(ctx context.Context, h *HandlerContext, region string) (postingWindowEvent, error) {
	if ev, ok := h.Windows.Current(region); ok && ev.ID != "" {
		return ev, nil
	}
	var window PostingWindow
	err := h.ReadDB.GetContext(ctx, &window, `
  SELECT id, region, local_date, starts_at, on_time_deadline, ended_at
  FROM posting_windows WHERE region = $1 AND ended_at IS NULL
 `, region)
	if err != nil {
		return postingWindowEvent{}, err
	}
	ev := postingWindowEvent{
		ID: window.ID, Region: window.Region, StartsAt: window.StartsAt,
		LocalDate: window.LocalDate.Format(time.DateOnly), OnTimeDeadline: window.OnTimeDeadline,
	}
	h.Windows.SetCurrent(ev)
	return ev, nil
}

func createPostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input createPostRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if input.Image == nil || !validUserText(input.Caption, 0, 1000) {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		switch input.Visibility {
		case "friends", "friends_of_friends", "public":
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		location := PostLocation{
			Kind: input.LocationKind, Latitude: input.Latitude,
			Longitude: input.Longitude, Locality: input.Locality,
		}
		if !location.valid() {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		imageFile, err := input.Image.Open()
		if err != nil {
			return err
		}
		defer imageFile.Close()
		imageData, err := io.ReadAll(imageFile)
		if err != nil {
			return err
		}
		width, height, ok := jpegSize(imageData)
		if !ok || width > 4096 || height > 4096 {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}

		ctx := c.Request().Context()
		userID := GetSession(c).UserID
		window, err := currentWindowEvent(ctx, h, GetSession(c).Region)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusConflict, "post_slot_unavailable")
		}
		if err != nil {
			return err
		}
		author, err := getUser(ctx, h.ReadDB, userID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()

		// 画像のバイト列は DB に書かず、このサーバのローカルにだけ置く。
		imageID := uuid.NewString()
		if err := h.PostImages.SaveOrigin(imageID, imageData); err != nil {
			return err
		}
		keepImage := false
		defer func() {
			if !keepImage {
				h.PostImages.Remove(imageID)
			}
		}()

		post := PostWithAuthor{
			Post: Post{
				ID: uuid.NewString(), AuthorID: userID, PostingWindowID: window.ID,
				ImageID: imageID, Caption: input.Caption, Visibility: input.Visibility,
				PostLocation: location, PostedAt: now, OnTimeDeadline: window.OnTimeDeadline,
			},
			Author: author,
		}
		if !h.Active.TryCreate(post, window.LocalDate, now, window.StartsAt) {
			return NewAPIError(http.StatusConflict, "post_slot_unavailable")
		}
		if err := fanoutActivePost(ctx, h, post); err != nil {
			h.Active.Remove(post.ID)
			return err
		}
		keepImage = true
		h.Persist.Enqueue(post, h.Config.Region)
		h.PostImages.ReplicateToPeers(ctx, imageID, imageData)
		return c.JSON(http.StatusCreated, newPostResponse(post.Post, post.Author))
	}
}

func newPostResponse(post Post, user User) createPostResponse {
	response := createPostResponse{
		ID: post.ID,
		Author: postAuthorResponse{
			ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
			ProfileImageURL: newGetMeResponse(user).ProfileImageURL,
			Bio:             user.Bio, AccountType: user.AccountType,
		},
		PostingWindowID: post.PostingWindowID, ImageURL: signedImageURL(user, post.ImageID),
		Caption: post.Caption, Visibility: post.Visibility, Location: post.PostLocation,
		PostedAt: post.PostedAt, OnTime: !post.PostedAt.After(post.OnTimeDeadline),
	}
	if !response.OnTime {
		response.LateSeconds = int64(post.PostedAt.Sub(post.OnTimeDeadline) / time.Second)
	}
	return response
}

type getMyCurrentPostResponse createPostResponse

func getMyCurrentPostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		userID := GetSession(c).UserID
		if post, ok := h.Active.CurrentByAuthor(userID); ok {
			return c.JSON(http.StatusOK, getMyCurrentPostResponse(newPostResponse(post.Post, post.Author)))
		}
		var postID string
		err := h.ReadDB.GetContext(ctx, &postID, `
   SELECT p.id FROM posts p
   JOIN posting_windows w ON w.id = p.posting_window_id
   JOIN users u ON u.id = p.author_id
   JOIN posting_windows current ON current.region = u.region AND current.ended_at IS NULL
                              AND current.local_date = w.local_date
   WHERE p.author_id = $1 AND p.deleted_at IS NULL
  `, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "post_not_found")
		}
		if err != nil {
			return err
		}
		post, err := getPost(ctx, h.ReadDB, postID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "post_not_found")
		}
		if err != nil {
			return err
		}
		user, err := getUser(ctx, h.ReadDB, userID)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, getMyCurrentPostResponse(newPostResponse(post, user)))
	}
}

type getPostRequest struct {
	PostID string `param:"postId"`
}

type getPostResponse createPostResponse

func getPostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getPostRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		post, err := resolvePostWithAuthor(h, ctx, h.ReadDB, input.PostID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "post_not_found")
		}
		if err != nil {
			return err
		}
		viewer, err := loadViewerContext(ctx, h.ReadDB, h.Graph, GetSession(c).UserID, h.Active)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if err := viewer.prepareCanView(ctx, h.ReadDB, post.Post); err != nil {
				return err
			}
		}
		if err != nil || !viewer.canView(post.Post) {
			return NewAPIError(http.StatusNotFound, "post_not_found")
		}
		return c.JSON(http.StatusOK, getPostResponse(newPostResponse(post.Post, post.Author)))
	}
}

type updatePostRequest struct {
	PostID     string  `param:"postId" json:"-"`
	Caption    *string `json:"caption"`
	Visibility *string `json:"visibility"`
}

type updatePostResponse createPostResponse

func updatePostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input updatePostRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if input.Caption == nil && input.Visibility == nil {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		if input.Caption != nil && !validUserText(*input.Caption, 0, 1000) {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		if input.Visibility != nil {
			switch *input.Visibility {
			case "friends", "friends_of_friends", "public":
			default:
				return NewAPIError(http.StatusBadRequest, "invalid_post")
			}
		}
		ctx := c.Request().Context()
		userID := GetSession(c).UserID
		if cached, ok := h.Active.Get(input.PostID); ok {
			if cached.AuthorID != userID {
				return NewAPIError(http.StatusForbidden, "post_not_editable")
			}
			if input.Caption != nil {
				cached.Caption = *input.Caption
			}
			if input.Visibility != nil {
				cached.Visibility = *input.Visibility
			}
			if err := publishActivePost(ctx, h, cached); err != nil {
				return err
			}
			if err := h.Persist.Ensure(ctx, cached.ID); err != nil {
				return err
			}
			if input.Caption != nil {
				if _, err := h.DB.ExecContext(ctx, `UPDATE posts SET caption = $2 WHERE id = $1`, cached.ID, *input.Caption); err != nil {
					return err
				}
			}
			if input.Visibility != nil {
				if _, err := h.DB.ExecContext(ctx, `UPDATE posts SET visibility = $2 WHERE id = $1`, cached.ID, *input.Visibility); err != nil {
					return err
				}
			}
			return c.JSON(http.StatusOK, updatePostResponse(newPostResponse(cached.Post, cached.Author)))
		}
		tx, err := beginLocalTx(ctx, h.DB)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var lockedUserID string
		if err := tx.GetContext(ctx, &lockedUserID, `SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID); err != nil {
			return err
		}
		post, err := getPost(ctx, tx, input.PostID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "post_not_found")
		}
		if err != nil {
			return err
		}
		if post.AuthorID != userID {
			return NewAPIError(http.StatusForbidden, "post_not_editable")
		}
		window, err := getPostingWindow(ctx, tx, post.PostingWindowID)
		if err != nil {
			return err
		}
		if window.EndedAt != nil {
			return NewAPIError(http.StatusForbidden, "post_not_editable")
		}
		if input.Caption != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE posts SET caption = $2 WHERE id = $1`, post.ID, *input.Caption); err != nil {
				return err
			}
		}
		if input.Visibility != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE posts SET visibility = $2 WHERE id = $1`, post.ID, *input.Visibility); err != nil {
				return err
			}
		}
		post, err = getPost(ctx, tx, post.ID)
		if err != nil {
			return err
		}
		user, err := getUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if h.Active.Live() {
			if err := publishActivePost(ctx, h, PostWithAuthor{Post: post, Author: user}); err != nil {
				return err
			}
		}
		return c.JSON(http.StatusOK, updatePostResponse(newPostResponse(post, user)))
	}
}

type deletePostRequest struct {
	PostID string `param:"postId" json:"-"`
}

func deletePostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input deletePostRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		userID := GetSession(c).UserID
		if cached, ok := h.Active.Get(input.PostID); ok {
			if cached.AuthorID != userID {
				return NewAPIError(http.StatusForbidden, "post_not_editable")
			}
			localDate := ""
			if ev, ok := h.Windows.Current(cached.Author.Region); ok && ev.ID == cached.PostingWindowID {
				localDate = ev.LocalDate
			}
			if localDate == "" {
				window, err := getPostingWindow(ctx, h.DB, cached.PostingWindowID)
				if err != nil {
					return err
				}
				localDate = window.LocalDate.Format(time.DateOnly)
			}
			if !h.Active.TryRecordDelete(userID, localDate) {
				return NewAPIError(http.StatusConflict, "post_slot_unavailable")
			}
			h.Persist.MarkDeleted(cached.ID)
			if err := withdrawActivePost(ctx, h, cached.ID); err != nil {
				return err
			}
			_ = h.Persist.Ensure(ctx, cached.ID)
			_, _ = h.DB.ExecContext(ctx, `UPDATE posts SET deleted_at = clock_timestamp() WHERE id = $1 AND deleted_at IS NULL`, cached.ID)
			return c.NoContent(http.StatusNoContent)
		}
		tx, err := beginLocalTx(ctx, h.DB)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var lockedUserID string
		if err := tx.GetContext(ctx, &lockedUserID, `SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID); err != nil {
			return err
		}
		post, err := getPost(ctx, tx, input.PostID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "post_not_found")
		}
		if err != nil {
			return err
		}
		if post.AuthorID != userID {
			return NewAPIError(http.StatusForbidden, "post_not_editable")
		}
		window, err := getPostingWindow(ctx, tx, post.PostingWindowID)
		if err != nil {
			return err
		}
		var deletions int
		if err := tx.GetContext(ctx, &deletions, `
   SELECT count(*) FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
   WHERE p.author_id = $1 AND w.local_date = $2 AND p.deleted_at IS NOT NULL
  `, userID, window.LocalDate); err != nil {
			return err
		}
		if deletions >= 2 {
			return NewAPIError(http.StatusConflict, "post_slot_unavailable")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE posts SET deleted_at = clock_timestamp() WHERE id = $1`, post.ID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		h.Active.RecordDelete(userID, window.LocalDate.Format(time.DateOnly))
		if err := withdrawActivePost(ctx, h, post.ID); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
