package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const definitionClassItems = `OPERATOR 1 pg_catalog.<, OPERATOR 2 pg_catalog.<=, OPERATOR 3 pg_catalog.=, OPERATOR 4 pg_catalog.>=, OPERATOR 5 pg_catalog.>, FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`

// Each serial route owns a fresh 30-second scenario, not an extended context.
func definitionScenario(t *testing.T, names []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	clean := fixture.AccessMethodReferenceCleanup(t, names)
	p := fixture.NewScenario(t)
	clean()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK"); c.ExecContext(p.Ctx, "RESET ROLE"); c.Close() })
			body(t, p, b, c)
		})
	}
}

func definitionRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
}

func definitionCount(query string, n int64, args ...any) fixture.Step {
	return fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{n}}, args...)
}

func definitionID(t *testing.T, p *fixture.Pair, e fixture.Executor, q string, args ...any) int64 {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	var id int64
	if !rows.Next() {
		t.Fatal("missing identity", q, rows.Err())
	}
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id <= 0 || rows.Next() || rows.Err() != nil {
		t.Fatal("invalid identity", q, id, rows.Err())
	}
	return id
}

func definitionHeld(t *testing.T, p *fixture.Pair, e fixture.Executor, q string) fixture.HeldStatement {
	t.Helper()
	stmt, err := e.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("quiet Prepare", q, err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
}

type definitionRoute struct {
	mode         string
	held, commit bool
}

func definitionRoutes(t *testing.T, body func(*testing.T, definitionRoute)) {
	t.Helper()
	for _, mode := range []string{"autocommit", "sql", "go"} {
		for _, held := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				if mode == "autocommit" && !commit {
					continue
				}
				r := definitionRoute{mode, held, commit}
				t.Run(fmt.Sprintf("%s/held_%t/commit_%t", mode, held, commit), func(t *testing.T) { body(t, r) })
			}
		}
	}
}

func (r definitionRoute) begin(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) (fixture.Executor, func()) {
	t.Helper()
	run := definitionRun(t, p, b, c)
	if r.mode == "autocommit" {
		return c, func() {}
	}
	if r.mode == "sql" {
		run(fixture.Exec("BEGIN", 0))
		return c, func() {
			end := "ROLLBACK"
			if r.commit {
				end = "COMMIT"
			}
			run(fixture.Exec(end, 0))
		}
	}
	tx, err := c.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	return tx, func() {
		var err error
		if r.commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Preparation can precede BEGIN; execution always uses the retained handle.
func (r definitionRoute) commands(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queries []string) func(fixture.Executor, string) {
	t.Helper()
	held := map[string]fixture.HeldStatement{}
	if r.held {
		for _, q := range queries {
			held[q] = definitionHeld(t, p, c, q)
		}
	}
	return func(e fixture.Executor, q string) {
		if r.held {
			h, ok := held[q]
			if !ok {
				t.Fatal("unprepared route", q)
			}
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Exec(q, 0))
		} else {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
		}
	}
}

func definitionAborted() fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
}

// Single-key scenarios enumerate the complete class dependency bag, including
// unexpected extra providers, rather than probing only for the expected edge.
func definitionIndexProvider(index, class, method int64) fixture.Step {
	return fixture.Select(`SELECT c.oid::bigint AS index_oid,d.refobjid::bigint AS class_oid,d.deptype,c.relam::bigint AS method_oid,d.objsubid,d.refobjsubid FROM pg_depend d JOIN pg_class c ON c.oid=d.objid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_opclass'::regclass AND d.objid=$1::oid ORDER BY d.refobjid,d.objsubid,d.refobjsubid`, []string{"index_oid", "class_oid", "deptype", "method_oid", "objsubid", "refobjsubid"}, []string{"INT8", "INT8", "CHAR", "INT8", "INT4", "INT4"}, [][]any{{index, class, "n", method, int64(0), int64(0)}}, index)
}
