package isureal

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type getImageRequest struct {
	ImageID string `param:"imageId" json:"-"`
	Exp     string `query:"exp" json:"-"`
	Sig     string `query:"sig" json:"-"`
}

func getImageHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		var input getImageRequest
		if err := c.Bind(&input); err != nil {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		query, err := url.ParseQuery(c.Request().URL.RawQuery)
		if err != nil || len(query["exp"]) != 1 || len(query["sig"]) != 1 {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		exp, err := strconv.ParseInt(input.Exp, 10, 64)
		if err != nil || strconv.FormatInt(exp, 10) != input.Exp || exp <= time.Now().Unix() {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		sig, err := base64.RawURLEncoding.Strict().DecodeString(input.Sig)
		if err != nil || len(sig) != sha256.Size || base64.RawURLEncoding.EncodeToString(sig) != input.Sig {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}

		ctx := c.Request().Context()
		// 削除状態を毎回確認する。即時反映も5秒以内の許容範囲に含まれる。
		// バイト列はローカルから返すので、DB からは鍵と取得の可否だけを 1 往復で引く。
		meta, err := loadImageMeta(ctx, h.ReadDB, input.ImageID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if err != nil {
			return err
		}
		// 削除状態を毎回確認する。即時反映も5秒以内の許容範囲に含まれる。
		// パーティションのある image_objects は引かず、所有者と参照元だけを 1 往復で引く。
		var object struct {
			SecretKeyHash string `db:"secret_key_hash"`
			Visible       bool   `db:"visible"`
		}
		// 可視の結果だけを imageVisibleTTL のあいだ使う。削除の拒否は 5 秒以内でよい。
		if v, ok := imageVisibleCache.Load(input.ImageID); ok && time.Since(v.(imageVisible).fetchedAt) < imageVisibleTTL {
			object.SecretKeyHash, object.Visible = v.(imageVisible).secretKeyHash, true
		} else if err = h.ReadDB.GetContext(ctx, &object, `
   SELECT u.secret_key_hash, (
     COALESCE(u.profile_image_id = $2, false)
     OR EXISTS (
       SELECT 1 FROM posts p
       WHERE p.image_id = $2 AND p.author_id = u.id AND p.deleted_at IS NULL
     )
     OR EXISTS (
       SELECT 1 FROM realmojis r
       JOIN posts p ON p.id = r.post_id AND p.deleted_at IS NULL
       JOIN users author ON author.id = p.author_id AND author.status = 'active'
       WHERE r.image_id = $2 AND r.reactor_id = u.id AND r.replaced_at IS NULL AND r.deleted_at IS NULL
     )
   ) AS visible
   FROM users u
   WHERE u.id = $1 AND u.status = 'active'
  `, preparedQuery, meta.OwnerUserID, input.ImageID); err == nil && object.Visible {
			imageVisibleCache.Store(input.ImageID, imageVisible{object.SecretKeyHash, time.Now()})
		}
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if err != nil {
			return err
		}
		key, err := imageSigningKey(object.SecretKeyHash)
		if err != nil {
			return err
		}
		if !hmac.Equal(sig, imageSignature(key, input.ImageID, exp)) {
			c.Set("abuse", "invalid_signature")
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if !object.Visible {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}

		// 署名を確かめてから読むので、不正な署名ではバイト列を DB から運ばない。
		data, err := h.PostImages.Load(ctx, h.ReadDB, input.ImageID, meta.OriginRegion)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if err != nil {
			return err
		}
		if exp <= time.Now().Unix() {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		return c.Blob(http.StatusOK, meta.MediaType, data)
	}
}

// imageMeta は image_objects の行のうち、作成後に変わらない列。
type imageMeta struct {
	OwnerUserID  string  `db:"owner_user_id"`
	MediaType    string  `db:"media_type"`
	OriginRegion *string `db:"origin_region"`
}

// imageMetaCache は画像 ID ごとの imageMeta。image_objects は created_at のパーティションが 36 個あり、
// id だけの検索は全パーティションをロックして LockManager の待ちになる。行は初期化でしか消えない。
var imageMetaCache sync.Map

// imageVisibleCache は画像 ID ごとの、可視と判定した時刻と所有者の secret_key_hash。
// secret_key_hash は変わらないので、署名は毎回このハッシュで確かめる。
// standby の遅延 (≤1s) を足しても 5 秒以内に収まる TTL にする。
var imageVisibleCache sync.Map

const imageVisibleTTL = 3 * time.Second

type imageVisible struct {
	secretKeyHash string
	fetchedAt     time.Time
}

func clearImageMetaCache() {
	imageMetaCache.Clear()
	imageVisibleCache.Clear()
}

func loadImageMeta(ctx context.Context, db sqlx.QueryerContext, imageID string) (imageMeta, error) {
	if meta, ok := imageMetaCache.Load(imageID); ok {
		return meta.(imageMeta), nil
	}
	var meta imageMeta
	if err := sqlx.GetContext(ctx, db, &meta, `SELECT owner_user_id, media_type, origin_region FROM image_objects WHERE id = $1`, preparedQuery, imageID); err != nil {
		return meta, err
	}
	imageMetaCache.Store(imageID, meta)
	return meta, nil
}
