package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceSettingsParserMode(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		a, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		if _, err = a.ExecContext(p.ctx, "CREATE TABLE settings_literals(id INT,v TEXT)"); err != nil {
			t.Fatal(err)
		}
		if _, err = a.ExecContext(p.ctx, "SET standard_conforming_strings TO off"); err != nil {
			t.Fatal(err)
		}
		off, err := a.PrepareContext(p.ctx, `INSERT INTO settings_literals VALUES(1,'a\nb')`)
		if err != nil {
			t.Fatal(err)
		}
		defer off.Close()
		on, err := b.PrepareContext(p.ctx, `INSERT INTO settings_literals VALUES(2,'a\nb')`)
		if err != nil {
			t.Fatal(err)
		}
		defer on.Close()
		if _, err = a.ExecContext(p.ctx, "SET standard_conforming_strings TO on"); err != nil {
			t.Fatal(err)
		}
		if _, err = b.ExecContext(p.ctx, "SET standard_conforming_strings TO off"); err != nil {
			t.Fatal(err)
		}
		if _, err = off.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = on.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		}
		off.Close()
		on.Close()
		if _, err = a.ExecContext(p.ctx, `INSERT INTO settings_literals VALUES(3,E'a\nb')`); err != nil {
			t.Fatal(err)
		}
		record := p.memoryRecord
		if db == p.postgres {
			record = p.postgresRecord
		}
		referenceRunStep(t, p.ctx, a, record, referenceSelect("SELECT v FROM settings_literals ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"a\nb"}, {`a\nb`}, {"a\nb"}}))
		referenceRunStep(t, p.ctx, a, record, referenceSelect("SELECT v FROM settings_literals ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a\nb"}, {"a\nb"}, {`a\nb`}}))
		a.Close()
		b.Close()
	}
}

func TestReferenceSettingsBooleanBindingDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, baseline := range []string{"", "retained"} {
			referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET application_name TO '"+baseline+"'", 0))
			for _, prepared := range []bool{false, true} {
				d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type boolean: "maybe"`, Context: "unnamed portal parameter $3 = '...'"}
				s := referenceErrorDiagnostic("SELECT set_config($1,$2,$3)", d, "application_name", "changed", "maybe")
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				exec := s
				exec.selectRows = false
				referenceRunStep(t, p.ctx, c, backend.recorder, exec)
				referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", baseline))
				referenceRunStep(t, p.ctx, c, backend.recorder, s)
				referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", baseline))
				for _, tc := range []struct{ query, code, message string }{
					{`SELECT set_config('application_name','v','maybe')`, "22P02", `invalid input syntax for type boolean: "maybe"`},
					{`SELECT set_config('application_name','a',)`, "42601", `syntax error at or near ")"`},
				} {
					s = referenceErrorDiagnostic(tc.query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: "42"})
					s.selectRows = true
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, c, backend.recorder, s)
					referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", baseline))
				}
			}
			referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SELECT set_config('application_name','healthy',false)", "set_config", "healthy"))
			referenceRunStep(t, p.ctx, c, backend.recorder, localReferenceSelect(backend.pg, "SELECT set_config('application_name',NULL,NULL)", "set_config", ""))
		}
	}
}

func TestReferenceSettingsUnicodeOffAndCapture(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		recorded := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
		run(referenceExec("CREATE TABLE unicode_capture(id INT,v TEXT)", 0))
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
		run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", ""))
		run(referenceExec("SET standard_conforming_strings TO off", 0))
		for _, tc := range []struct{ query, position string }{
			{queries[0], "25"}, {`SELECT current_setting(U&'application_name')`, "24"},
			{`SET application_name TO U&'\ZZZZ' UESCAPE '0'`, "25"}, {`SET application_name TO U&'unterminated`, "25"},
			{`/*é*/ SET application_name TO U&'\0061'`, "31"}, {`INSERT INTO unicode_capture VALUES(2,U&'\0061')`, "38"},
		} {
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic(tc.query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsafe use of string constant with Unicode escapes", Detail: `String constants with Unicode escapes cannot be used when "standard_conforming_strings" is off.`, Position: tc.position})
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				run(s)
				run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", ""))
			}
		}
		run(referenceExec(`SET U&"application_name" TO 'x'`, 0))
		run(referenceExec(`SET application_name TO E'a\nb'`, 0))
		run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", `a\x0ab`))
		run(referenceExec(`SET application_name TO $$a\nb$$`, 0))
		run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", `a\nb`))
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
		run(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "a"))
		run(localReferenceSelect(backend.pg, "SELECT v FROM unicode_capture", "v", "a"))
		c.Close()
	}
}

func TestReferenceSettingsSetConfigNotices(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		postgres := backend.pg
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, postgres, &notices)
			original := strings.Repeat("a", 64)
			clipped := strings.Repeat("a", 63)
			want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
			for _, prepared := range []bool{false, true} {
				for _, threshold := range []string{"notice", "warning", "error"} {
					referenceRunStep(t, p.ctx, c, record, referenceExec("SET client_min_messages TO "+threshold, 0))
					before := len(notices)
					s := referenceSelect("SELECT set_config($1,$2,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{clipped}}, "application_name", original)
					s.prepared = prepared
					referenceRunStep(t, p.ctx, c, record, s)
					got := referenceNoticeWitnesses(t, postgres, notices[before:], want)
					count := 0
					if threshold == "notice" {
						count = 1
					}
					if len(got) != count {
						t.Fatal(notices[before:])
					}
					referenceRunStep(t, p.ctx, c, record, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{clipped}}))
				}
				referenceRunStep(t, p.ctx, c, record, referenceExec("RESET client_min_messages", 0))
				before := len(notices)
				s := referenceSelect("SELECT set_config('application_name',NULL,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}})
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, record, s)
				if len(notices) != before {
					t.Fatal("reset emitted notice", notices[before:])
				}
			}
		})
	}
}
