package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferencePGNotifyResult pins the scalar row shape of the function form:
// one VOID row, non-NULL empty text, with the call's column name. Delivery is
// covered separately; here only the row and declaration are observed.
func TestReferencePGNotifyResult(t *testing.T) {
	cases := []struct {
		name string
		step fixture.Step
	}{
		{"direct-call", fixture.Select(
			"SELECT pg_notify('chan','payload')",
			[]string{"pg_notify"}, []string{"VOID"}, [][]any{{""}},
		)},
		{"schema-qualified", fixture.Select(
			"SELECT pg_catalog.pg_notify('schema_qualified','p')",
			[]string{"pg_notify"}, []string{"VOID"}, [][]any{{""}},
		)},
		{"two-calls", fixture.Select(
			"SELECT pg_notify('a','1') AS first, pg_notify('b','2') AS second",
			[]string{"first", "second"}, []string{"VOID", "VOID"}, [][]any{{"", ""}},
		)},
		{"null-payload", fixture.Select(
			"SELECT pg_notify('chan', NULL)",
			[]string{"pg_notify"}, []string{"VOID"}, [][]any{{""}},
		)},
		{"case-and-unicode", fixture.Select(
			"SELECT pg_notify('MiXed Chan','Payload É')",
			[]string{"pg_notify"}, []string{"VOID"}, [][]any{{""}},
		)},
		{"varchar-operands", fixture.Select(
			"SELECT pg_notify('c'::varchar,'p'::varchar)",
			[]string{"pg_notify"}, []string{"VOID"}, [][]any{{""}},
		)},
		{"no-rows", fixture.Select(
			"SELECT pg_notify('none','x') WHERE false",
			[]string{"pg_notify"}, []string{"VOID"}, [][]any{},
		)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				s, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				fixture.RunStep(t, p.Ctx, s, b.Recorder, c.step)
				fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Prepared(c.step))
			})
		})
	}
}
