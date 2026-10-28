package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func xmlScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func xmlBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
}

func xmlSelect(q, typ string, values ...any) fixture.Step {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	return fixture.Select(q, []string{"v"}, []string{typ}, rows)
}

func xmlMetadata(t *testing.T, rows *sql.Rows, pg, stored bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 {
		t.Fatal(types)
	}
	c := types[0]
	wantScan := reflect.TypeFor[string]()
	if pg {
		wantScan = reflect.TypeFor[any]()
	}
	if c.Name() != "v" || c.DatabaseTypeName() != "XML" || c.ScanType() != wantScan {
		t.Fatalf("XML descriptor: %s %s %v", c.Name(), c.DatabaseTypeName(), c.ScanType())
	}
	if n, ok := c.Length(); n != 0 || ok {
		t.Fatalf("XML length %d/%v", n, ok)
	}
	if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
		t.Fatalf("XML decimal %d/%d/%v", p, s, ok)
	}
	if n, ok := c.Nullable(); pg && (n || ok) || !pg && stored && (!n || !ok) {
		t.Fatalf("XML nullable %v/%v", n, ok)
	}
}

func xmlError(code, message, detail string, position int) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail, d.Position = detail, fixture.Position(position)
	return d
}

const xmlBadA = "line 1: Premature end of data in tag a line 1\n<a>\n   ^"
const xmlFragmentDetail = "line 1: Start tag expected, '<' not found\ntext<a/>\n^"
const xmlAdjacency = `<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`

func xmlFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d, args...)
		s.SelectRows, s.ErrorStage = true, "execute"
		if prepared {
			s = fixture.Prepared(s)
			if analysis {
				s.ErrorStage = "prepare"
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
