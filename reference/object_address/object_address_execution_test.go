package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectAddressHeldExecution(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		a := objectAddressCore(t, p, b, c)
		queries := []string{
			"SELECT identity AS v FROM pg_identify_object($1,$2,$3)",
			"SELECT object_names AS v FROM pg_identify_object_as_address($1,$2,$3)",
			"SELECT pg_describe_object($1,$2,$3) AS v",
			"SELECT objsubid AS v FROM pg_get_object_address($1,$2,$3)",
		}
		var held []fixture.HeldStatement
		for _, q := range queries {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q})
		}
		check := func(table, column string) {
			for i, h := range held {
				typ, want := "TEXT", any("public."+table+"."+column)
				args := []any{a.class, a.object, a.sub}
				switch i {
				case 1:
					typ = "_TEXT"
					want = "{public," + table + "," + column + "}"
				case 2:
					want = "column " + column + " of table " + table
				case 3:
					typ = "INT4"
					want = int64(2)
					args = []any{"table column", "{public," + table + "," + column + "}", "{}"}
				}
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(h.SQL, []string{"v"}, []string{typ}, [][]any{{want}}, args...))
			}
		}
		check("addr_probe", "b")
		var old *sql.Rows
		var metadata []*sql.ColumnType
		if !b.PG {
			var err error
			old, err = held[0].Statement.QueryContext(p.Ctx, a.class, a.object, a.sub)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err = old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		objectAddressRun(t, p, b, c, fixture.Exec("ALTER TABLE addr_probe RENAME TO addr_renamed", 0))
		objectAddressRun(t, p, b, c, fixture.Exec("ALTER TABLE addr_renamed RENAME COLUMN b TO renamed_b", 0))
		check("addr_renamed", "renamed_b")
		fresh := objectAddressResolve(t, p, c, "table column", []string{"public", "addr_renamed", "renamed_b"}, nil)
		if fresh != a {
			t.Fatal("rename changed address", a, fresh)
		}
		if old != nil {
			fixture.ScalarRows(t, false, old, []string{"v"}, []string{"TEXT"}, [][]any{{"public.addr_probe.b"}}, false)
			fixture.ScalarAssertDescription(t, metadata, []string{"v"}, []string{"TEXT"})
		}
		objectAddressRun(t, p, b, c, fixture.Exec("DROP TABLE addr_renamed", 0))
		for i := range 3 {
			typ := "TEXT"
			if i == 1 {
				typ = "_TEXT"
			}
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, fixture.Select(held[i].SQL, []string{"v"}, []string{typ}, [][]any{{nil}}, a.class, a.object, a.sub))
		}
		objectAddressRun(t, p, b, c, fixture.Exec("CREATE TABLE addr_renamed(a INT,renamed_b TEXT)", 0))
		fixture.RunStep(t, p.Ctx, held[3], b.Recorder, fixture.Select(held[3].SQL, []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}, "table column", "{public,addr_renamed,renamed_b}", "{}"))
	})
}

func TestReferenceObjectAddressTransactions(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		a := objectAddressCore(t, p, b, c)
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		// DROP COLUMN already has transactional catalog publication; inquiries
		// observe its private tombstone, then rollback restores the live identity.
		objectAddressRun(t, p, b, tx, fixture.Exec("ALTER TABLE addr_probe DROP COLUMN b", 0))
		objectAddressNumeric(t, p, b, tx, a, "table column", "public", "addr_probe", `public.addr_probe."........pg.dropped.2........"`, "{public,addr_probe,........pg.dropped.2........}", "{}", "column ........pg.dropped.2........ of table addr_probe")
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		objectAddressNumeric(t, p, b, c, a, "table column", "public", "addr_probe", "public.addr_probe.b", "{public,addr_probe,b}", "{}", "column b of table addr_probe")
	})
}

func TestReferenceObjectAddressCancellation(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var publicID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&publicID); err != nil {
			t.Fatal(err)
		}
		for _, fn := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_get_object_address", "pg_describe_object"} {
			t.Run(fn, func(t *testing.T) {
				q := "SELECT " + fn + "($1,$2,$3) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				args := []any{int64(2615), publicID, int64(0)}
				typ, want := "RECORD", any("(schema,,public,public)")
				if fn == "pg_identify_object_as_address" {
					want = "(schema,{public},{})"
				}
				if fn == "pg_describe_object" {
					typ = "TEXT"
					want = "schema public"
				}
				if fn == "pg_get_object_address" {
					args = []any{"type", "{integer}", "{}"}
					want = "(1247,23,0)"
				}
				if b.PG {
					fixture.SessionCancel(t, p.Ctx, c, false, true)
					fixture.SessionCancel(t, p.Ctx, c, true, true)
				} else {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					for _, useHeld := range []bool{false, true} {
						var rows *sql.Rows
						if useHeld {
							rows, err = stmt.QueryContext(ctx, args...)
						} else {
							rows, err = c.QueryContext(ctx, q, args...)
						}
						if rows != nil {
							rows.Close()
							t.Fatal("canceled query published rows")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatal("ordinary cancellation", err)
						}
					}
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{want}}, args...))
			})
		}
	})
}
