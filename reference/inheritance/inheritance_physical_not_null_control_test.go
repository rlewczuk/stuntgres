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

func physicalNotNullRestoreConstruction(s *inheritanceCase, script string) {
	s.t.Helper()
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(script)); err != nil {
		s.t.Fatal("closed physical flag import counterpart", err)
	}
}

func physicalNotNullRoundTrip(s *inheritanceCase, kind string, keyed bool, count int, extra []fixture.Step) {
	s.t.Helper()
	rows := make([][]any, count)
	for i := range rows {
		rows[i] = []any{int64(12)}
	}
	keys := [][]any(nil)
	if keyed {
		keys = [][]any{{"c_key"}}
	}
	checks := []fixture.Step{
		fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='c'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='n'", []string{"conname"}, []string{"NAME"}, nil),
		fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='c'::regclass::oid AND contype='p'", []string{"conname"}, []string{"NAME"}, keys),
		fixture.Select("SELECT a FROM ONLY c ORDER BY a", []string{"a"}, []string{kind}, rows),
	}
	enforcement := []fixture.Step{
		fixture.ImplicitError("INSERT INTO c VALUES(NULL)", inheritanceNullDiagnostic("c", "a", "null")),
		fixture.ImplicitError("ALTER TABLE c ALTER a DROP NOT NULL", physicalNotNullMissingFact()),
	}
	if keyed {
		enforcement = append(enforcement, fixture.ImplicitError("INSERT INTO c VALUES(12)", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "c_key"`, Detail: "Key (a)=(12) already exists.", Schema: "public", Table: "c", Constraint: "c_key"}))
	}
	checks = append(checks, extra...)
	physicalNotNullControlRoundTrip(s, checks, enforcement)
}

func physicalNotNullControlRoundTrip(s *inheritanceCase, checks, enforcement []fixture.Step) {
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
		return
	}
	var dump bytes.Buffer
	for range 2 {
		dump.Reset()
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			s.t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "WITH target AS") || !strings.Contains(dump.String(), "removed_dependencies AS") {
			s.t.Fatal("actual physical flag dump omitted constructive cleanup", dump.String())
		}
		if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
			s.t.Fatal(err)
		}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			s.t.Fatal("actual physical flag bytes restore", err)
		}
		observe(s.run)
		if s.p.Postgres != nil {
			if _, err := s.p.Postgres.ExecContext(s.p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				s.t.Fatal(err)
			}
			if _, err := s.p.Postgres.ExecContext(s.p.Ctx, dump.String()); err != nil {
				s.t.Fatal("actual physical flag dump PostgreSQL replay", err, dump.String())
			}
			observe(func(step fixture.Step) {
				step.Prepared = s.prepared
				fixture.RunStep(s.t, s.p.Ctx, s.p.Postgres, s.p.PostgresRecord, step)
			})
		}
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String()+"; INSERT INTO public.c(a) VALUES(NULL)")); err == nil {
		s.t.Fatal("late NULL failure published physical repair")
	}
	observe(s.run)
	ctx, cancel := context.WithCancel(s.p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, s.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		s.t.Fatal("canceled physical repair restore", err)
	}
	observe(s.run)
	if _, err := s.c.ExecContext(s.p.Ctx, physicalNotNullCleanupSQL()); err == nil {
		s.t.Fatal("public catalog CTE deletion admitted")
	}
	observe(s.run)
}

func TestReferenceInheritanceAlterPhysicalNotNullRestoreBoundary(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		// PostgreSQL's positive counterpart and exact dependency cleanup are
		// independently specified by CatalogConstruction; this is native control.
		if s.b.PG {
			return
		}
		s.exec("CREATE TABLE sentinel(a INT)", 0)
		s.exec("INSERT INTO sentinel VALUES(7)", 1)
		prefix := "CREATE TABLE public.c(a BIGINT); ALTER TABLE ONLY public.c ADD CONSTRAINT c_key PRIMARY KEY(a);"
		cleanup := physicalNotNullCleanupSQL() + ";"
		for label, script := range map[string]string{
			"unpaired":             "CREATE TABLE public.c(a BIGINT);" + cleanup,
			"wrong primary":        prefix + strings.ReplaceAll(cleanup, "conname = 'c_key'", "conname = 'wrong'"),
			"wrong attribute":      prefix + strings.ReplaceAll(cleanup, "attname = 'a'", "attname = 'other'"),
			"intervening rows":     prefix + "INSERT INTO public.c VALUES(12);" + cleanup,
			"reused cleanup":       prefix + cleanup + cleanup,
			"unclosed constructor": prefix,
			"dependency predicate": prefix + strings.ReplaceAll(cleanup, "classid = 'pg_catalog.pg_constraint'::regclass AND objid", "classid = 'pg_catalog.pg_class'::regclass AND objid"),
		} {
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(script)); err == nil {
				s.t.Fatal("invalid physical repair accepted", label)
			}
			s.values("SELECT a FROM sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
			s.values("SELECT relname FROM pg_class WHERE relname='c' AND relnamespace='public'::regnamespace::oid", []string{"relname"}, []string{"NAME"}, nil)
		}
	})
}
