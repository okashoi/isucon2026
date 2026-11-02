package isureal

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

type Comment struct {
	ID        string     `db:"id"`
	PostID    string     `db:"post_id"`
	AuthorID  string     `db:"author_id"`
	Body      string     `db:"body"`
	CreatedAt time.Time  `db:"created_at"`
	DeletedAt *time.Time `db:"deleted_at"`
}

type CommentWithAuthor struct {
	Comment
	Author User `db:"author"`
}

// createComment は canComment と同じ判定をペアのロックの下で行って書く。不可なら sql.ErrNoRows を返す。
func createComment(ctx context.Context, db sqlx.QueryerContext, actor, postID, body string) (CommentWithAuthor, error) {
	var comment CommentWithAuthor
	if err := sqlx.GetContext(ctx, db, &comment, `
  SELECT c.id, c.post_id, c.author_id, c.body, c.created_at, c.deleted_at, `+userColumnsAs("u", "author")+`
  FROM create_comment($1, $2, $3) x
  CROSS JOIN LATERAL (SELECT (x.new_comment).*) c
  CROSS JOIN LATERAL (SELECT (x.author).*) u
 `, actor, postID, body); err != nil {
		return comment, err
	}
	return comment, comment.Author.setImageKey()
}

func selectCommentsWithAuthor(ctx context.Context, db sqlx.QueryerContext, conditions string, args ...any) ([]CommentWithAuthor, error) {
	var comments []CommentWithAuthor
	if err := sqlx.SelectContext(ctx, db, &comments, `
  SELECT c.id, c.post_id, c.author_id, c.body, c.created_at, c.deleted_at, `+userColumnsAs("u", "author")+`
  FROM comments c JOIN users u ON u.id = c.author_id AND u.status = 'active'
  WHERE c.deleted_at IS NULL `+conditions, args...); err != nil {
		return nil, err
	}
	for i := range comments {
		if err := comments[i].Author.setImageKey(); err != nil {
			return nil, err
		}
	}
	return comments, nil
}

func canComment(ctx context.Context, db sqlx.QueryerContext, viewerID, postID string) (bool, error) {
	post, err := getPostWithAuthor(ctx, db, postID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	viewer, err := loadViewerContext(ctx, db, viewerID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := viewer.prepareCanView(ctx, db, post.Post); err != nil {
		return false, err
	}
	if !viewer.canView(post.Post) || post.WindowEndedAt != nil {
		return false, nil
	}
	if viewer.ID == post.AuthorID {
		return viewer.HasPostedToday, nil
	}
	if post.Visibility == "public" {
		return true, nil
	}
	return viewer.Friends[post.AuthorID], nil
}
