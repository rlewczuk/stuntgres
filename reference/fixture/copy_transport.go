package fixture

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5/stdlib"
	"stuntgres"
)

// CopyFrom runs the public native API or the pinned oracle's genuine streaming
// transport. Oracle callers supply a connection from Pair.CopyPostgres.
func CopyFrom(ctx context.Context, conn *sql.Conn, postgres bool, query string, input io.Reader) (stuntgres.CopyResult, error) {
	if !postgres {
		return stuntgres.CopyFrom(ctx, conn, query, input)
	}
	var result stuntgres.CopyResult
	err := conn.Raw(func(raw any) error {
		c, ok := raw.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected oracle COPY connection %T", raw)
		}
		tag, err := c.Conn().PgConn().CopyFrom(ctx, input, query)
		if err == nil {
			result = stuntgres.CopyResult{Count: tag.RowsAffected(), CommandTag: tag.String()}
		}
		return err
	})
	return result, err
}
