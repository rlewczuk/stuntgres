package reference_test

import (
	"context"
	"testing"
)

// Import errors precede projection demand, strict NULL and nested effects.
func TestReferenceSequenceRegclassParameterInput(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, query string
			args        []any
		}{
			{"Projection", "SELECT $1::regclass", []any{"missing"}},
			{"ZeroRows", "SELECT nextval($1) FROM empty_table", []any{"missing"}},
			{"RuntimeNull", "SELECT setval($1,$2)", []any{"missing", nil}},
			{"NestedEffect", "SELECT setval($1,nextval('t'))", []any{"missing"}},
		} {
			t.Run(tc.name+map[bool]string{false: "/Direct", true: "/Prepared"}[prepared], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						w := newSequencePreparedSession(t, p, backend)
						w.exec("CREATE TABLE empty_table(v INT)")
						w.exec("CREATE SEQUENCE t START 17")
						want := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
						want.Context = "unnamed portal parameter $1 = '...'"
						referenceRunStep(t, p.ctx, w.c, backend.recorder, referenceStep{query: tc.query, args: tc.args, prepared: prepared, selectRows: true, state: want.Code, diagnostic: &want, errorStage: "execute"})
						undefined := controlsReferenceDiagnostic("55000", `currval of sequence "t" is not yet defined in this session`)
						ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
						var n int64
						err := w.c.QueryRowContext(ctx, "SELECT currval('t')").Scan(&n)
						referenceState(t, err, undefined.Code)
						referenceCheckDiagnostic(t, err, referenceStep{diagnostic: &undefined, errorStage: "execute"}, "execute")
						sequenceProbeValue(t, p.ctx, w.c, "SELECT nextval('t')", 17)
					})
				}
			})
		}
	}
}

func TestReferenceSequenceRegclassParameterPresentationAndDemand(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s", "CREATE SEQUENCE b.s", `CREATE SEQUENCE a."A"`, "CREATE TABLE empty_table(v INT)", "SET search_path TO a,b,public"} {
				w.exec(q)
			}
			for _, prepared := range []bool{false, true} {
				executor := referenceExecutor(w.c)
				if prepared {
					s := w.hold("SELECT $1::regclass")
					executor = referenceHeldStatement{referenceExecutor: w.c, statement: s, query: "SELECT $1::regclass"}
				}
				for _, tc := range []struct{ input, want any }{{"a.s", "s"}, {"b.s", "b.s"}, {`a."A"`, `"A"`}, {"4294967295", "4294967295"}, {"0", "-"}, {"-", "-"}, {nil, nil}} {
					referenceRunStep(t, p.ctx, executor, backend.recorder, referenceStep{query: "SELECT $1::regclass", args: []any{tc.input}, selectRows: true, columns: []string{"regclass"}, types: []string{"REGCLASS"}, rows: [][]any{{tc.want}}})
				}
			}
			for _, arg := range []any{"a.s", "4294967295", "-", nil} {
				referenceRunStep(t, p.ctx, w.c, backend.recorder, referenceStep{query: "SELECT nextval($1) FROM empty_table", args: []any{arg}, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{}})
				referenceRunStep(t, p.ctx, w.c, backend.recorder, referenceStep{query: "SELECT setval($1,$2)", args: []any{arg, nil}, selectRows: true, columns: []string{"setval"}, types: []string{"INT8"}, rows: [][]any{{nil}}})
			}
			for _, tc := range []struct{ input, oid string }{{"4294967295", "4294967295"}, {"0", "0"}, {"-", "0"}} {
				want := controlsReferenceDiagnostic("XX000", "could not open relation with OID "+tc.oid)
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
				var n int64
				err := w.c.QueryRowContext(ctx, "SELECT nextval($1)", tc.input).Scan(&n)
				referenceState(t, err, want.Code)
				referenceCheckDiagnostic(t, err, referenceStep{diagnostic: &want, errorStage: "execute"}, "execute")
			}
			sequenceProbeValue(t, p.ctx, w.c, "SELECT nextval('a.s')", 1)
		})
	}
}

func TestReferenceSequenceRegclassParameterNamespaceInput(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		} // O: schema privileges, not a native dialect expansion.
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			t.Cleanup(func() {
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS private CASCADE", "DROP ROLE IF EXISTS seq_input_reader"} {
					w.exec(q)
				}
			})
			for _, q := range []string{"CREATE ROLE seq_input_reader", "CREATE SCHEMA private", "CREATE SEQUENCE private.s", "REVOKE ALL ON SCHEMA private FROM PUBLIC", "SET SESSION AUTHORIZATION seq_input_reader"} {
				w.exec(q)
			}
			for _, q := range []string{"SELECT $1::regclass", "SELECT setval($1,$2)"} {
				args := []any{"private.s"}
				if q == "SELECT setval($1,$2)" {
					args = append(args, nil)
				}
				want := controlsReferenceDiagnostic("42501", "permission denied for schema private")
				want.Context = "unnamed portal parameter $1 = '...'"
				referenceRunStep(t, p.ctx, w.c, backend.recorder, referenceStep{query: q, args: args, prepared: true, selectRows: true, state: want.Code, diagnostic: &want, errorStage: "execute"})
			}
			w.exec("RESET SESSION AUTHORIZATION")
			sequenceProbeValue(t, p.ctx, w.c, "SELECT nextval('private.s')", 1)
		})
	}
}
