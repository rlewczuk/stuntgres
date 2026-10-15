package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL observes the same independently specified final SQL state; only
// native owns Reset/Dump/Restore. No reconstructed fixture stands in for Dump.
func inheritanceMutationRoundTrip(s *inheritanceCase, checks []fixture.Step) {
	for _, step := range checks {
		s.run(step)
	}
	if s.b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	for _, step := range checks {
		s.run(step)
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String()+"; CREATE TABLE broken() INHERITS(missing)")); err == nil {
		s.t.Fatal("invalid restore succeeded")
	}
	for _, step := range checks {
		s.run(step)
	}
	ctx, cancel := context.WithCancel(s.p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, s.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		s.t.Fatal(err)
	}
	for _, step := range checks {
		s.run(step)
	}
	if s.p.Postgres != nil {
		if _, err := s.p.Postgres.ExecContext(s.p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			s.t.Fatal(err)
		}
		// Schema ACLs are external replay prerequisites, not logical Dump data.
		// The fresh schema above removed the scenario's owner-role USAGE.
		for _, role := range s.replayRoles {
			if _, err := s.p.Postgres.ExecContext(s.p.Ctx, "GRANT USAGE ON SCHEMA public TO "+role); err != nil {
				s.t.Fatal(err)
			}
		}
		if _, err := s.p.Postgres.ExecContext(s.p.Ctx, dump.String()); err != nil {
			s.t.Fatal("genuine dump PostgreSQL replay", err)
		}
		for _, step := range checks {
			step.Prepared = s.prepared
			fixture.RunStep(s.t, s.p.Ctx, s.p.Postgres, s.p.PostgresRecord, step)
		}
	}
}

func TestReferenceInheritanceDMLPreparedControls(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT,x TEXT DEFAULT 'p',CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("INSERT INTO p VALUES(1,'parent')", 1)
		const q = "UPDATE p SET a=a+1 RETURNING a,x"
		held, err := s.c.PrepareContext(s.p.Ctx, q)
		if err != nil {
			s.t.Fatal(err)
		}
		defer held.Close()
		run := func(rows [][]any) {
			step := fixture.Select(q, []string{"a", "x"}, []string{"INT4", "TEXT"}, rows)
			step.Bag = true
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: q}, s.record, step)
		}
		run([][]any{{int64(2), "parent"}})
		s.exec("CREATE TABLE q(prefix BOOL)", 0)
		s.exec("CREATE TABLE c(a INT,x TEXT DEFAULT 'child',extra TEXT) INHERITS(q,p)", 0)
		s.exec("INSERT INTO c(prefix,a,extra) VALUES(true,4,'keep')", 1)
		run([][]any{{int64(3), "parent"}, {int64(5), "child"}})
		only, err := s.c.PrepareContext(s.p.Ctx, "UPDATE ONLY p SET a=a+1")
		if err != nil {
			s.t.Fatal(err)
		}
		defer only.Close()
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: only, SQL: "UPDATE ONLY p SET a=a+1"}, s.record, fixture.Exec("UPDATE ONLY p SET a=a+1", 1))
		s.exec("CREATE TABLE g() INHERITS(c)", 0)
		s.exec("INSERT INTO g(prefix,a,extra) VALUES(false,6,'leaf')", 1)
		run([][]any{{int64(5), "parent"}, {int64(6), "child"}, {int64(7), "child"}})
		// Localize a previously inherited slot and retain a local inherited default.
		s.exec("ALTER TABLE ONLY p DROP COLUMN x", 0)
		checks := []fixture.Step{
			fixture.Select("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}, {int64(7)}}),
			fixture.Select("SELECT * FROM ONLY c", []string{"prefix", "a", "x", "extra"}, []string{"BOOL", "INT4", "TEXT", "TEXT"}, [][]any{{true, int64(6), "child", "keep"}}),
			fixture.Select("SELECT * FROM ONLY g", []string{"prefix", "a", "x", "extra"}, []string{"BOOL", "INT4", "TEXT", "TEXT"}, [][]any{{false, int64(7), "child", "leaf"}}),
			fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"prefix", false, int64(1)}, {"a", true, int64(1)}, {"x", true, int64(0)}, {"extra", true, int64(0)}}),
		}
		// The old signature is deliberately not reused after a column-drop signature
		// change. A fresh held handle spans actual replacement with a stable signature.
		const rebound = "UPDATE p SET a=a RETURNING a"
		stable, err := s.c.PrepareContext(s.p.Ctx, rebound)
		if err != nil {
			s.t.Fatal(err)
		}
		defer stable.Close()
		var eager fixture.SequenceFrozenRows
		if !s.b.PG {
			rows, err := s.c.QueryContext(context.WithValue(s.p.Ctx, fixture.RecorderKey{}, s.record), "SELECT a FROM p ORDER BY a")
			if err != nil {
				s.t.Fatal(err)
			}
			defer rows.Close()
			eager = fixture.SequenceFrozenRows{Executor: s.c, Rows: rows, Query: checks[0].Query}
		}
		inheritanceMutationRoundTrip(s, checks)
		step := fixture.Select(rebound, []string{"a"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}, {int64(7)}})
		step.Bag = true
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stable, SQL: rebound}, s.record, step)
		if !s.b.PG {
			fixture.RunStep(s.t, s.p.Ctx, eager, s.record, checks[0])
		}
		const del = "DELETE FROM p WHERE a=$1 RETURNING a"
		deleting, err := s.c.PrepareContext(s.p.Ctx, del)
		if err != nil {
			s.t.Fatal(err)
		}
		defer deleting.Close()
		for _, a := range []int64{6, 5, 7} {
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: deleting, SQL: del}, s.record, fixture.Select(del, []string{"a"}, []string{"INT4"}, [][]any{{a}}, a))
		}
		s.values("SELECT a FROM p", []string{"a"}, []string{"INT4"}, nil)
	})
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		const q = "DELETE FROM p WHERE a=$1 RETURNING a"
		held, err := s.c.PrepareContext(s.p.Ctx, q)
		if err != nil {
			s.t.Fatal(err)
		}
		defer held.Close()
		run := func(rows [][]any) {
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: q}, s.record, fixture.Select(q, []string{"a"}, []string{"INT4"}, rows, int64(99)))
		}
		run(nil)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO c VALUES(99)", 1)
		run([][]any{{int64(99)}})
		const only = "DELETE FROM ONLY p WHERE a=99"
		physical, err := s.c.PrepareContext(s.p.Ctx, only)
		if err != nil {
			s.t.Fatal(err)
		}
		defer physical.Close()
		s.exec("CREATE TABLE g() INHERITS(c)", 0)
		s.exec("INSERT INTO g VALUES(99)", 1)
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: physical, SQL: only}, s.record, fixture.Exec(only, 0))
		run([][]any{{int64(99)}})
		run(nil)
	})
}
