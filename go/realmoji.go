package isureal

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
)

type RealMoji struct {
	ID           string     `db:"id"`
	PostID       string     `db:"post_id"`
	ReactorID    string     `db:"reactor_id"`
	ImageID      string     `db:"image_id"`
	ReactionType string     `db:"reaction_type"`
	CreatedAt    time.Time  `db:"created_at"`
	ReplacedAt   *time.Time `db:"replaced_at"`
	DeletedAt    *time.Time `db:"deleted_at"`
}

// replaceRealMoji は replace_realmoji を 1 文で呼ぶ。失敗は *pgconn.PgError の Message で返る。
// 画像の行も同じ文で書く。バイト列は imageOrigin のリージョンのサーバのローカルにある。
func replaceRealMoji(ctx context.Context, db sqlx.QueryerContext, actor, postID, imageID, imageOrigin, reactionType string) (RealMojiWithReactor, error) {
	var reaction RealMojiWithReactor
	err := sqlx.GetContext(ctx, db, &reaction, `
  SELECT r.id, r.post_id, r.reactor_id, r.image_id, r.reaction_type, r.created_at, r.replaced_at, r.deleted_at,
         `+userColumnsAs("u", "reactor")+`
  FROM replace_realmoji($1, $2, $3, $4, $5) c
  CROSS JOIN LATERAL (SELECT (c.reaction).*) r
  CROSS JOIN LATERAL (SELECT (c.reactor).*) u
 `, actor, postID, imageID, imageOrigin, reactionType)
	if err != nil {
		return reaction, err
	}
	return reaction, reaction.Reactor.setImageKey()
}

type RealMojiWithReactor struct {
	RealMoji
	Reactor User `db:"reactor"`
}

func selectCurrentRealMojis(ctx context.Context, db sqlx.QueryerContext, conditions string, args ...any) ([]RealMojiWithReactor, error) {
	var reactions []RealMojiWithReactor
	if err := sqlx.SelectContext(ctx, db, &reactions, `
  SELECT r.id, r.post_id, r.reactor_id, r.image_id, r.reaction_type, r.created_at, r.replaced_at, r.deleted_at,
         `+userColumnsAs("u", "reactor")+`
  FROM realmojis r JOIN users u ON u.id = r.reactor_id AND u.status = 'active'
  WHERE r.replaced_at IS NULL AND r.deleted_at IS NULL `+conditions, args...); err != nil {
		return nil, err
	}
	for i := range reactions {
		if err := reactions[i].Reactor.setImageKey(); err != nil {
			return nil, err
		}
	}
	return reactions, nil
}
