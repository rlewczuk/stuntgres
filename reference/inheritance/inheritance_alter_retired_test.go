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

func retiredFlags(table string, count int64) fixture.Step {
	return fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"ck", false, count, false}, {"nn", false, count, false}})
}
func retiredEnforcement(table, nullRow, checkRow string) []fixture.Step {
	return []fixture.Step{
		fixture.ImplicitError("INSERT INTO "+table+"(a) VALUES(NULL)", inheritanceNullDiagnostic(table, "a", nullRow)),
		fixture.ImplicitError("INSERT INTO "+table+"(a) VALUES(0)", inheritanceCheckDiagnostic(table, "ck", checkRow)),
	}
}

// Genuine bytes, not an equivalent handwritten fixture, exercise both native
// replacement and PostgreSQL replay. Enforcement is checked independently on
// every replay backend, not merely on the original live catalog.
func retiredRoundTrip(s *inheritanceCase, checks, enforcement, afterReplay, pgChecks []fixture.Step) {
	s.t.Helper()
	observe := func(run func(fixture.Step)) {
		for _, step := range checks {
			run(step)
		}
		for _, step := range enforcement {
			run(step)
		}
		for _, step := range checks {
			run(step)
		}
	}
	observe(s.run)
	if s.b.PG {
		for _, step := range pgChecks {
			s.run(step)
		}
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
			s.t.Fatal("actual retired-history restore", err)
		}
		observe(s.run)
		for _, step := range afterReplay {
			s.run(step)
		}
		if s.p.Postgres != nil {
			if _, err := s.p.Postgres.ExecContext(s.p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				s.t.Fatal(err)
			}
			if _, err := s.p.Postgres.ExecContext(s.p.Ctx, dump.String()); err != nil {
				s.t.Fatal("actual retired-history bytes PostgreSQL replay", err)
			}
			pgRun := func(step fixture.Step) {
				step.Prepared = s.prepared
				fixture.RunStep(s.t, s.p.Ctx, s.p.Postgres, s.p.PostgresRecord, step)
			}
			observe(pgRun)
			for _, step := range afterReplay {
				pgRun(step)
			}
			for _, step := range pgChecks {
				pgRun(step)
			}
		}
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String()+"; INSERT INTO public.c(a) VALUES(0)")); err == nil {
		s.t.Fatal("late enforcement failure accepted by isolated importer")
	}
	observe(s.run)
	ctx, cancel := context.WithCancel(s.p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, s.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		s.t.Fatal("canceled history restore", err)
	}
	observe(s.run)
}

func TestReferenceInheritanceAlterRetiredCompaction(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES(1,10)", 1)
		s.exec("INSERT INTO c VALUES(2,20)", 1)
		s.exec("INSERT INTO leaf VALUES(3,30)", 1)
		nn := inheritanceAlterConstraintOID(s, "c", "nn")
		ck := inheritanceAlterConstraintOID(s, "c", "ck")
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		if inheritanceAlterConstraintOID(s, "c", "nn") != nn || inheritanceAlterConstraintOID(s, "c", "ck") != ck {
			s.t.Fatal("column DROP changed retained physical fact identity")
		}
		s.values("SELECT attname,attnum FROM pg_attribute WHERE attrelid='p'::regclass::oid AND attnum>0 AND NOT attisdropped", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"b", int64(2)}})
		checks := []fixture.Step{
			retiredFlags("c", 1), retiredFlags("leaf", 1),
			fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='p'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil),
			fixture.Select("SELECT b FROM p ORDER BY b", []string{"b"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(20)}, {int64(30)}}),
			fixture.Select("SELECT a,b FROM c ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}),
			fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attname", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"a", true, int64(0)}, {"b", false, int64(1)}}),
		}
		// Symmetric failing values keep full physical Detail independently exact
		// even when compaction changes the child layout from a,b to b,a.
		enforcement := []fixture.Step{
			fixture.ImplicitError("INSERT INTO c(a,b) VALUES(NULL,NULL)", inheritanceNullDiagnostic("c", "a", "null, null")),
			fixture.ImplicitError("INSERT INTO c(a,b) VALUES(0,0)", inheritanceCheckDiagnostic("c", "ck", "0, 0")),
			fixture.ImplicitError("INSERT INTO leaf(a,b) VALUES(NULL,NULL)", inheritanceNullDiagnostic("leaf", "a", "null, null")),
			fixture.ImplicitError("INSERT INTO leaf(a,b) VALUES(0,0)", inheritanceCheckDiagnostic("leaf", "ck", "0, 0")),
		}
		afterReplay := []fixture.Step{
			fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='p'::regclass::oid AND attnum>0 AND NOT attisdropped", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"b", int64(1)}}),
		}
		retiredRoundTrip(s, checks, enforcement, afterReplay, nil)
	})
}

func TestReferenceInheritanceAlterRetiredSiblingTypes(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE q(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c() INHERITS(p,q)", 0)
		s.exec("CREATE TABLE r(a INT) INHERITS(p)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("INSERT INTO r VALUES(3)", 1)
		s.exec("INSERT INTO leaf VALUES(4)", 1)
		retainedNN := inheritanceAlterConstraintOID(s, "c", "nn")
		retainedCK := inheritanceAlterConstraintOID(s, "c", "ck")
		retiredNN := inheritanceAlterConstraintOID(s, "r", "nn")
		retiredCK := inheritanceAlterConstraintOID(s, "r", "ck")
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		s.exec("ALTER TABLE r ALTER a TYPE BIGINT USING a+10", 0)
		// PostgreSQL's TYPE cleanup drops dependent inherited-only facts and
		// reconstructs them through live parent recursion, not through history.
		// p.a is gone: r's historical-only facts therefore genuinely disappear.
		inheritanceAlterFacts(s, "r", nil)
		s.run(fixture.Select("SELECT conname FROM pg_constraint WHERE oid=$1 OR oid=$2", []string{"conname"}, []string{"NAME"}, nil, retiredNN, retiredCK))
		// Supply actual new local providers on the now-BIGINT sibling. This is not
		// a repair of r's old historical counts. c remains the old-history survivor.
		s.exec("ALTER TABLE r ADD CONSTRAINT nn NOT NULL a,ADD CONSTRAINT ck CHECK(a>0)", 0)
		if inheritanceAlterConstraintOID(s, "r", "nn") == retiredNN || inheritanceAlterConstraintOID(s, "r", "ck") == retiredCK {
			s.t.Fatal("new local TYPE-era facts reused removed historical identities")
		}
		if inheritanceAlterConstraintOID(s, "c", "nn") != retainedNN || inheritanceAlterConstraintOID(s, "c", "ck") != retainedCK {
			s.t.Fatal("sibling TYPE cleanup changed untouched old-history survivor")
		}
		checks := []fixture.Step{
			retiredFlags("c", 2), retiredFlags("leaf", 1),
			fixture.Select("SELECT conname,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='r'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "BOOL", "INT2", "BOOL"}, [][]any{{"ck", true, int64(0), false}, {"nn", true, int64(0), false}}),
			fixture.Select("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}),
			fixture.Select("SELECT a FROM r", []string{"a"}, []string{"INT8"}, [][]any{{int64(13)}}),
			fixture.Select("SELECT count(*) AS n FROM p", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}),
			fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attnum>0 AND NOT attisdropped", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"a", false, int64(1)}}),
			fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='r'::regclass::oid AND attnum>0 AND NOT attisdropped", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"a", true, int64(0)}}),
		}
		enforcement := append(retiredEnforcement("c", "null", "0"), retiredEnforcement("r", "null", "0")...)
		enforcement = append(enforcement, retiredEnforcement("leaf", "null", "0")...)
		retiredRoundTrip(s, checks, enforcement, nil, nil)
	})
}

func TestReferenceInheritanceAlterRetiredLifetimes(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE c(a INT) INHERITS(p)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("INSERT INTO leaf VALUES(3)", 1)
		ownNN := inheritanceAlterConstraintOID(s, "c", "nn")
		ownCK := inheritanceAlterConstraintOID(s, "c", "ck")
		firstNN := inheritanceAlterConstraintOID(s, "p", "nn")
		firstCK := inheritanceAlterConstraintOID(s, "p", "ck")
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		s.exec("ALTER TABLE p ADD COLUMN a INT DEFAULT 7,ADD CONSTRAINT nn NOT NULL a,ADD CONSTRAINT ck CHECK(a>0)", 0)
		if inheritanceAlterConstraintOID(s, "p", "nn") == firstNN || inheritanceAlterConstraintOID(s, "p", "ck") == firstCK {
			s.t.Fatal("readd reused removed parent provider identity")
		}
		s.run(retiredFlags("c", 2))
		s.run(retiredFlags("leaf", 1))
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		if inheritanceAlterConstraintOID(s, "c", "nn") != ownNN || inheritanceAlterConstraintOID(s, "c", "ck") != ownCK {
			s.t.Fatal("repeated DROP reminted retained physical fact")
		}
		checks := []fixture.Step{
			retiredFlags("c", 2), retiredFlags("leaf", 1),
			fixture.Select("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}),
			fixture.Select("SELECT count(*) AS n FROM p", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}),
		}
		retiredRoundTrip(s, checks, append(retiredEnforcement("c", "null", "0"), retiredEnforcement("leaf", "null", "0")...), nil, nil)
	})
}

func TestReferenceInheritanceAlterRetiredMixedValidity(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))", 0)
		s.exec("CREATE TABLE q(a INT)", 0)
		s.exec("CREATE TABLE c(a INT) INHERITS(p,q)", 0)
		s.exec("CREATE TABLE leaf() INHERITS(c)", 0)
		s.exec("INSERT INTO q VALUES(NULL),(-1)", 2)
		s.exec("INSERT INTO c VALUES(2)", 1)
		s.exec("INSERT INTO leaf VALUES(3)", 1)
		s.exec("ALTER TABLE p DROP COLUMN a", 0)
		s.exec("ALTER TABLE q ADD CONSTRAINT nn NOT NULL a NOT VALID,ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0)
		checks := []fixture.Step{
			retiredFlags("c", 2), retiredFlags("leaf", 1),
			fixture.Select("SELECT conname,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='q'::regclass::oid ORDER BY conname", []string{"conname", "definition"}, []string{"NAME", "TEXT"}, [][]any{{"ck", "CHECK ((a > 0)) NOT VALID"}, {"nn", "NOT NULL a NOT VALID"}}),
			fixture.Select("SELECT conname,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='c'::regclass::oid ORDER BY conname", []string{"conname", "definition"}, []string{"NAME", "TEXT"}, [][]any{{"ck", "CHECK ((a > 0))"}, {"nn", "NOT NULL a"}}),
			fixture.Select("SELECT a FROM ONLY q ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, [][]any{{int64(-1)}, {nil}}),
			fixture.Select("SELECT a FROM c ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}),
		}
		enforcement := append(retiredEnforcement("c", "null", "0"), retiredEnforcement("q", "null", "0")...)
		enforcement = append(enforcement, retiredEnforcement("leaf", "null", "0")...)
		pgChecks := []fixture.Step{
			fixture.Select("SELECT conname,convalidated,conenforced FROM pg_constraint WHERE conrelid='c'::regclass::oid ORDER BY conname", []string{"conname", "convalidated", "conenforced"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"ck", true, true}, {"nn", true, true}}),
			fixture.Select("SELECT conname,convalidated,conenforced FROM pg_constraint WHERE conrelid='q'::regclass::oid ORDER BY conname", []string{"conname", "convalidated", "conenforced"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"ck", false, true}, {"nn", false, true}}),
		}
		retiredRoundTrip(s, checks, enforcement, nil, pgChecks)
	})
}
