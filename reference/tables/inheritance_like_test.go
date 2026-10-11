package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceLikeComposition(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT DEFAULT 7,x TEXT)", 0)
		s.exec("CREATE TABLE q(a INT DEFAULT 8)", 0)
		s.exec("CREATE TABLE template(a INT DEFAULT 9,z BOOL)", 0)
		*s.notices = nil
		s.fail("CREATE TABLE conflict(lead BIGINT,LIKE template INCLUDING DEFAULTS) INHERITS(p,q)", fixture.Diagnostic{Code: "42611", Message: `column "a" inherits conflicting default values`, Hint: "To resolve the conflict, specify a default explicitly."})
		if want := []fixture.Diagnostic{inheritanceMergeNotice("a"), inheritanceLocalNotice("a", true)}; !reflect.DeepEqual(*s.notices, want) {
			s.t.Fatalf("notices %#v want %#v", *s.notices, want)
		}
		s.absent("conflict")
		s.fail("CREATE TABLE duplicate(LIKE template INCLUDING DEFAULTS,a INT DEFAULT 9) INHERITS(p,q)", fixture.Diagnostic{Code: "42701", Message: `column "a" specified more than once`})
		s.absent("duplicate")
		// Raw explicit DEFAULT resolves the conflict; a deferred LIKE copy does not.
		s.notice("CREATE TABLE resolved(a INT DEFAULT 9) INHERITS(p,q)", inheritanceMergeNotice("a"), inheritanceLocalNotice("a", false))
		s.exec("INSERT INTO resolved DEFAULT VALUES", 1)
		s.values("SELECT * FROM resolved", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), nil}})
		s.notice("CREATE TABLE c(lead BIGINT,LIKE template INCLUDING DEFAULTS) INHERITS(p)", inheritanceLocalNotice("a", true))
		s.exec("INSERT INTO c(lead,z) VALUES(11,true)", 1)
		s.values("SELECT * FROM c", []string{"a", "x", "lead", "z"}, []string{"INT4", "TEXT", "INT8", "BOOL"}, [][]any{{int64(9), nil, int64(11), true}})
		if !s.b.PG {
			inheritanceLikeRoundTrip(s, []fixture.Step{
				fixture.Select("SELECT * FROM c", []string{"a", "x", "lead", "z"}, []string{"INT4", "TEXT", "INT8", "BOOL"}, [][]any{{int64(9), nil, int64(11), true}}),
				fixture.Select("SELECT * FROM resolved", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), nil}}),
			})
			s.exec("INSERT INTO c(lead) VALUES(12)", 1)
			s.values("SELECT a,lead FROM c ORDER BY lead", []string{"a", "lead"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(11)}, {int64(9), int64(12)}})
		}
	})
}

func TestReferenceInheritanceLikeGeneratedConflicts(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE generated_p(a INT,g INT GENERATED ALWAYS AS(a*2) STORED)", 0)
		s.exec("CREATE TABLE generated_q(a INT,g INT GENERATED ALWAYS AS(a*3) STORED)", 0)
		s.exec("CREATE TABLE generated_template(a INT,g INT GENERATED ALWAYS AS(a*4) STORED)", 0)
		*s.notices = nil
		s.fail("CREATE TABLE generated_c(LIKE generated_template INCLUDING GENERATED) INHERITS(generated_p,generated_q)", fixture.Diagnostic{Code: "42611", Message: `column "g" inherits conflicting generation expressions`, Hint: "To resolve the conflict, specify a generation expression explicitly."})
		if want := []fixture.Diagnostic{inheritanceMergeNotice("a"), inheritanceMergeNotice("g"), inheritanceLocalNotice("a", false), inheritanceLocalNotice("g", false)}; !reflect.DeepEqual(*s.notices, want) {
			s.t.Fatalf("notices %#v want %#v", *s.notices, want)
		}
		s.absent("generated_c")
		*s.notices = nil
		s.fail("CREATE TABLE generated_single(LIKE generated_template INCLUDING GENERATED) INHERITS(generated_p)", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop default value for column g of table generated_single because column g of table generated_single requires it", Hint: "You can drop column g of table generated_single instead."})
		if want := []fixture.Diagnostic{inheritanceLocalNotice("a", false), inheritanceLocalNotice("g", false)}; !reflect.DeepEqual(*s.notices, want) {
			s.t.Fatalf("notices %#v want %#v", *s.notices, want)
		}
		s.absent("generated_single")
		s.exec("CREATE TABLE generated_c() INHERITS(generated_p)", 0)
		s.exec("INSERT INTO generated_c(a) VALUES(5)", 1)
		s.values("SELECT * FROM generated_c", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(10)}})
	})
}

func inheritanceLikeRoundTrip(s *inheritanceCase, checks []fixture.Step) {
	s.t.Helper()
	held, err := s.c.QueryContext(s.p.Ctx, checks[0].Query)
	if err != nil {
		s.t.Fatal(err)
	}
	defer held.Close()
	stmt, err := s.c.PrepareContext(s.p.Ctx, "SELECT a FROM c WHERE z=$1")
	if err != nil {
		s.t.Fatal(err)
	}
	defer stmt.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	if err = stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	s.absent("c")
	s.absent("resolved")
	if err = stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	for _, check := range checks {
		s.run(check)
	}
	var a int64
	if err = stmt.QueryRowContext(s.p.Ctx, true).Scan(&a); err != nil || a != 9 {
		s.t.Fatalf("held prepared BOOL result %d %v", a, err)
	}
	for _, suffix := range []string{"; CREATE TABLE bad() INHERITS(missing)", "; CREATE TABLE bad(a TEXT) INHERITS(p)", "; INSERT INTO c(lead) VALUES('invalid bigint')"} {
		if err = stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String()+suffix)); err == nil {
			s.t.Fatal("invalid restore accepted", suffix)
		}
		for _, check := range checks {
			s.run(check)
		}
	}
	canceled, cancel := context.WithCancel(s.p.Ctx)
	cancel()
	if err = stuntgres.Restore(canceled, s.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		s.t.Fatal(err)
	}
	for _, check := range checks {
		s.run(check)
	}
	fixture.RunStep(s.t, s.p.Ctx, fixture.SequenceFrozenRows{Executor: s.c, Rows: held, Query: checks[0].Query}, s.record, checks[0])
	s.edges("c", "p")
	s.edges("resolved", "p", "q")
	if s.p.Postgres != nil {
		if _, err = s.p.Postgres.ExecContext(s.p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			s.t.Fatal(err)
		}
		if _, err = s.p.Postgres.ExecContext(s.p.Ctx, dump.String()); err != nil {
			s.t.Fatal("actual LIKE inheritance dump replay", err)
		}
		for _, check := range checks {
			check.Prepared = s.prepared
			fixture.RunStep(s.t, s.p.Ctx, s.p.Postgres, s.p.PostgresRecord, check)
		}
		check := fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='c'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"a", true, int64(1)}, {"x", false, int64(1)}, {"lead", true, int64(0)}, {"z", true, int64(0)}})
		check.Prepared = s.prepared
		fixture.RunStep(s.t, s.p.Ctx, s.p.Postgres, s.p.PostgresRecord, check)
		if _, err = s.p.Postgres.ExecContext(s.p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			s.t.Fatal(err)
		}
	}
}
