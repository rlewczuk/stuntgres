package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTypeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, backend, conn)
		})
	}
}

func jsonTypeRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func jsonTypeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		copy := s
		if prepared {
			copy = fixture.Prepared(copy)
		}
		jsonTypeRun(t, p, b, c, copy)
	}
}

func jsonTypeQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func jsonTypeError(query, code, message string, position int, args ...any) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Position = fixture.Position(position)
	s := fixture.ErrorDiagnostic(query, d, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func jsonTypeSignature(query, signature string, position int) fixture.Step {
	s := jsonTypeError(query, "42883", "function "+signature+" does not exist", position)
	s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	return s
}

func jsonTypeAnalysisError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	jsonTypeRun(t, p, b, c, s)
	s = fixture.Prepared(s)
	s.ErrorStage = "prepare"
	jsonTypeRun(t, p, b, c, s)
}

const jsonTypeOrderedObject = `{"zz":0,"b":1,"a":2,"aa":3,"b":4,"é":5,"aaa":6}`

func jsonTypeKeyRows(family string) [][]any {
	if family == "json" {
		return [][]any{{"zz", int64(1)}, {"b", int64(2)}, {"a", int64(3)}, {"aa", int64(4)}, {"b", int64(5)}, {"é", int64(6)}, {"aaa", int64(7)}}
	}
	return [][]any{{"a", int64(1)}, {"b", int64(2)}, {"aa", int64(3)}, {"zz", int64(4)}, {"é", int64(5)}, {"aaa", int64(6)}}
}

func jsonTypeKeySelect(family string, input any, rows [][]any) fixture.Step {
	return fixture.Select("SELECT * FROM "+family+"_object_keys($1) WITH ORDINALITY AS k(v,n) ORDER BY n", []string{"v", "n"}, []string{"TEXT", "INT8"}, rows, input)
}
