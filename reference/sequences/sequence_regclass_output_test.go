package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceRegclassOutput(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s", "CREATE SEQUENCE b.s", `CREATE SEQUENCE a."select"`, `CREATE SEQUENCE a."A"`, "SET search_path TO a,b"} {
				w.exec(q)
			}
			for _, tc := range []struct{ query, want string }{{"SELECT 'a.s'::regclass", "s"}, {"SELECT 'b.s'::regclass", "b.s"}, {`SELECT 'a.select'::regclass`, `"select"`}, {`SELECT 'a."A"'::regclass`, `"A"`}, {"SELECT '4294967295'::regclass", "4294967295"}, {"SELECT '0'::regclass", "-"}} {
				step := fixture.Step{Query: tc.query, SelectRows: true, Columns: []string{"regclass"}, Types: []string{"REGCLASS"}, Rows: [][]any{{tc.want}}}
				step.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
					var got string
					if err := rows.Scan(&got); err != nil {
						t.Fatal("REGCLASS string scan", err)
					}
					if rowIndex != 0 || got != tc.want {
						t.Fatal("REGCLASS string", rowIndex, got, tc.want)
					}
				}
				fixture.RunStep(t, p.Ctx, w.c, backend.Recorder, step)
			}
			w.exec("SET search_path TO b,a")
			step := fixture.Step{Query: "SELECT 'a.s'::regclass", SelectRows: true, Columns: []string{"regclass"}, Types: []string{"REGCLASS"}, Rows: [][]any{{"a.s"}}}
			step.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
				var got string
				if err := rows.Scan(&got); err != nil {
					t.Fatal("changed-path REGCLASS string scan", err)
				}
				if rowIndex != 0 || got != "a.s" {
					t.Fatal("changed-path REGCLASS string", rowIndex, got)
				}
			}
			fixture.RunStep(t, p.Ctx, w.c, backend.Recorder, step)
		})
	}
}

// O: captured real OID continues to identify the renamed relation, but cannot
// adopt a new same-name object after deletion. Native general oid casts remain excluded.
func TestReferenceSequenceNumericIdentity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		if !backend.PG {
			continue
		}
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			w.exec("CREATE SEQUENCE s START 10")
			var oid int64
			if err := w.c.QueryRowContext(p.Ctx, "SELECT 's'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal("actual OID", oid, err)
			}
			numeric := w.hold(fmt.Sprintf("SELECT nextval('%d'::regclass)", oid))
			parameter := w.hold("SELECT nextval($1)")
			w.exec("ALTER SEQUENCE s RENAME TO identity")
			w.value(numeric, 10)
			w.held(parameter, fixture.SequenceInt64Step(fixture.Step{Args: []any{fmt.Sprint(oid)}, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(11)}}}))
			w.exec("DROP SEQUENCE identity")
			w.exec("CREATE SEQUENCE s START 90")
			want := fixture.ControlsReferenceDiagnostic("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			var got int64
			err := numeric.QueryRowContext(ctx).Scan(&got)
			fixture.State(t, err, want.Code)
			fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: "execute"}, "execute")
			err = parameter.QueryRowContext(ctx, fmt.Sprint(oid)).Scan(&got)
			fixture.State(t, err, want.Code)
			fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: "execute"}, "execute")
			fixture.SequenceProbeValue(t, p.Ctx, w.c, "SELECT nextval('s')", 90)
		})
	}
}
