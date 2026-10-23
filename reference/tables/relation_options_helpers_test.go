package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// relationOptionsScenario is shared by every SQL witness, including controls.
func relationOptionsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, bool, func(fixture.Step))) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					body(t, p, b, c, prepared, run)
				})
			}
		})
	}
}

func relationOptionsDiagnostic(code, message, detail string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail = detail
	return d
}

func relationOptionsError(query, code, message, detail string) fixture.Step {
	s := fixture.ErrorDiagnostic(query, relationOptionsDiagnostic(code, message, detail))
	s.ErrorStage = "execute"
	return s
}

func relationOptionsState(table string, entries ...string) fixture.Step {
	var value any
	if len(entries) != 0 {
		value = relationOptionsArray(entries)
	}
	return fixture.Select("SELECT reloptions FROM pg_class WHERE oid='"+table+"'::regclass", []string{"reloptions"}, []string{"_TEXT"}, [][]any{{value}})
}

func relationOptionsRows(table string) fixture.Step {
	return fixture.Select("SELECT * FROM "+table+" ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "sentinel"}})
}

func relationOptionsSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE ro(id INT,payload TEXT)", 0))
	run(fixture.Exec("INSERT INTO ro VALUES(1,'sentinel')", 1))
	run(fixture.Exec("ALTER TABLE ro SET(fillfactor=70)", 0))
}

func relationOptionsDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// Physical TOAST identity is a genuine backend boundary, not a SQL bypass.
func relationOptionsToastState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, table string, entries ...string) {
	t.Helper()
	if !b.PG {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT reltoastrelid=0 AS absent FROM pg_class WHERE oid='"+table+"'::regclass", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}))
		return
	}
	var value any
	if len(entries) != 0 {
		value = relationOptionsArray(entries)
	}
	s := fixture.Select("SELECT t.oid<>b.oid AS distinct_toast,t.reloptions FROM pg_class b JOIN pg_class t ON t.oid=b.reltoastrelid WHERE b.oid='"+table+"'::regclass", []string{"distinct_toast", "reloptions"}, []string{"BOOL", "_TEXT"}, [][]any{{true, value}})
	s.Prepared = prepared
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func relationOptionsArray(entries []string) string {
	quoted := make([]string, len(entries))
	for i, entry := range entries {
		if entry == "" || strings.EqualFold(entry, "NULL") || strings.ContainsAny(entry, " \t\r\n{},\"\\") {
			entry = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(entry) + `"`
		}
		quoted[i] = entry
	}
	return "{" + strings.Join(quoted, ",") + "}"
}
