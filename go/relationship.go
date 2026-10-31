package isureal

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
)

func validateRelationshipTarget(actor, target string) error {
	if actor == target || target == "" {
		return NewAPIError(http.StatusBadRequest, "invalid_relationship")
	}
	return nil
}

func beginLocalTx(ctx context.Context, db *sqlx.DB) (*sqlx.Tx, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('synchronous_commit', 'local', true)`); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func beginRelationshipTx(ctx context.Context, db *sqlx.DB, actor, target string) (*sqlx.Tx, error) {
	if err := validateRelationshipTarget(actor, target); err != nil {
		return nil, err
	}
	tx, err := beginLocalTx(ctx, db)
	if err != nil {
		return nil, err
	}
	active, err := lockUserPair(ctx, tx, actor, target)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if !active {
		tx.Rollback()
		return nil, NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	return tx, nil
}

type friendRequestResult struct {
	Result     string     `db:"result"`
	RelationID *string    `db:"relation_id"`
	Status     *string    `db:"relation_status"`
	UpdatedAt  *time.Time `db:"relation_updated_at"`
}

func createFriendRequest(ctx context.Context, db sqlx.QueryerContext, actor, target string) (friendRequestResult, error) {
	var result friendRequestResult
	err := sqlx.GetContext(ctx, db, &result, `SELECT result, relation_id, relation_status, relation_updated_at FROM create_friend_request($1, $2, $3::bigint)`, actor, target, userPairAdvisoryKey(actor, target))
	return result, err
}

func transitionFriendRequest(ctx context.Context, db sqlx.QueryerContext, actor, requestID, status string) (friendRequestResult, error) {
	var result friendRequestResult
	err := sqlx.GetContext(ctx, db, &result, `SELECT result, relation_id, relation_status, relation_updated_at FROM transition_friend_request($1, $2, $3)`, actor, requestID, status)
	return result, err
}

func isBlocked(ctx context.Context, db sqlx.QueryerContext, actor, target string) (bool, error) {
	var blocked bool
	err := sqlx.GetContext(ctx, db, &blocked, `SELECT EXISTS(SELECT 1 FROM block_relations WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1))`, actor, target)
	return blocked, err
}

func requireRelationshipChange(result sql.Result, err error, status int, code string) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return NewAPIError(status, code)
	}
	return nil
}
