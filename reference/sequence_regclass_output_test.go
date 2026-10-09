package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceSequenceRegclassOutput(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s", "CREATE SEQUENCE b.s", `CREATE SEQUENCE a."select"`, `CREATE SEQUENCE a."A"`, "SET search_path TO a,b"} {
				w.exec(q)
			}
			for _, tc := range []struct{ query, want string }{{"SELECT 'a.s'::regclass", "s"}, {"SELECT 'b.s'::regclass", "b.s"}, {`SELECT 'a.select'::regclass`, `"select"`}, {`SELECT 'a."A"'::regclass`, `"A"`}, {"SELECT '4294967295'::regclass", "4294967295"}, {"SELECT '0'::regclass", "-"}} {
				step := referenceStep{query: tc.query, selectRows: true, columns: []string{"regclass"}, types: []string{"REGCLASS"}, rows: [][]any{{tc.want}}}
				step.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
					var got string
					if err := rows.Scan(&got); err != nil {
						t.Fatal("REGCLASS string scan", err)
					}
					if rowIndex != 0 || got != tc.want {
						t.Fatal("REGCLASS string", rowIndex, got, tc.want)
					}
				}
				referenceRunStep(t, p.ctx, w.c, backend.recorder, step)
			}
			w.exec("SET search_path TO b,a")
			step := referenceStep{query: "SELECT 'a.s'::regclass", selectRows: true, columns: []string{"regclass"}, types: []string{"REGCLASS"}, rows: [][]any{{"a.s"}}}
			step.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
				var got string
				if err := rows.Scan(&got); err != nil {
					t.Fatal("changed-path REGCLASS string scan", err)
				}
				if rowIndex != 0 || got != "a.s" {
					t.Fatal("changed-path REGCLASS string", rowIndex, got)
				}
			}
			referenceRunStep(t, p.ctx, w.c, backend.recorder, step)
		})
	}
}

// O: captured real OID continues to identify the renamed relation, but cannot
// adopt a new same-name object after deletion. Native general oid casts remain excluded.
func TestReferenceSequenceNumericIdentity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		}
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			w.exec("CREATE SEQUENCE s START 10")
			var oid int64
			if err := w.c.QueryRowContext(p.ctx, "SELECT 's'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal("actual OID", oid, err)
			}
			numeric := w.hold(fmt.Sprintf("SELECT nextval('%d'::regclass)", oid))
			parameter := w.hold("SELECT nextval($1)")
			w.exec("ALTER SEQUENCE s RENAME TO identity")
			w.value(numeric, 10)
			w.held(parameter, sequenceInt64Step(referenceStep{args: []any{fmt.Sprint(oid)}, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(11)}}}))
			w.exec("DROP SEQUENCE identity")
			w.exec("CREATE SEQUENCE s START 90")
			want := controlsReferenceDiagnostic("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			var got int64
			err := numeric.QueryRowContext(ctx).Scan(&got)
			referenceState(t, err, want.Code)
			referenceCheckDiagnostic(t, err, referenceStep{diagnostic: &want, errorStage: "execute"}, "execute")
			err = parameter.QueryRowContext(ctx, fmt.Sprint(oid)).Scan(&got)
			referenceState(t, err, want.Code)
			referenceCheckDiagnostic(t, err, referenceStep{diagnostic: &want, errorStage: "execute"}, "execute")
			sequenceProbeValue(t, p.ctx, w.c, "SELECT nextval('s')", 90)
		})
	}
}
