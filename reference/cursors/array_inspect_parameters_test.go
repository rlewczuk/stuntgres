package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func arrayInspectHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = "SELECT array_length($1::int[],$2) AS n"
	st, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
	for _, tc := range []struct{ a, d, want any }{
		{arrayInspectInput, 1, int64(2)}, {arrayInspectInput, 2, int64(3)}, {"{}", 1, nil}, {nil, 1, nil}, {arrayInspectInput, nil, nil},
	} {
		s := fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{tc.want}}, tc.a, tc.d)
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) { fixture.JSONTypeMetadataCheck(t, r, b.PG, s.Columns, s.Types) }
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	for _, tc := range []struct {
		a, d                             any
		parameter, code, message, detail string
	}{
		{arrayInspectInput, "bad", "2", "22P02", `invalid input syntax for type integer: "bad"`, ""},
		{nil, "bad", "2", "22P02", `invalid input syntax for type integer: "bad"`, ""},
		{"{1,,2}", 1, "1", "22P02", `malformed array literal: "{1,,2}"`, `Unexpected "," character.`},
		{arrayInspectInput, "2147483648", "2", "22003", `value "2147483648" is out of range for type integer`, ""},
	} {
		d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
		d.Detail, d.Context = tc.detail, "unnamed portal parameter $"+tc.parameter+" = '...'"
		s := fixture.ErrorDiagnostic(q, d, tc.a, tc.d)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		arrayInspectBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}}, arrayInspectInput, 2))
	}
	input := []byte(arrayInspectInput)
	rows, err := st.QueryContext(p.Ctx, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for i := range input {
		input[i] = 'x'
	}
	fixture.ArrayReferenceConsumeHeld(t, rows, [][]any{{int64(2)}}, []string{"INT4"})
}

func TestReferenceArrayInspectParametersMetadata(t *testing.T) {
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayInspectHeld(t, p, b, c)
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT " + arrayInspectTuple("'"+arrayInspectInput+"'::int[]", "2"), [][]any{{"[-2:-1][3:5]", int64(3), int64(3), int64(5), int64(2), int64(6)}}},
			{"SELECT " + arrayInspectTuple("NULL::int[]", "1"), [][]any{{nil, nil, nil, nil, nil, nil}}},
			{"SELECT " + arrayInspectTuple("'{}'::int[]", "1"), [][]any{{nil, nil, nil, nil, nil, int64(0)}}},
			{"SELECT " + arrayInspectTuple("ARRAY[1]", "1") + " WHERE FALSE", [][]any{}},
		} {
			arrayInspectBoth(t, p, b, c, arrayInspectSelect(tc.q, tc.rows, b.PG))
		}
	})
}
