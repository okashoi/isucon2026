package isureal

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
)

type User struct {
	ID             string  `db:"id" json:"id"`
	Username       string  `db:"username" json:"username"`
	SecretKeyHash  string  `db:"secret_key_hash" json:"-"`
	DisplayName    string  `db:"display_name" json:"displayName"`
	Bio            string  `db:"bio" json:"bio"`
	ProfileImageID *string `db:"profile_image_id" json:"-"`
	Region         string  `db:"region" json:"region"`
	AccountType    string  `db:"account_type" json:"accountType"`
	Status         string  `db:"status" json:"status"`
	ImageKey       []byte  `db:"-" json:"-"`
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

func validUserText(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && length >= minimum && length <= maximum
}

func validRegion(region string) bool {
	switch region {
	case "america", "europe", "asia":
		return true
	default:
		return false
	}
}

var userColumns = []string{"id", "username", "secret_key_hash", "display_name", "bio", "profile_image_id", "region", "account_type", "status"}

func userColumnsAs(alias, prefix string) string {
	columns := make([]string, len(userColumns))
	for i, column := range userColumns {
		columns[i] = fmt.Sprintf(`%s.%s AS "%s.%s"`, alias, column, prefix, column)
	}
	return strings.Join(columns, ", ")
}

func (user *User) setImageKey() error {
	key, err := imageSigningKey(user.SecretKeyHash)
	user.ImageKey = key
	return err
}

var activeUserQuery = `SELECT ` + strings.Join(userColumns, ", ") + ` FROM users WHERE id = $1 AND status = 'active'`

func getUser(ctx context.Context, db sqlx.QueryerContext, userID string) (User, error) {
	return queryUser(ctx, db, activeUserQuery, userID)
}

func lockUser(ctx context.Context, tx *sqlx.Tx, userID string) (User, error) {
	return queryUser(ctx, tx, activeUserQuery+` FOR NO KEY UPDATE`, userID)
}

func queryUser(ctx context.Context, db sqlx.QueryerContext, query, userID string) (User, error) {
	var user User
	if err := sqlx.GetContext(ctx, db, &user, query, userID); err != nil {
		return user, err
	}
	return user, user.setImageKey()
}

// 初期データの username_norm / display_name_norm を normalizeSearchText で埋める。
// PostgreSQL の lower() は Go の cases.Fold + NFC と一致しないので SQL では生成できない。
// initial_users に埋めるのは初回だけで、以降の reset.sql はそこから users へ復元する。
func backfillUserSearchNorms(ctx context.Context, db *sqlx.DB) error {
	var rows []struct {
		ID          string `db:"id"`
		Username    string `db:"username"`
		DisplayName string `db:"display_name"`
	}
	if err := db.SelectContext(ctx, &rows, `SELECT id, username, display_name FROM initial_users WHERE username_norm IS NULL`); err != nil {
		return err
	}
	for chunk := range slices.Chunk(rows, 5000) {
		ids := make([]string, len(chunk))
		usernames := make([]string, len(chunk))
		displayNames := make([]string, len(chunk))
		for i, row := range chunk {
			ids[i] = row.ID
			usernames[i] = normalizeSearchText(row.Username)
			displayNames[i] = normalizeSearchText(row.DisplayName)
		}
		if _, err := db.ExecContext(ctx, `UPDATE initial_users i
			SET username_norm = v.username_norm, display_name_norm = v.display_name_norm
			FROM (SELECT unnest($1::text[]) AS id, unnest($2::text[]) AS username_norm, unnest($3::text[]) AS display_name_norm) v
			WHERE i.id = v.id`, preparedQuery, ids, usernames, displayNames); err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, `UPDATE users u
		SET username_norm = i.username_norm, display_name_norm = i.display_name_norm
		FROM initial_users i
		WHERE u.id = i.id
		  AND (u.username_norm, u.display_name_norm) IS DISTINCT FROM (i.username_norm, i.display_name_norm)`)
	return err
}
