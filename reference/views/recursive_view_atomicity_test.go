package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRecursiveViewAtomicity(t *testing.T) {
	recursiveViewReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE SEQUENCE rv_draw", 0),
			fixture.Exec("CREATE RECURSIVE VIEW rv_draw_view(n) AS SELECT nextval('rv_draw')", 0),
			recursiveViewRows("SELECT nextval('rv_draw') AS n", "INT8", 1),
			recursiveViewRows("SELECT n FROM rv_draw_view", "INT8", 2),
			recursiveViewRows("SELECT nextval('rv_draw') AS n", "INT8", 3),
			fixture.Exec("CREATE RECURSIVE VIEW rv_spin(n) AS SELECT 1 UNION ALL SELECT n FROM rv_spin", 0),
			recursiveViewRows("SELECT n FROM rv_spin LIMIT 3", "INT4", 1, 1, 1),
			fixture.Exec("CREATE TABLE rv_atomic_sink(n INT,v INT)", 0),
			fixture.Exec("INSERT INTO rv_atomic_sink VALUES(99,99)", 1),
			fixture.Exec("CREATE RECURSIVE VIEW rv_late(n,v) AS SELECT 1,1 UNION ALL SELECT n+1,1/(3-n) FROM rv_late WHERE n<4", 0),
		} {
			run(s)
		}
		q := "SELECT sum(n) AS total FROM rv_spin"
		for _, prepared := range []bool{false, true} {
			var x fixture.Executor = c
			if prepared {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			fixture.BinaryConversionActiveCancel(t, p, b, c, q, stuntgres.WorkRecursiveUnion, func(ctx context.Context) fixture.RelationOutcome {
				if b.PG {
					result, err := x.ExecContext(ctx, q)
					return fixture.RelationOutcome{Result: result, Err: err}
				}
				rows, err := x.QueryContext(ctx, q)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			// Re-execute the SAME held consumer after a compatible finite replacement.
			run(fixture.Exec("CREATE OR REPLACE RECURSIVE VIEW rv_spin(n) AS SELECT 1 UNION ALL SELECT n+1 FROM rv_spin WHERE n<2", 0))
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"total"}, []string{"INT8"}, recursiveViewInts(3)))
			run(recursiveViewRows("SELECT 7 AS n", "INT4", 7))
			run(fixture.Exec("CREATE OR REPLACE RECURSIVE VIEW rv_spin(n) AS SELECT 1 UNION ALL SELECT n FROM rv_spin", 0))
		}
		insert := "INSERT INTO rv_atomic_sink SELECT n,n FROM rv_spin"
		fixture.BinaryConversionActiveCancel(t, p, b, c, insert, stuntgres.WorkRecursiveUnion, func(ctx context.Context) fixture.RelationOutcome {
			result, err := c.ExecContext(ctx, insert)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		sentinel := fixture.Select("SELECT n,v FROM rv_atomic_sink", []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}})
		run(sentinel)
		division := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		for _, prepared := range []bool{false, true} {
			q := "SELECT n,v FROM rv_late"
			var x fixture.Executor = c
			if prepared {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			rows, err := x.QueryContext(p.Ctx, q)
			if rows != nil {
				if !b.PG {
					rows.Close()
					t.Fatal("native failed recursive read exposed Rows")
				}
				for rows.Next() {
				}
				if err == nil {
					err = rows.Err()
				}
				rows.Close()
			}
			fixture.AssertDiagnostic(t, err, &division)
			s := fixture.ErrorDiagnostic("INSERT INTO rv_atomic_sink SELECT n,v FROM rv_late", division)
			s.Prepared = prepared
			run(s)
			run(sentinel)
		}
		if !b.PG {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := c.ExecContext(canceled, "CREATE RECURSIVE VIEW rv_canceled(n) AS SELECT 1")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(result, err)
			}
			run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname='rv_canceled'", []string{"relname"}, []string{"NAME"}, nil))
			detached, err := c.QueryContext(p.Ctx, "SELECT n FROM rv_draw_view")
			if err != nil {
				t.Fatal(err)
			}
			defer detached.Close()
			dump := fixture.ViewDump(t, p)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE TABLE replacement(n INT); CREATE RECURSIVE VIEW broken(n) AS SELECT missing FROM replacement;")); err == nil {
				t.Fatal("invalid Restore accepted")
			}
			during, stop := context.WithCancel(p.Ctx)
			defer stop()
			if err := stuntgres.Restore(during, p.Native, &fixture.CatalogControlCancelReader{Cancel: stop}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !bytes.Equal(dump, fixture.ViewDump(t, p)) {
				t.Fatal("failed Restore changed persistent state")
			}
			run(sentinel)
			run(recursiveViewRows("SELECT n FROM rv_draw_view", "INT8", 5))
			var n int64
			if !detached.Next() {
				t.Fatal(detached.Err())
			}
			if err := detached.Scan(&n); err != nil || n != 4 {
				t.Fatal(n, err)
			}
			if detached.Next() || detached.Err() != nil {
				t.Fatal("detached rows changed", detached.Err())
			}
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop sequence rv_draw because other objects depend on it")
			want.Detail = "view rv_draw_view depends on sequence rv_draw"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(fixture.ErrorDiagnostic("DROP SEQUENCE rv_draw", want))
		}
	})
}
