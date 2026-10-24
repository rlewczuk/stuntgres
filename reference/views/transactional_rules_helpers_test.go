package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Each cell has an independent deadline. Utilities are prepared before BEGIN,
// including commands whose objects do not exist yet, and retained until cleanup.
func transactionalRulesRoutes(t *testing.T, explicitOnly bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn, string, bool)) {
	t.Helper()
	for _, mode := range []string{"autocommit", "sql", "go"} {
		if explicitOnly && mode == "autocommit" {
			continue
		}
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%v", mode, held), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						body(t, p, b, c, peer, mode, held)
					})
				}
			})
		}
	}
}

func transactionalRulesHandles(t *testing.T, p *fixture.Pair, c *sql.Conn, held bool, queries ...string) map[string]*sql.Stmt {
	t.Helper()
	handles := make(map[string]*sql.Stmt)
	if held {
		for _, q := range queries {
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("Prepare must be effect-free", q, err)
			}
			t.Cleanup(func() { s.Close() })
			handles[q] = s
		}
	}
	return handles
}

func transactionalRulesRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, handles map[string]*sql.Stmt) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) {
		var x fixture.Executor = e
		if h := handles[s.Query]; h != nil {
			x = fixture.HeldStatement{Executor: e, Statement: h, SQL: s.Query}
		}
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	}
}

func transactionalRulesSetup(run func(fixture.Step)) {
	for _, q := range []string{"CREATE TABLE src(id INT,v INT)", "CREATE TABLE sink1(id INT,v INT)", "CREATE TABLE sink2(id INT,v INT)", "CREATE TABLE other(id INT,v INT)", "CREATE RULE r AS ON INSERT TO other DO NOTHING"} {
		run(fixture.Exec(q, 0))
	}
}

const transactionalRulesCreate = "CREATE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink1 VALUES(NEW.id,abs(NEW.v))"
const transactionalRulesReplace = "CREATE OR REPLACE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink2 VALUES(NEW.id,NEW.v+1)"
const transactionalRulesRename = "ALTER RULE r ON src RENAME TO renamed"
const transactionalRulesDrop = "DROP RULE renamed ON src"
const transactionalRulesInsert = "INSERT INTO src VALUES($1,$2)"

func transactionalRulesNames(names ...string) fixture.Step {
	rows := make([][]any, 0, len(names))
	for _, n := range names {
		rows = append(rows, []any{n})
	}
	return fixture.Select("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass ORDER BY rulename", []string{"rulename"}, []string{"NAME"}, rows)
}
func transactionalRulesRows(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
}
func transactionalRulesOID(t *testing.T, p *fixture.Pair, e fixture.Executor, name string) int64 {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT oid::BIGINT FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename=$1", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if !rows.Next() {
		t.Fatal("missing rule", name, rows.Err())
	}
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || rows.Next() || rows.Err() != nil {
		t.Fatal("invalid rule identity", oid, rows.Err())
	}
	return oid
}
func transactionalRulesSameOID(t *testing.T, p *fixture.Pair, e fixture.Executor, name string, want int64) {
	t.Helper()
	if got := transactionalRulesOID(t, p, e, name); got != want {
		t.Fatalf("rule OID %d want %d", got, want)
	}
}
func transactionalRulesEdge(sink string, n int64) fixture.Step {
	return fixture.Select("SELECT count(*) FROM (SELECT DISTINCT d.refobjid FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_class'::regclass AND r.ev_class='src'::regclass AND d.refobjid='"+sink+"'::regclass) edges", []string{"count"}, []string{"INT8"}, [][]any{{n}})
}
