package isureal

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// execLocalSync は SET LOCAL synchronous_commit = local の明示トランザクションで query を 1 往復で実行する。
// standby への remote_apply 待ちを払わず、bytea は extended protocol の binary のまま送る。
func execLocalSync(ctx context.Context, db *sqlx.DB, query string, paramOIDs []uint32, args ...any) error {
	if len(paramOIDs) != len(args) {
		return fmt.Errorf("paramOID count %d != args %d", len(paramOIDs), len(args))
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	return conn.Raw(func(driverConn any) error {
		c := driverConn.(*stdlib.Conn).Conn()
		var eqb pgx.ExtendedQueryBuilder
		sd := &pgconn.StatementDescription{ParamOIDs: paramOIDs}
		if err := eqb.Build(c.TypeMap(), sd, args); err != nil {
			return err
		}

		pipeline := c.PgConn().StartPipeline(ctx)
		defer pipeline.Close()

		pipeline.SendQueryParams(`begin`, nil, nil, nil, nil)
		pipeline.SendQueryParams(`set local synchronous_commit to local`, nil, nil, nil, nil)
		pipeline.SendQueryParams(query, eqb.ParamValues, paramOIDs, eqb.ParamFormats, eqb.ResultFormats)
		pipeline.SendQueryParams(`commit`, nil, nil, nil, nil)
		if err := pipeline.Sync(); err != nil {
			return err
		}
		for {
			results, err := pipeline.GetResults()
			if err != nil {
				return err
			}
			if results == nil {
				break
			}
			if rr, ok := results.(*pgconn.ResultReader); ok {
				if _, err := rr.Close(); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
