package fixture

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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

// CopyNoticeConnection borrows a genuine stream connection with a case-owned
// notice handler. It shares the pair's native engine or pinned oracle and leaves
// ordinary wrapped SQL consumers on their existing middleware-owned pools.
// Callers inspect records only after the command completes.
func CopyNoticeConnection(t *testing.T, p *Pair, postgres bool, records *[]Diagnostic, handler func(Diagnostic)) *sql.Conn {
	t.Helper()
	deliver := func(d Diagnostic) {
		*records = append(*records, d)
		if handler != nil {
			handler(d)
		}
	}
	var db *sql.DB
	if postgres {
		config, err := pgx.ParseConfig(DSN)
		if err != nil {
			t.Fatal(err)
		}
		config.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
			deliver(ExtractDiagnostic(t, (*pgconn.PgError)(n)))
		}
		db = sql.OpenDB(stdlib.GetConnector(*config))
	} else {
		db = sql.OpenDB(p.NativeConnector.WithNoticeHandler(func(n stuntgres.Error) {
			deliver(ExtractDiagnostic(t, &n))
		}))
	}
	conn, err := db.Conn(p.Ctx)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil && err != sql.ErrConnDone {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return conn
}
