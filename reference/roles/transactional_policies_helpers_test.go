package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type transactionalPolicies struct {
	t       *testing.T
	p       *fixture.Pair
	b       fixture.Backend
	c, peer *sql.Conn
	x       fixture.Executor
	held    bool
}

func transactionalPolicyCases(t *testing.T, body func(*transactionalPolicies)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"tp_reader", "tp_group", "tp_other", "tp_private"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			s := &transactionalPolicies{t: t, p: p, b: b, c: c, peer: peer, x: c}
			defer func() {
				// A Go transaction owns c until Rollback returns, including after
				// Fatal unwinds body; release it before issuing physical cleanup.
				if tx, ok := s.x.(*sql.Tx); ok {
					tx.Rollback()
					s.x = c
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				c.ExecContext(ctx, "ROLLBACK")
				c.ExecContext(ctx, "RESET ROLE")
				peer.ExecContext(ctx, "ROLLBACK")
				peer.ExecContext(ctx, "RESET ROLE")
			}()
			for _, q := range []string{
				"CREATE ROLE tp_reader", "CREATE ROLE tp_group", "CREATE ROLE tp_other",
				"GRANT USAGE ON SCHEMA public TO tp_reader,tp_group,tp_other",
				"GRANT tp_group TO tp_reader WITH INHERIT TRUE, SET TRUE",
				"CREATE TABLE t(id INT,allowed BOOL)",
				"CREATE TABLE u(id INT,allowed BOOL)",
				"GRANT SELECT,INSERT,UPDATE,DELETE ON t,u TO tp_reader,tp_group,tp_other",
				"ALTER TABLE t ENABLE ROW LEVEL SECURITY",
				"ALTER TABLE u ENABLE ROW LEVEL SECURITY",
			} {
				s.exec(q)
			}
			s.run(fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3))
			body(s)
		})
	}
}

func (s *transactionalPolicies) run(step fixture.Step) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, s.x, s.b.Recorder, step)
}
func (s *transactionalPolicies) exec(q string) { s.t.Helper(); s.run(fixture.Exec(q, 0)) }
func (s *transactionalPolicies) ddl(q string) {
	s.t.Helper()
	if !s.held {
		s.exec(q)
		return
	}
	stmt, err := s.x.PrepareContext(s.p.Ctx, q)
	if err != nil {
		s.t.Fatal("quiet utility Prepare", q, err)
	}
	defer stmt.Close()
	fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.x, Statement: stmt, SQL: q}, s.b.Recorder, fixture.Exec(q, 0))
}
func (s *transactionalPolicies) ids(ids ...int64) {
	s.t.Helper()
	s.exec("SET ROLE tp_reader")
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	s.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
	s.exec("RESET ROLE")
}
func (s *transactionalPolicies) oid(table, name string) int64 {
	s.t.Helper()
	rows, err := s.x.QueryContext(s.p.Ctx, "SELECT classid,objid,objsubid FROM pg_get_object_address('policy',ARRAY['public',$1,$2],ARRAY[]::text[])", table, name)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var class, oid, sub int64
	if !rows.Next() {
		s.t.Fatal("missing policy address", rows.Err())
	}
	if err := rows.Scan(&class, &oid, &sub); err != nil {
		s.t.Fatal(err)
	}
	if class != 3256 || oid <= 0 || sub != 0 || rows.Next() || rows.Err() != nil {
		s.t.Fatal("invalid policy address", class, oid, sub, rows.Err())
	}
	return oid
}
func (s *transactionalPolicies) sameOID(table, name string, want int64) {
	s.t.Helper()
	if got := s.oid(table, name); got != want {
		s.t.Fatalf("policy identity %s.%s: %d != %d", table, name, got, want)
	}
}
func (s *transactionalPolicies) failure(q, code, message string) {
	s.t.Helper()
	step := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	step.Prepared = s.held
	step.ErrorStage = "execute"
	s.run(step)
}
func (s *transactionalPolicies) begin(mode string) func(bool) {
	s.t.Helper()
	if mode == "go" {
		tx, err := s.c.BeginTx(s.p.Ctx, nil)
		if err != nil {
			s.t.Fatal(err)
		}
		s.x = tx
		return func(commit bool) {
			var err error
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				s.t.Fatal(err)
			}
			s.x = s.c
		}
	}
	s.exec("BEGIN")
	return func(commit bool) {
		if commit {
			s.exec("COMMIT")
		} else {
			s.exec("ROLLBACK")
		}
	}
}
func transactionalPolicyMatrix(t *testing.T, modes []string, body func(*transactionalPolicies, string)) {
	t.Helper()
	for _, mode := range modes {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%v", mode, held), func(t *testing.T) {
				transactionalPolicyCases(t, func(s *transactionalPolicies) { s.held = held; body(s, mode) })
			})
		}
	}
}
