package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Typed-nil TEXT bytes are SQL NULL on both routes; nonnil empty bytes retain
// genuine empty-input behavior. Go kind never selects the SQL signature.
func xml2ByteRepresentations(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := xml2Scalars(nil, nil, nil, nil, nil).Query
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	run := func(s fixture.Step) {
		t.Helper()
		xml2Both(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	healthy := xml2Scalars("<r>12</r>", ".", "12", float64(12), true)
	for _, bytes := range [][]byte{nil, {}} {
		var boolean any = false
		if bytes == nil {
			boolean = nil
		}
		for _, s := range []fixture.Step{
			xml2Scalars(bytes, ".", nil, nil, boolean),
			xml2Scalars("<r>", bytes, nil, nil, boolean),
		} {
			run(s)
			run(healthy)
			run(xml2Scalars(nil, "[", nil, nil, nil))
			run(s)
			run(healthy)
		}
	}
	for _, fn := range []struct {
		name, typ string
		valid     any
	}{
		{"xpath_string", "TEXT", "12"}, {"xpath_number", "FLOAT4", float64(12)}, {"xpath_bool", "BOOL", true},
	} {
		func() {
			q := "SELECT " + fn.name + "($1::text,$2::text) AS value"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run := func(s fixture.Step) {
				t.Helper()
				xml2Both(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			healthy := fixture.Select(q, []string{"value"}, []string{fn.typ}, [][]any{{fn.valid}}, "<r>12</r>", ".")
			null := fixture.Select(q, []string{"value"}, []string{fn.typ}, [][]any{{nil}}, "<r>12</r>", nil)
			for _, path := range []struct {
				value    any
				typedNil bool
			}{{[]byte(nil), true}, {[]byte{}, false}, {"", false}} {
				var value any = fn.valid
				if path.typedNil {
					value = nil
				}
				s := fixture.Select(q, []string{"value"}, []string{fn.typ}, [][]any{{value}}, "<r>12</r>", path.value)
				if fn.name != "xpath_string" && !path.typedNil {
					s = fixture.ErrorDiagnostic(q, xml2Compile("Invalid expression"), "<r>12</r>", path.value)
					s.SelectRows, s.ErrorStage = true, "execute"
				}
				run(s)
				run(healthy)
				run(null)
				run(s)
				run(healthy)
			}
		}()
	}
	xml2ValidationBytes(t, p, b, c)
}

func xml2ValidationBytes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT xml_valid($1::text) AS value"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	run := func(s fixture.Step) {
		t.Helper()
		xml2Both(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	for _, mode := range []string{"CONTENT", "DOCUMENT", "CONTENT"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET xmloption TO "+mode, 0))
		for _, input := range []struct {
			value any
			null  bool
		}{{[]byte(nil), true}, {[]byte{}, false}, {"", false}, {nil, true}} {
			var want any = mode == "CONTENT"
			if input.null {
				want = nil
			}
			s := fixture.Select(q, []string{"value"}, []string{"BOOL"}, [][]any{{want}}, input.value)
			run(s)
			run(fixture.Select(q, []string{"value"}, []string{"BOOL"}, [][]any{{true}}, "<r/>"))
			run(fixture.Select(q, []string{"value"}, []string{"BOOL"}, [][]any{{nil}}, nil))
			run(s)
		}
	}
}
