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

// lockUserPair は同じ二人の関係操作・コメント・RealMoji を直列化する。
// 相手の users 行はロックしない。
func lockUserPair(ctx context.Context, db execerContext, a, b string) error {
	if _, err := db.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::bigint)`, userPairAdvisoryKey(a, b)); err != nil {
		return err
	}
	return nil
}

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
