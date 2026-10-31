package isureal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

// execerContext はトランザクション中のロック取得に使う。
type execerContext interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// リージョン在籍（登録・リージョン変更・退会・投稿期間の開始）を直列化する。
// 投稿やコメントは在籍を変えないので、このロックは取らない。
// pg_advisory_xact_lock(int, int) は bigint 版とロック空間が別なので、ペアロックと衝突しない。
func lockRegionMemberships(ctx context.Context, db execerContext, regions ...string) error {
	keys, err := regionAdvisoryKeys(regions)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := db.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::integer, $2::integer)`, int32(1), key); err != nil {
			return err
		}
	}
	return nil
}

func regionAdvisoryKey(region string) (int32, bool) {
	switch region {
	case "america":
		return 1, true
	case "europe":
		return 2, true
	case "asia":
		return 3, true
	default:
		return 0, false
	}
}

func regionAdvisoryKeys(regions []string) ([]int32, error) {
	keys := make([]int32, 0, len(regions))
	seen := make(map[int32]struct{}, len(regions))
	for _, region := range regions {
		key, ok := regionAdvisoryKey(region)
		if !ok {
			return nil, fmt.Errorf("unknown region %q", region)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys, nil
}

// lockUserPair は同じ二人の関係操作・コメント・RealMoji を直列化し、二人の users 行を FOR KEY SHARE でロックする。
// FOR KEY SHARE は退会の FOR UPDATE とだけ衝突し、ログインや投稿の FOR NO KEY UPDATE とは衝突しない。
// 二人とも active なら true を返す。
// この文のスナップショットは advisory lock を待つ前に決まるので、関係の確認は後続の文で行う。
func lockUserPair(ctx context.Context, tx *sqlx.Tx, a, b string) (bool, error) {
	var ids []string
	err := tx.SelectContext(ctx, &ids, `
  WITH pair AS MATERIALIZED (SELECT 1 FROM pg_advisory_xact_lock($1::bigint))
  SELECT u.id FROM pair, users u
  WHERE u.id IN ($2, $3) AND u.status = 'active'
  ORDER BY u.id
  FOR KEY SHARE OF u
 `, userPairAdvisoryKey(a, b), a, b)
	return len(ids) == 2, err
}

// userPairAdvisoryKey を変えるときは sql/reset.sql の user_pair_advisory_key も同じ値になるように変える。
func userPairAdvisoryKey(a, b string) int64 {
	if a > b {
		a, b = b, a
	}
	sum := sha256.Sum256([]byte(a + "\x00" + b))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
