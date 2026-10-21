package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Different before/after sets explicitly allow logical replay's dense parent
// ordinals, never relaxed live metadata. Only native has a controls phase; real
// mode additionally executes the actual native bytes on pinned PostgreSQL.
func inheritanceMaintReplay(s *inheritanceCase, before, after []fixture.Step) {
	for _, step := range before {
		s.run(step)
	}
	if s.b.PG {
		return
	}
	var dump bytes.Buffer
	for range 2 {
		dump.Reset()
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			s.t.Fatal(err)
		}
		if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
			s.t.Fatal(err)
		}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			s.t.Fatal("actual maintenance dump restore", err)
		}
		for _, step := range after {
			s.run(step)
		}
		if s.p.Postgres != nil {
			if _, err := s.p.Postgres.ExecContext(s.p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				s.t.Fatal(err)
			}
			if _, err := s.p.Postgres.ExecContext(s.p.Ctx, dump.String()); err != nil {
				s.t.Fatal("actual maintenance bytes PostgreSQL replay", err)
			}
			for _, step := range after {
				step.Prepared = s.prepared
				fixture.RunStep(s.t, s.p.Ctx, s.p.Postgres, s.p.PostgresRecord, step)
			}
		}
	}
	s.exec("CREATE TABLE maintenance_sentinel(a INT)", 0)
	s.exec("INSERT INTO maintenance_sentinel VALUES(91)", 1)
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String()+"; CREATE TABLE broken() INHERITS(missing)")); err == nil {
		s.t.Fatal("late invalid stream published")
	}
	ctx, cancel := context.WithCancel(s.p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, s.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		s.t.Fatal("canceled maintenance restore", err)
	}
	s.values("SELECT a FROM maintenance_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(91)}})
	for _, step := range after {
		s.run(step)
	}
	s.exec("DROP TABLE maintenance_sentinel", 0)
}

func TestReferenceInheritanceMaintControls(t *testing.T) {
	t.Run("AttachedNonprefixRecipes", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT DEFAULT 7,CONSTRAINT ck CHECK(a>0))", 0)
			s.exec("CREATE TABLE c(extra TEXT,a INT DEFAULT 9,CONSTRAINT ck CHECK(a>0))", 0)
			s.exec("INSERT INTO p VALUES(1)", 1)
			s.exec("INSERT INTO c VALUES('local',2)", 1)
			s.exec("ALTER TABLE c INHERIT p", 0)
			checks := []fixture.Step{
				fixture.Select("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
				fixture.Select("SELECT extra,a FROM c", []string{"extra", "a"}, []string{"TEXT", "INT4"}, [][]any{{"local", int64(2)}}),
				fixture.Select("SELECT conname,conislocal,coninhcount FROM pg_constraint WHERE conrelid='c'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"ck", true, int64(1)}}),
			}
			inheritanceMaintReplay(s, checks, checks)
			s.exec("INSERT INTO c(extra) VALUES('future')", 1)
			s.values("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(9)}})
			s.exec("ALTER TABLE c NO INHERIT p", 0)
			s.exec("DROP TABLE p", 0)
			s.values("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(9)}})
		})
	})
	t.Run("SparseLogicalReplay", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			for _, name := range []string{"p1", "p2", "p3", "c"} {
				s.exec("CREATE TABLE "+name+"(a INT)", 0)
			}
			s.exec("ALTER TABLE c INHERIT p1,INHERIT p2,INHERIT p3,NO INHERIT p2", 0)
			s.exec("INSERT INTO c VALUES(4)", 1)
			const q = "SELECT p.relname,i.inhseqno FROM pg_inherits i JOIN pg_class p ON p.oid=i.inhparent WHERE i.inhrelid='c'::regclass::oid ORDER BY i.inhseqno"
			values := fixture.Select("SELECT a FROM p1", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}})
			before := []fixture.Step{values, fixture.Select(q, []string{"relname", "inhseqno"}, []string{"NAME", "INT4"}, [][]any{{"p1", int64(1)}, {"p3", int64(3)}})}
			after := []fixture.Step{values, fixture.Select(q, []string{"relname", "inhseqno"}, []string{"NAME", "INT4"}, [][]any{{"p1", int64(1)}, {"p3", int64(2)}})}
			inheritanceMaintReplay(s, before, after)
			s.exec("ALTER TABLE c NO INHERIT p1,NO INHERIT p3", 0)
			s.edges("c")
			s.exec("ALTER TABLE c INHERIT p2", 0)
			inheritanceMaintOrdinals(s, "c", [][]any{{"p2", int64(1), false}})
		})
	})
	t.Run("DetachedHistoricalLifetime", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))", 0)
			s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
			s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
			s.exec("INSERT INTO c VALUES(2,20)", 1)
			s.exec("INSERT INTO leaf VALUES(3,30)", 1)
			s.exec("ALTER TABLE p DROP a", 0)
			s.exec("ALTER TABLE c NO INHERIT p", 0)
			s.exec("DROP TABLE p", 0)
			checks := []fixture.Step{
				retiredFlags("c", 1), retiredFlags("leaf", 1),
				fixture.Select("SELECT a,b FROM c ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}),
				fixture.Select("SELECT inhseqno FROM pg_inherits WHERE inhrelid='c'::regclass::oid", []string{"inhseqno"}, []string{"INT4"}, nil),
				fixture.ImplicitError("INSERT INTO c(a,b) VALUES(NULL,NULL)", inheritanceNullDiagnostic("c", "a", "null, null")),
				fixture.ImplicitError("INSERT INTO c(a,b) VALUES(0,0)", inheritanceCheckDiagnostic("c", "ck", "0, 0")),
			}
			inheritanceMaintReplay(s, checks, checks)
			s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))", 0)
			s.exec("ALTER TABLE c INHERIT p", 0)
			s.run(retiredFlags("c", 2))
			mixed := []fixture.Step{
				retiredFlags("c", 2), retiredFlags("leaf", 1),
				fixture.Select("SELECT a,b FROM c ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}),
				fixture.ImplicitError("INSERT INTO c(a,b) VALUES(NULL,NULL)", inheritanceNullDiagnostic("c", "a", "null, null")),
				fixture.ImplicitError("INSERT INTO c(a,b) VALUES(0,0)", inheritanceCheckDiagnostic("c", "ck", "0, 0")),
			}
			inheritanceMaintReplay(s, mixed, mixed)
			s.exec("ALTER TABLE c NO INHERIT p", 0)
			s.run(retiredFlags("c", 1))
		})
	})
	t.Run("DetachedTypePhysicalFlag", func(t *testing.T) {
		inheritanceScenario(t, func(s *inheritanceCase) {
			s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))", 0)
			s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
			s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
			s.exec("INSERT INTO c VALUES(2,20)", 1)
			s.exec("INSERT INTO leaf VALUES(3,30)", 1)
			s.exec("ALTER TABLE p DROP a", 0)
			s.exec("ALTER TABLE c NO INHERIT p", 0)
			s.exec("DROP TABLE p", 0)
			s.exec("ALTER TABLE c ALTER a TYPE BIGINT USING a+10", 0)
			checks := []fixture.Step{
				fixture.Select("SELECT a,b FROM c ORDER BY a", []string{"a", "b"}, []string{"INT8", "INT4"}, [][]any{{int64(12), int64(20)}, {int64(13), int64(30)}}),
			}
			for _, name := range []string{"c", "leaf"} {
				checks = append(checks,
					fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='"+name+"'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil),
					fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='"+name+"'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}),
					fixture.ImplicitError("INSERT INTO "+name+"(a,b) VALUES(NULL,NULL)", inheritanceNullDiagnostic(name, "a", "null, null")),
					fixture.Exec("INSERT INTO "+name+"(a,b) VALUES(0,0)", 1),
					fixture.Exec("DELETE FROM ONLY "+name+" WHERE a=0", 1))
			}
			inheritanceMaintReplay(s, checks, checks)
			s.edges("c")
			s.edges("leaf", "c")
		})
	})
}

func TestReferenceInheritanceMaintFeatureSmoke(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT DEFAULT 7,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c(extra TEXT,a INT DEFAULT 9,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE g() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES('child',2)", 1)
		s.exec("INSERT INTO g VALUES('leaf',3)", 1)
		s.exec("ALTER TABLE c INHERIT p", 0)
		s.values("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
		s.values("SELECT a FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
		s.exec("CREATE TABLE bad(extra TEXT)", 0)
		s.fail("ALTER TABLE bad INHERIT p", fixture.Diagnostic{Code: "42804", Message: `child table is missing column "a"`})
		s.edges("bad")
		s.exec("ALTER TABLE p ADD flag INT DEFAULT 7,ADD CONSTRAINT flag_ck CHECK(flag>0)", 0)
		s.fail("ALTER TABLE c DROP flag CASCADE", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited column "flag"`})
		s.exec("ALTER TABLE c NO INHERIT p", 0)
		s.exec("ALTER TABLE p ADD later INT", 0)
		s.values("SELECT attname FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='later' AND NOT attisdropped", []string{"attname"}, []string{"NAME"}, nil)
		checks := []fixture.Step{
			fixture.Select("SELECT a FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select("SELECT extra,a,flag FROM c ORDER BY a", []string{"extra", "a", "flag"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"child", int64(2), int64(7)}, {"leaf", int64(3), int64(7)}}),
		}
		var eager fixture.SequenceFrozenRows
		if !s.b.PG {
			rows, err := s.c.QueryContext(context.WithValue(s.p.Ctx, fixture.RecorderKey{}, s.record), checks[1].Query)
			if err != nil {
				s.t.Fatal(err)
			}
			defer rows.Close()
			eager = fixture.SequenceFrozenRows{Executor: s.c, Rows: rows, Query: checks[1].Query}
		}
		inheritanceMaintReplay(s, checks, checks)
		if !s.b.PG {
			fixture.RunStep(s.t, s.p.Ctx, eager, s.record, checks[1])
		}
		s.exec("INSERT INTO c(extra) VALUES('future')", 1)
		s.values("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(9)}})
		s.fail("INSERT INTO c(extra,a,flag) VALUES('bad',0,0)", inheritanceCheckDiagnostic("c", "ck", "bad, 0, 0"))
		s.exec("DROP TABLE p", 0)
		s.values("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(9)}})
		s.edges("g", "c")
	})
}
