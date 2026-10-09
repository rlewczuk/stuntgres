package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteInputBeforeDemand(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
			defer cancel()
			c, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			aSchema := fmt.Sprintf("delete_boundary_%d", time.Now().UnixNano())
			defer func() {
				_, _ = c.ExecContext(p.ctx, "ROLLBACK")
				_, _ = c.ExecContext(p.ctx, "SET search_path TO public")
				if _, err := c.ExecContext(p.ctx, "DROP SCHEMA "+aSchema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, ctx, c, backend.recorder, s) }
			exec := func(q string, n int64) { run(referenceExec(q, n)) }
			rows := func(q string, columns, types []string, want [][]any) { run(referenceSelect(q, columns, types, want)) }
			state := func(name string, n int64, called bool) {
				rows("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}})
			}
			exec("CREATE SCHEMA "+aSchema, 0)
			exec("SET search_path TO "+aSchema, 0)
			for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_t(id INT)", "CREATE TABLE t(id INT)"} {
				exec(q, 0)
			}
			exec("INSERT INTO t VALUES(1),(1),(2)", 3)
			bad := controlsReferenceDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`)
			missing := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			missing.Context = "unnamed portal parameter $1 = '...'"
			for _, prepared := range []bool{false, true} {
				for _, target := range []string{"empty_t", "t"} {
					for _, tc := range []struct {
						q    string
						diag referenceDiagnostic
					}{
						{"DELETE FROM " + target + " WHERE setval('s',$1::text::bigint)>0", bad},
						{"DELETE FROM " + target + " WHERE setval($1,nextval('nested'))>0", missing},
					} {
						arg := "bad"
						if tc.diag.Code == "42P01" {
							arg = "missing"
						}
						run(referenceStep{query: tc.q, args: []any{arg}, prepared: prepared, state: tc.diag.Code, diagnostic: &tc.diag, errorStage: "execute"})
						rows("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
						rows("SELECT id FROM empty_t", []string{"id"}, []string{"INT4"}, nil)
						for _, seq := range []struct {
							name  string
							start int64
						}{{"s", 31}, {"nested", 47}} {
							state(seq.name, seq.start, false)
							want := controlsReferenceDiagnostic("55000", `currval of sequence "`+seq.name+`" is not yet defined in this session`)
							run(referenceStep{query: "SELECT currval('" + seq.name + "')", selectRows: true, state: want.Code, diagnostic: &want, errorStage: "execute"})
						}
						want := controlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
						run(referenceStep{query: "SELECT lastval()", selectRows: true, state: want.Code, diagnostic: &want, errorStage: "execute"})
					}
				}
			}
		})
	}
}
