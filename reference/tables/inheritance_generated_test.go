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

func TestReferenceInheritanceGeneratedMerges(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT,g INT GENERATED ALWAYS AS(a*2) STORED)", 0)
		s.notice("CREATE TABLE plain(g INT) INHERITS(p)", inheritanceLocalNotice("g", true))
		s.notice("CREATE TABLE overridden(g INT GENERATED ALWAYS AS(a*3) STORED) INHERITS(p)", inheritanceLocalNotice("g", true))
		s.exec("CREATE TABLE q(a INT,g INT GENERATED ALWAYS AS(a*3) STORED)", 0)
		s.notice("CREATE TABLE resolved(g INT GENERATED ALWAYS AS(a*4) STORED) INHERITS(p,q)", inheritanceMergeNotice("a"), inheritanceMergeNotice("g"), inheritanceLocalNotice("g", true))
		for _, tc := range []struct {
			name  string
			value int64
		}{{"plain", 8}, {"overridden", 12}, {"resolved", 16}} {
			s.exec("INSERT INTO "+tc.name+"(a) VALUES(4)", 1)
			s.values("SELECT * FROM "+tc.name, []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(4), tc.value}})
			s.fail("INSERT INTO "+tc.name+"(a,g) VALUES(4,99)", fixture.Diagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "g"`, Detail: `Column "g" is a generated column.`})
		}
		if !s.b.PG {
			checks := []fixture.Step{
				fixture.Select("SELECT * FROM plain", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(8)}}),
				fixture.Select("SELECT * FROM overridden", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(12)}}),
				fixture.Select("SELECT * FROM resolved", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(16)}}),
			}
			held, err := s.c.QueryContext(context.WithValue(s.p.Ctx, fixture.RecorderKey{}, s.record), checks[0].Query)
			if err != nil {
				s.t.Fatal(err)
			}
			defer held.Close()
			stmt, err := s.c.PrepareContext(s.p.Ctx, "SELECT * FROM resolved")
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
			for _, name := range []string{"plain", "overridden", "resolved"} {
				s.absent(name)
			}
			if err = stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			for _, check := range checks {
				s.run(check)
			}
			for _, suffix := range []string{"; CREATE TABLE bad() INHERITS(missing)", "; CREATE TABLE bad(g TEXT) INHERITS(p)", "; INSERT INTO resolved(a,g) VALUES(4,99)"} {
				if err = stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String()+suffix)); err == nil {
					s.t.Fatal("invalid generated restore accepted", suffix)
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
			// Retain both an eager result and a prepared statement across the real
			// replacement, using this scenario's generated relations and shape.
			frozen := checks[0]
			fixture.RunStep(s.t, s.p.Ctx, fixture.SequenceFrozenRows{Executor: s.c, Rows: held, Query: frozen.Query}, s.record, frozen)
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: checks[2].Query}, s.record, checks[2])
			for _, name := range []string{"plain", "overridden", "resolved"} {
				s.constraints(name, nil)
				s.fail("INSERT INTO "+name+"(a,g) VALUES(4,99)", fixture.Diagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "g"`, Detail: `Column "g" is a generated column.`})
			}
			s.edges("plain", "p")
			s.edges("overridden", "p")
			s.edges("resolved", "p", "q")
			if s.p.Postgres != nil {
				if _, err = s.p.Postgres.ExecContext(s.p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.t.Fatal(err)
				}
				if _, err = s.p.Postgres.ExecContext(s.p.Ctx, dump.String()); err != nil {
					s.t.Fatal("actual generated inheritance dump replay", err)
				}
				replay := *s
				replay.run = func(check fixture.Step) {
					check.Prepared = s.prepared
					fixture.RunStep(s.t, s.p.Ctx, s.p.Postgres, s.p.PostgresRecord, check)
				}
				for _, check := range checks {
					replay.run(check)
				}
				for _, name := range []string{"plain", "overridden", "resolved"} {
					replay.constraints(name, nil)
					replay.fail("INSERT INTO "+name+"(a,g) VALUES(4,99)", fixture.Diagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "g"`, Detail: `Column "g" is a generated column.`})
				}
				replay.edges("plain", "p")
				replay.edges("overridden", "p")
				replay.edges("resolved", "p", "q")
			}
			s.exec("INSERT INTO resolved(a) VALUES(5)", 1)
			s.values("SELECT * FROM resolved ORDER BY a", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(16)}, {int64(5), int64(20)}})
		}
	})
}

func TestReferenceInheritanceGeneratedConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, setup, query string
		diagnostic         fixture.Diagnostic
		notices            []fixture.Diagnostic
	}{
		{"kind", "CREATE TABLE q(a INT,g INT)", "CREATE TABLE c() INHERITS(p,q)", fixture.Diagnostic{Code: "42804", Message: `inherited column "g" has a generation conflict`}, []fixture.Diagnostic{inheritanceMergeNotice("a"), inheritanceMergeNotice("g")}},
		{"expressions", "CREATE TABLE q(a INT,g INT GENERATED ALWAYS AS(a*3) STORED)", "CREATE TABLE c() INHERITS(p,q)", fixture.Diagnostic{Code: "42611", Message: `column "g" inherits conflicting generation expressions`, Hint: "To resolve the conflict, specify a generation expression explicitly."}, []fixture.Diagnostic{inheritanceMergeNotice("a"), inheritanceMergeNotice("g")}},
		{"default", "", "CREATE TABLE c(g INT DEFAULT 9) INHERITS(p)", fixture.Diagnostic{Code: "42611", Message: `column "g" inherits from generated column but specifies default`}, []fixture.Diagnostic{inheritanceLocalNotice("g", true)}},
		{"base_to_generated", "CREATE TABLE q(a INT,g INT)", "CREATE TABLE c(g INT GENERATED ALWAYS AS(a*2) STORED) INHERITS(q)", fixture.Diagnostic{Code: "42611", Message: `child column "g" specifies generation expression`, Hint: "A child table column cannot be generated unless its parent column is."}, []fixture.Diagnostic{inheritanceLocalNotice("g", true)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE p(a INT,g INT GENERATED ALWAYS AS(a*2) STORED)", 0)
				if tc.setup != "" {
					s.exec(tc.setup, 0)
				}
				*s.notices = nil
				s.fail(tc.query, tc.diagnostic)
				if !reflect.DeepEqual(*s.notices, tc.notices) {
					s.t.Fatalf("notices %#v want %#v", *s.notices, tc.notices)
				}
				s.absent("c")
				s.exec("CREATE TABLE c(healthy INT)", 0)
				s.exec("INSERT INTO c VALUES(17)", 1)
				s.values("SELECT * FROM c", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(17)}})
			})
		})
	}
}
