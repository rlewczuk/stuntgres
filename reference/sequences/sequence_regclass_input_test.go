package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
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
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						w := newSequencePreparedSession(t, p, backend)
						w.exec("CREATE TABLE empty_table(v INT)")
						w.exec("CREATE SEQUENCE t START 17")
						want := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
						want.Context = "unnamed portal parameter $1 = '...'"
						fixture.RunStep(t, p.Ctx, w.c, backend.Recorder, fixture.Step{Query: tc.query, Args: tc.args, Prepared: prepared, SelectRows: true, State: want.Code, Diagnostic: &want, ErrorStage: "execute"})
						undefined := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "t" is not yet defined in this session`)
						ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
						var n int64
						err := w.c.QueryRowContext(ctx, "SELECT currval('t')").Scan(&n)
						fixture.State(t, err, undefined.Code)
						fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &undefined, ErrorStage: "execute"}, "execute")
						fixture.SequenceProbeValue(t, p.Ctx, w.c, "SELECT nextval('t')", 17)
					})
				}
			})
		}
	}
}

func TestReferenceSequenceRegclassParameterPresentationAndDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s", "CREATE SEQUENCE b.s", `CREATE SEQUENCE a."A"`, "CREATE TABLE empty_table(v INT)", "SET search_path TO a,b,public"} {
				w.exec(q)
			}
			for _, prepared := range []bool{false, true} {
				executor := fixture.Executor(w.c)
				if prepared {
					s := w.hold("SELECT $1::regclass")
					executor = fixture.HeldStatement{Executor: w.c, Statement: s, SQL: "SELECT $1::regclass"}
				}
				for _, tc := range []struct{ input, want any }{{"a.s", "s"}, {"b.s", "b.s"}, {`a."A"`, `"A"`}, {"4294967295", "4294967295"}, {"0", "-"}, {"-", "-"}, {nil, nil}} {
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Step{Query: "SELECT $1::regclass", Args: []any{tc.input}, SelectRows: true, Columns: []string{"regclass"}, Types: []string{"REGCLASS"}, Rows: [][]any{{tc.want}}})
				}
			}
			for _, arg := range []any{"a.s", "4294967295", "-", nil} {
				fixture.RunStep(t, p.Ctx, w.c, backend.Recorder, fixture.Step{Query: "SELECT nextval($1) FROM empty_table", Args: []any{arg}, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{}})
				fixture.RunStep(t, p.Ctx, w.c, backend.Recorder, fixture.Step{Query: "SELECT setval($1,$2)", Args: []any{arg, nil}, SelectRows: true, Columns: []string{"setval"}, Types: []string{"INT8"}, Rows: [][]any{{nil}}})
			}
			for _, tc := range []struct{ input, oid string }{{"4294967295", "4294967295"}, {"0", "0"}, {"-", "0"}} {
				want := fixture.ControlsReferenceDiagnostic("XX000", "could not open relation with OID "+tc.oid)
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
				var n int64
				err := w.c.QueryRowContext(ctx, "SELECT nextval($1)", tc.input).Scan(&n)
				fixture.State(t, err, want.Code)
				fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: "execute"}, "execute")
			}
			fixture.SequenceProbeValue(t, p.Ctx, w.c, "SELECT nextval('a.s')", 1)
		})
	}
}

func TestReferenceSequenceRegclassParameterNamespaceInput(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		if !backend.PG {
			continue
		} // O: schema privileges, not a native dialect expansion.
		t.Run(backend.Name, func(t *testing.T) {
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
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema private")
				want.Context = "unnamed portal parameter $1 = '...'"
				fixture.RunStep(t, p.Ctx, w.c, backend.Recorder, fixture.Step{Query: q, Args: args, Prepared: true, SelectRows: true, State: want.Code, Diagnostic: &want, ErrorStage: "execute"})
			}
			w.exec("RESET SESSION AUTHORIZATION")
			fixture.SequenceProbeValue(t, p.Ctx, w.c, "SELECT nextval('private.s')", 1)
		})
	}
}

// Retain the original held QueryRowContext route and its no-demand import
// failures before admitting punctuation and whitespace as real identifiers.
func TestReferenceSequenceRegclassSourceInputStream(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			for _, q := range []string{"CREATE SEQUENCE s START 17 CACHE 1", "CREATE SEQUENCE t START 31 CACHE 1", "CREATE TABLE empty_table(v INT)", "CREATE TABLE nullable_table(v BIGINT)"} {
				w.exec(q)
			}
			fixture.LockExec(t, p.Ctx, w.c, "INSERT INTO nullable_table VALUES(NULL)", 1)
			held := w.hold("SELECT nextval($1)")
			for _, input := range []string{"s--suffix", "s/*suffix*/"} {
				want := fixture.ControlsReferenceDiagnostic("42P01", `relation "`+input+`" does not exist`)
				want.Context = "unnamed portal parameter $1 = '...'"
				for _, q := range []string{"SELECT nextval($1) FROM empty_table", "SELECT setval($1,$2)", "SELECT setval($1,v) FROM nullable_table", "SELECT setval($1,nextval('t'))"} {
					stmt := w.hold(q)
					args := []any{input}
					if q == "SELECT setval($1,$2)" {
						args = append(args, nil)
					}
					var n any
					err := stmt.QueryRowContext(p.Ctx, args...).Scan(&n)
					fixture.AssertDiagnostic(t, err, &want)
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
				var n int64
				fixture.AssertDiagnostic(t, held.QueryRowContext(p.Ctx, input).Scan(&n), &want)
				for _, name := range []string{"s", "t"} {
					undefined := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "`+name+`" is not yet defined in this session`)
					fixture.AssertDiagnostic(t, w.c.QueryRowContext(p.Ctx, "SELECT currval('"+name+"')").Scan(&n), &undefined)
				}
				undefined := fixture.ControlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
				fixture.AssertDiagnostic(t, w.c.QueryRowContext(p.Ctx, "SELECT lastval()").Scan(&n), &undefined)
			}
			for _, q := range []string{`CREATE SEQUENCE "s--suffix" START 41 CACHE 1`, `CREATE SEQUENCE "s/*suffix*/" START 51 CACHE 1`, `CREATE SEQUENCE "s-other" START 61 CACHE 1`, `CREATE SEQUENCE "0" START 71 CACHE 1`, `CREATE SEQUENCE "-" START 81 CACHE 1`, `CREATE SEQUENCE "4294967295" START 91 CACHE 1`} {
				w.exec(q)
			}
			for _, tc := range []struct {
				input string
				want  int64
			}{{"s--suffix", 41}, {"s/*suffix*/", 51}, {"s-other", 61}, {" 0 ", 71}, {" - ", 81}, {" 4294967295 ", 91}, {"s", 17}, {"t", 31}} {
				var n int64
				if err := held.QueryRowContext(p.Ctx, tc.input).Scan(&n); err != nil || n != tc.want {
					t.Fatal(tc.input, n, tc.want, err)
				}
			}
			projection := w.hold("SELECT $1::regclass")
			for _, tc := range []struct{ input, want string }{{"0", "-"}, {"-", "-"}, {"4294967295", "4294967295"}, {" 0 ", `"0"`}, {" - ", `"-"`}, {" 4294967295 ", `"4294967295"`}} {
				var name string
				if err := projection.QueryRowContext(p.Ctx, tc.input).Scan(&name); err != nil || name != tc.want {
					t.Fatal(tc.input, name, tc.want, err)
				}
			}
		})
	}
}
