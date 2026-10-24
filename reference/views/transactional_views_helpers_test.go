package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Every matrix cell owns its fixture/context. A held route keeps the actual
// Conn-owned handle across BEGIN and executes it without Tx.Stmt repreparation.
func transactionalViewsRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn, string, bool)) {
	t.Helper()
	for _, mode := range []string{"autocommit", "sql", "go"} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%v", mode, held), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"txv_owner", "txv_actor"})
				fixture.SchemaReferenceCleanup(t, "txv_dest")
				fixture.SchemaReferenceCleanup(t, "txv_addon")
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						body(t, p, b, owner, peer, mode, held)
					})
				}
			})
		}
	}
}

func transactionalViewsBegin(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string) (fixture.Executor, func(bool)) {
	t.Helper()
	if mode == "autocommit" {
		return c, func(bool) {}
	}
	if mode == "go" {
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		return tx, func(commit bool) {
			var err error
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK") })
	return c, func(commit bool) {
		q := "ROLLBACK"
		if commit {
			q = "COMMIT"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func transactionalViewsExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, held bool, s fixture.Step) {
	t.Helper()
	if !held {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
		return
	}
	stmt, err := e.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal("held Prepare", s.Query, err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: s.Query}, b.Recorder, s)
}

func transactionalViewsBag(name string, extra bool) fixture.Step {
	rows := [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}}
	if extra {
		rows = append(rows, []any{int64(3), "c"})
	}
	s := fixture.Select("SELECT id,txt FROM "+name, []string{"id", "txt"}, []string{"INT4", "TEXT"}, rows)
	s.Bag = true
	return s
}

func transactionalViewsCount(name string, n int64) fixture.Step {
	return fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='"+name+"'", []string{"count"}, []string{"INT8"}, [][]any{{n}})
}

func transactionalViewsIdentity(t *testing.T, p *fixture.Pair, e fixture.Executor, name string) []int64 {
	t.Helper()
	var relation, rowtype, array, rule int64
	rows, err := e.QueryContext(p.Ctx, "SELECT c.oid::BIGINT,c.reltype::BIGINT,t.typarray::BIGINT,r.oid::BIGINT FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_rewrite r ON r.ev_class=c.oid WHERE c.oid='"+name+"'::regclass AND r.rulename='_RETURN'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing identity", name, rows.Err())
	}
	if err := rows.Scan(&relation, &rowtype, &array, &rule); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("nonunique identity", name, rows.Err())
	}
	if relation <= 0 || rowtype <= 0 || array <= 0 || rule <= 0 {
		t.Fatal("invalid identity", relation, rowtype, array, rule)
	}
	return []int64{relation, rowtype, array, rule}
}
