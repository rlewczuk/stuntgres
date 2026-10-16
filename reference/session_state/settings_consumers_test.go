package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSettingsParserMode(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		a, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		if _, err = a.ExecContext(p.Ctx, "CREATE TABLE settings_literals(id INT,v TEXT)"); err != nil {
			t.Fatal(err)
		}
		if _, err = a.ExecContext(p.Ctx, "SET standard_conforming_strings TO off"); err != nil {
			t.Fatal(err)
		}
		off, err := a.PrepareContext(p.Ctx, `INSERT INTO settings_literals VALUES(1,'a\nb')`)
		if err != nil {
			t.Fatal(err)
		}
		defer off.Close()
		on, err := b.PrepareContext(p.Ctx, `INSERT INTO settings_literals VALUES(2,'a\nb')`)
		if err != nil {
			t.Fatal(err)
		}
		defer on.Close()
		if _, err = a.ExecContext(p.Ctx, "SET standard_conforming_strings TO on"); err != nil {
			t.Fatal(err)
		}
		if _, err = b.ExecContext(p.Ctx, "SET standard_conforming_strings TO off"); err != nil {
			t.Fatal(err)
		}
		if _, err = off.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = on.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		}
		off.Close()
		on.Close()
		if _, err = a.ExecContext(p.Ctx, `INSERT INTO settings_literals VALUES(3,E'a\nb')`); err != nil {
			t.Fatal(err)
		}
		record := p.MemoryRecord
		if db == p.Postgres {
			record = p.PostgresRecord
		}
		fixture.RunStep(t, p.Ctx, a, record, fixture.Select("SELECT v FROM settings_literals ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"a\nb"}, {`a\nb`}, {"a\nb"}}))
		fixture.RunStep(t, p.Ctx, a, record, fixture.Select("SELECT v FROM settings_literals ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a\nb"}, {"a\nb"}, {`a\nb`}}))
		a.Close()
		b.Close()
	}
}

func TestReferenceSettingsBooleanBindingDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, baseline := range []string{"", "retained"} {
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("SET application_name TO '"+baseline+"'", 0))
			for _, prepared := range []bool{false, true} {
				d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type boolean: "maybe"`, Context: "unnamed portal parameter $3 = '...'"}
				s := fixture.ErrorDiagnostic("SELECT set_config($1,$2,$3)", d, "application_name", "changed", "maybe")
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				exec := s
				exec.SelectRows = false
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, exec)
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", baseline))
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", baseline))
				for _, tc := range []struct{ query, code, message string }{
					{`SELECT set_config('application_name','v','maybe')`, "22P02", `invalid input syntax for type boolean: "maybe"`},
					{`SELECT set_config('application_name','a',)`, "42601", `syntax error at or near ")"`},
				} {
					s = fixture.ErrorDiagnostic(tc.query, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: "42"})
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", baseline))
				}
			}
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, localReferenceSelect(backend.PG, "SELECT set_config('application_name','healthy',false)", "set_config", "healthy"))
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, localReferenceSelect(backend.PG, "SELECT set_config('application_name',NULL,NULL)", "set_config", ""))
		}
	}
}

func TestReferenceSettingsUnicodeOffAndCapture(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		recorded := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE unicode_capture(id INT,v TEXT)", 0))
		queries := []string{`SET application_name TO U&'\0061'`, `SELECT set_config('application_name',U&'\0061',false)`, `INSERT INTO unicode_capture VALUES(1,U&'\0061')`}
		var captured []*sql.Stmt
		for _, q := range queries {
			s, e := c.PrepareContext(recorded, q)
			if e != nil {
				t.Fatal(e)
			}
			captured = append(captured, s)
			defer s.Close()
		}
		run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", ""))
		run(fixture.Exec("SET standard_conforming_strings TO off", 0))
		for _, tc := range []struct{ query, position string }{
			{queries[0], "25"}, {`SELECT current_setting(U&'application_name')`, "24"},
			{`SET application_name TO U&'\ZZZZ' UESCAPE '0'`, "25"}, {`SET application_name TO U&'unterminated`, "25"},
			{`/*é*/ SET application_name TO U&'\0061'`, "31"}, {`INSERT INTO unicode_capture VALUES(2,U&'\0061')`, "38"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.query, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsafe use of string constant with Unicode escapes", Detail: `String constants with Unicode escapes cannot be used when "standard_conforming_strings" is off.`, Position: tc.position})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", ""))
			}
		}
		run(fixture.Exec(`SET U&"application_name" TO 'x'`, 0))
		run(fixture.Exec(`SET application_name TO E'a\nb'`, 0))
		run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", `a\x0ab`))
		run(fixture.Exec(`SET application_name TO $$a\nb$$`, 0))
		run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", `a\nb`))
		for i, s := range captured {
			r, e := s.ExecContext(recorded)
			s.Close()
			if e != nil {
				t.Fatal(e)
			}
			n, e := r.RowsAffected()
			want := int64(0)
			if i > 0 {
				want = 1
			}
			if e != nil || n != want {
				t.Fatal(n, e)
			}
		}
		run(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "a"))
		run(localReferenceSelect(backend.PG, "SELECT v FROM unicode_capture", "v", "a"))
		c.Close()
	}
}

func TestReferenceSettingsSetConfigNotices(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		postgres := backend.PG
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
			original := strings.Repeat("a", 64)
			clipped := strings.Repeat("a", 63)
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
			for _, prepared := range []bool{false, true} {
				for _, threshold := range []string{"notice", "warning", "error"} {
					fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("SET client_min_messages TO "+threshold, 0))
					before := len(notices)
					s := fixture.Select("SELECT set_config($1,$2,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{clipped}}, "application_name", original)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, record, s)
					got := fixture.NoticeWitnesses(t, postgres, notices[before:], want)
					count := 0
					if threshold == "notice" {
						count = 1
					}
					if len(got) != count {
						t.Fatal(notices[before:])
					}
					fixture.RunStep(t, p.Ctx, c, record, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{clipped}}))
				}
				fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("RESET client_min_messages", 0))
				before := len(notices)
				s := fixture.Select("SELECT set_config('application_name',NULL,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, record, s)
				if len(notices) != before {
					t.Fatal("reset emitted notice", notices[before:])
				}
			}
		})
	}
}
