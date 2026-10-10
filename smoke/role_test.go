package smoke_test

import (
	"context"
	"database/sql"
	"fmt"
)

func roleCommand(ctx context.Context, c *sql.Conn, query string, count int64, prepared bool) error {
	var result sql.Result
	var err error
	if prepared {
		stmt, prepareErr := c.PrepareContext(ctx, query)
		if prepareErr != nil {
			return prepareErr
		}
		defer stmt.Close()
		result, err = stmt.ExecContext(ctx)
	} else {
		result, err = c.ExecContext(ctx, query)
	}
	if err != nil {
		return err
	}
	got, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if got != count {
		return fmt.Errorf("%s affected %d want %d", query, got, count)
	}
	return nil
}
