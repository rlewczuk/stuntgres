package unreadview_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

type scenario struct {
	t          *testing.T
	p          *fixture.Pair
	b          fixture.Backend
	conn, peer *sql.Conn
	e          fixture.Executor
	held       bool
}

func backends(t *testing.T, body func(*scenario)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			owner, peer := fixture.LockConnections(t, p.Ctx, backend.DB)
			body(&scenario{t: t, p: p, b: backend, conn: owner, peer: peer, e: owner})
		})
	}
}

func routes(t *testing.T, body func(*scenario, string)) {
	t.Helper()
	for _, mode := range []string{"sql", "go"} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%v", mode, held), func(t *testing.T) {
				backends(t, func(s *scenario) { s.held = held; body(s, mode) })
			})
		}
	}
}

func (s *scenario) step(e fixture.Executor, step fixture.Step) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, e, s.b.Recorder, step)
}

func (s *scenario) run(step fixture.Step) {
	s.t.Helper()
	if !s.held {
		s.step(s.e, step)
		return
	}
	stmt := s.prepare(s.e, step.Query)
	defer stmt.Close()
	s.step(fixture.HeldStatement{Executor: s.e, Statement: stmt, SQL: step.Query}, step)
}

func (s *scenario) exec(query string, affected int64) {
	s.t.Helper()
	s.run(fixture.Exec(query, affected))
}

func (s *scenario) prepare(e fixture.Executor, query string) *sql.Stmt {
	s.t.Helper()
	stmt, err := e.PrepareContext(s.p.Ctx, query)
	if err != nil {
		s.t.Fatalf("prepare %q: %v", query, err)
	}
	s.t.Cleanup(func() { stmt.Close() })
	return stmt
}

func (s *scenario) begin(mode string) func(bool) {
	s.t.Helper()
	if mode == "go" {
		tx, err := s.conn.BeginTx(s.p.Ctx, nil)
		if err != nil {
			s.t.Fatal(err)
		}
		s.e = tx
		s.t.Cleanup(func() { tx.Rollback() })
		return func(commit bool) {
			var err error
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				s.t.Fatalf("Go transaction commit=%v: %v", commit, err)
			}
			s.e = s.conn
		}
	}
	s.exec("BEGIN", 0)
	s.t.Cleanup(func() { s.conn.ExecContext(s.p.Ctx, "ROLLBACK") })
	return func(commit bool) {
		query := "ROLLBACK"
		if commit {
			query = "COMMIT"
		}
		s.exec(query, 0)
		s.e = s.conn
	}
}

func (s *scenario) setup(populated, graph bool) {
	s.t.Helper()
	s.exec("CREATE TYPE oldenum AS ENUM ('active')", 0)
	s.exec("CREATE TABLE t(id INT,state oldenum)", 0)
	s.exec("CREATE VIEW v AS SELECT id FROM t", 0)
	if populated {
		s.exec("INSERT INTO t VALUES(1,'active'),(2,NULL)", 2)
	}
	if graph {
		s.exec("CREATE VIEW w AS SELECT id FROM v", 0)
		s.exec("CREATE TABLE marker(id INT)", 0)
		s.exec("INSERT INTO marker VALUES(1),(2)", 2)
		s.exec("CREATE VIEW j AS SELECT w.id FROM w JOIN marker ON w.id=marker.id", 0)
	}
}

func (s *scenario) replacement() {
	s.t.Helper()
	s.exec("CREATE TYPE replacement AS ENUM ('active','dormant')", 0)
	s.exec("ALTER TABLE t ALTER state TYPE replacement USING state::text::replacement", 0)
	s.exec("DROP TYPE oldenum", 0)
	s.exec("ALTER TYPE replacement RENAME TO oldenum", 0)
}

func ids(view string, values ...int64) fixture.Step {
	rows := make([][]any, len(values))
	for i, value := range values {
		rows[i] = []any{value}
	}
	return fixture.Select("SELECT id FROM "+view+" ORDER BY id", []string{"id"}, []string{"INT4"}, rows)
}

func (s *scenario) graph(values ...int64) {
	s.t.Helper()
	for _, view := range []string{"v", "w", "j"} {
		s.run(ids(view, values...))
	}
	s.nestedLinks()
}

func (s *scenario) heldRead(stmt *sql.Stmt, step fixture.Step) {
	s.t.Helper()
	s.step(fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: step.Query}, step)
}

func (s *scenario) scalar(query string) int64 {
	s.t.Helper()
	rows, err := s.e.QueryContext(s.p.Ctx, query)
	if err != nil {
		s.t.Fatal(query, err)
	}
	defer rows.Close()
	var value int64
	if !rows.Next() {
		s.t.Fatal(query, "missing scalar", rows.Err())
	}
	if err := rows.Scan(&value); err != nil {
		s.t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		s.t.Fatal(query, "non-scalar", rows.Err())
	}
	return value
}

func (s *scenario) oid(name string) int64 {
	s.t.Helper()
	value := s.scalar("SELECT oid::BIGINT FROM pg_type WHERE typname='" + name + "'")
	if value <= 0 {
		s.t.Fatal("invalid type identity", name, value)
	}
	return value
}

// Identity includes source, stable read attribute, visible view type, and rule.
func (s *scenario) identity() []int64 {
	s.t.Helper()
	queries := []string{
		"SELECT oid::BIGINT FROM pg_class WHERE oid='t'::regclass",
		"SELECT attnum::BIGINT FROM pg_attribute WHERE attrelid='t'::regclass AND attname='id'",
		"SELECT oid::BIGINT FROM pg_class WHERE oid='v'::regclass",
		"SELECT atttypid::BIGINT FROM pg_attribute WHERE attrelid='v'::regclass AND attname='id'",
		"SELECT oid::BIGINT FROM pg_rewrite WHERE ev_class='v'::regclass AND rulename='_RETURN'",
	}
	identity := make([]int64, len(queries))
	for i, query := range queries {
		identity[i] = s.scalar(query)
		if identity[i] <= 0 {
			s.t.Fatal("invalid graph identity", identity)
		}
	}
	if identity[3] != 23 || identity[0] == identity[2] {
		s.t.Fatal("invalid source/view relationship", identity)
	}
	return identity
}

func (s *scenario) sameIdentity(want []int64) {
	s.t.Helper()
	if got := s.identity(); !reflect.DeepEqual(got, want) {
		s.t.Fatalf("graph identity got %v want %v", got, want)
	}
}

func (s *scenario) links() {
	s.t.Helper()
	// Actual catalog address edges, not allocator equality or guessed counts.
	for _, query := range []string{
		"SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid JOIN pg_attribute a ON a.attrelid=d.refobjid AND a.attnum=d.refobjsubid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_class'::regclass AND r.ev_class='v'::regclass AND d.refobjid='t'::regclass AND a.attname='id'",
		"SELECT count(*) FROM pg_depend d JOIN pg_attribute a ON a.attrelid=d.objid AND a.attnum=d.objsubid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_type'::regclass AND d.objid='t'::regclass AND a.attname='state' AND d.refobjid='oldenum'::regtype",
	} {
		if s.scalar(query) < 1 {
			s.t.Fatal("missing real dependency", query)
		}
	}
	if got := s.scalar("SELECT atttypid::BIGINT FROM pg_attribute WHERE attrelid='t'::regclass AND attname='state'"); got != s.oid("oldenum") {
		s.t.Fatal("source enum linkage", got)
	}
}

func (s *scenario) definitions() []string {
	s.t.Helper()
	rows, err := s.e.QueryContext(s.p.Ctx, "SELECT definition FROM pg_views WHERE schemaname='public' AND viewname IN ('v','w','j') ORDER BY viewname")
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var definition string
		if err := rows.Scan(&definition); err != nil {
			s.t.Fatal(err)
		}
		result = append(result, definition)
	}
	if rows.Err() != nil || len(result) != 3 {
		s.t.Fatal("view definitions", result, rows.Err())
	}
	return result
}

func (s *scenario) unchanged(wantType int64, wantIdentity []int64, definitions []string) {
	s.t.Helper()
	if s.oid("oldenum") != wantType {
		s.t.Fatal("original enum identity changed")
	}
	s.sameIdentity(wantIdentity)
	if got := s.definitions(); !reflect.DeepEqual(got, definitions) {
		s.t.Fatalf("definitions got %v want %v", got, definitions)
	}
	s.links()
	s.graph(1, 2)
	s.run(fixture.Select("SELECT id,state::text FROM t ORDER BY id", []string{"id", "state"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "active"}, {int64(2), nil}}))
	s.run(fixture.Select("SELECT count(*) FROM pg_type WHERE typname='replacement'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func (s *scenario) failure(query, code, message, detail, hint string) {
	s.t.Helper()
	step := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message, Detail: detail, Hint: hint})
	step.ErrorStage = "execute"
	s.run(step)
}

func (s *scenario) aborted(stmt *sql.Stmt, query string) {
	s.t.Helper()
	step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	step.SelectRows, step.ErrorStage = true, "execute"
	executor := s.e
	if s.held {
		executor = fixture.HeldStatement{Executor: s.e, Statement: stmt, SQL: query}
	}
	s.step(executor, step)
}

func (s *scenario) viewDefinition(name string) string {
	s.t.Helper()
	rows, err := s.e.QueryContext(s.p.Ctx, "SELECT definition FROM pg_views WHERE schemaname='public' AND viewname='"+name+"'")
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		s.t.Fatal("missing view definition", name, rows.Err())
	}
	var definition string
	if err := rows.Scan(&definition); err != nil {
		s.t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		s.t.Fatal("non-scalar view definition", name, rows.Err())
	}
	return definition
}

func (s *scenario) nestedIdentity() []int64 {
	s.t.Helper()
	var result []int64
	for _, view := range []string{"w", "j"} {
		relation := s.scalar("SELECT oid::BIGINT FROM pg_class WHERE oid='" + view + "'::regclass")
		rule := s.scalar("SELECT oid::BIGINT FROM pg_rewrite WHERE ev_class='" + view + "'::regclass AND rulename='_RETURN'")
		if relation <= 0 || rule <= 0 || relation == rule {
			s.t.Fatal("invalid nested identities", view, relation, rule)
		}
		result = append(result, relation, rule)
	}
	return result
}

func (s *scenario) nestedLinks() {
	s.t.Helper()
	for _, edge := range [][2]string{{"w", "v"}, {"j", "w"}, {"j", "marker"}} {
		if s.scalar("SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid JOIN pg_attribute a ON a.attrelid=d.refobjid AND a.attnum=d.refobjsubid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_class'::regclass AND r.ev_class='"+edge[0]+"'::regclass AND d.refobjid='"+edge[1]+"'::regclass AND a.attname='id'") < 1 {
			s.t.Fatal("missing nested dependency address", edge)
		}
	}
}
