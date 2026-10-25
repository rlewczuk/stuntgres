package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func pgcryptoRandomRange(q string, args ...any) fixture.Step {
	return fixture.ImplicitError(q, fixture.Diagnostic{Code: "39000", Message: "Length not in range"}, args...)
}

func TestReferencePgcryptoRandomDiagnostics(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoRandomInstall(t, p, c, r)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("SET search_path TO pgcrypto_part,public", 0))
		q := "SELECT gen_random_bytes($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, n := range []int64{-2147483648, -1, 0, 1025, 2147483647} {
			for _, s := range []fixture.Step{pgcryptoRandomRange(fmt.Sprintf("SELECT gen_random_bytes(%d)", n)), pgcryptoRandomRange(q, n)} {
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Prepared(s))
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, pgcryptoRandomRange(q, n))
		}
		for _, v := range []struct{ value, code, message string }{
			{"oops", "22P02", `invalid input syntax for type integer: "oops"`},
			{"2147483648", "22003", `value "2147483648" is out of range for type integer`},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				pos := "25"
				if prefix != "" {
					pos = "31"
				}
				s := fixture.ImplicitError(prefix+"SELECT gen_random_bytes('"+v.value+"')", fixture.Diagnostic{Code: v.code, Message: v.message, Position: pos})
				s.ErrorStage = "execute"
				run(s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				run(s)
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: v.code, Message: v.message, Context: "unnamed portal parameter $1 = '...'"}, v.value)
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, s)
		}
		run(fixture.Exec("CREATE TABLE pgcrypto_random_atomic(id integer,b bytea)", 0))
		run(fixture.Exec("INSERT INTO pgcrypto_random_atomic VALUES(9,'\\x0080ff'::bytea)", 1))
		write := pgcryptoRandomRange("INSERT INTO pgcrypto_random_atomic SELECT n,gen_random_bytes(n) FROM (VALUES (1),(0)) AS source(n)")
		check := fixture.Select("SELECT id,b FROM pgcrypto_random_atomic ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(9), string([]byte{0, 128, 255})}})
		run(write)
		run(check)
		run(fixture.Exec("BEGIN", 0))
		run(write)
		run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK", 0))
		run(check)
		// The native adapter guarantees precancellation. A fast PostgreSQL
		// result can win lib/pq's cancellation race and leave connection health
		// uncertain; this is not a PostgreSQL server-cancellation witness.
		if pg {
			return
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				rows, err = stmt.QueryContext(ctx, 16)
			} else {
				rows, err = c.QueryContext(ctx, q, 16)
			}
			if rows != nil {
				rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("pre-cancelled call", err)
			}
		}
	})
}
