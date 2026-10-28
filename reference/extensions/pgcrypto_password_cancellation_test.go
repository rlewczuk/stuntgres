package reference_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPasswordCancellation(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var backend fixture.Backend
		for _, b := range p.Backends() {
			if b.PG == pg {
				backend = b
			}
		}
		q := "SELECT crypt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		resume := func() {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{pgcryptoPasswordFoox}}, "foox", pgcryptoPasswordSalt))
		}
		cancelRead := func() {
			fixture.BinaryConversionActiveCancel(t, p, backend, c, q, stuntgres.WorkPasswordBcrypt, func(ctx context.Context) fixture.RelationOutcome {
				rows, err := stmt.QueryContext(ctx, "password", "$2a$31$......................")
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
		}
		cancelRead()
		resume()
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
		cancelRead()
		fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
		resume()
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_password_cancel(id integer,h text)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_password_cancel VALUES(0,'retained')", 1))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SEQUENCE pgcrypto_password_prefix", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_password_cancel_source(id integer)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_password_cancel_source VALUES(2),(1)", 2))
		// Sort the source before projection. The successful first row uses a
		// published completed hash, not bcrypt; only the later row can admit
		// the pgcryptoBcrypt frame. A nontransactional sequence marks the
		// first row and later-row crypto arguments, so the observer waits for
		// both transitions rather than only PostgreSQL's active-query state.
		// The crypt password depends on the source row, preventing a custom
		// PostgreSQL plan from folding the expensive call before execution.
		write := "INSERT INTO pgcrypto_password_cancel(h,id) SELECT CASE WHEN id=1 THEN '" + pgcryptoPasswordFoox + "' ELSE crypt($1||nextval('pgcrypto_password_prefix')::text||id::text,$2) END,CASE WHEN id=1 THEN nextval('pgcrypto_password_prefix')::integer ELSE id END FROM (SELECT id FROM pgcrypto_password_cancel_source ORDER BY id) AS ordered"
		writer, err := c.PrepareContext(p.Ctx, write)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		for attempt, transaction := range []bool{false, true} {
			if transaction {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
			}
			prefix := int64(2 * (attempt + 1))
			admitted := func(ctx context.Context) {
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					var value int64
					var called bool
					if err := backend.DB.QueryRowContext(ctx, "SELECT last_value,is_called FROM pgcrypto_password_prefix").Scan(&value, &called); err != nil {
						t.Fatal("prefix observation", err)
					}
					if called && value == prefix {
						return
					}
					select {
					case <-ctx.Done():
						t.Fatal("successful prefix was not evaluated", value, called, ctx.Err())
					case <-ticker.C:
					}
				}
			}
			fixture.BinaryConversionActiveCancelAfter(t, p, backend, c, write, "pgcryptoBcrypt", admitted, func(ctx context.Context) fixture.RelationOutcome {
				result, err := writer.ExecContext(ctx, "password", "$2a$31$......................")
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			if transaction {
				fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT currval('pgcrypto_password_prefix') AS prefix", []string{"prefix"}, []string{"INT8"}, [][]any{{prefix}}))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id,h FROM pgcrypto_password_cancel ORDER BY id", []string{"id", "h"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "retained"}}))
			resume()
		}
	})
}
