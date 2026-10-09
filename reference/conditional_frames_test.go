package reference_test

import (
	"context"
	"testing"
)

func TestReferenceConditionalHeldParameters(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1)", 1))
	q := "SELECT COALESCE($1,1),NULLIF($1,1) FROM t WHERE id=1"
	for _, backend := range p.backends() {
		held, err := backend.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error(err)
			}
		}()
		executor := referenceHeldStatement{backend.db, held, q}
		for _, tc := range []struct {
			arg any
			row []any
		}{{2, []any{int64(2), int64(2)}}, {nil, []any{int64(1), nil}}, {1, []any{int64(1), nil}}, {3, []any{int64(3), int64(3)}}} {
			scalarRunStep(t, p.ctx, executor, backend.recorder, referenceSelect(q, []string{"coalesce", "nullif"}, []string{"INT4", "INT4"}, [][]any{tc.row}, tc.arg))
		}
		bad := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
		bad.selectRows = true
		bad.errorStage = "execute"
		scalarRunStep(t, p.ctx, executor, backend.recorder, bad)
		scalarRunStep(t, p.ctx, executor, backend.recorder, referenceSelect(q, []string{"coalesce", "nullif"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(4)}}, 4))
		held.Close()
	}
}

func TestReferenceConditionalPreparedDemand(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,v INT,b BOOL)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,0,TRUE),(2,NULL,FALSE)", 2))
	q := "SELECT CASE WHEN b THEN 1 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END FROM t WHERE id=$2"
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			held, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			executor := referenceHeldStatement{backend.db, held, q}
			for _, id := range []int{1, 2, 1} {
				step := referenceSelect(q, []string{"case"}, []string{"INT4"}, [][]any{{int64(1)}}, "bad", id)
				if backend.pg || id == 2 {
					step = referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad", id)
					step.selectRows = true
					step.errorStage = "execute"
				}
				scalarRunStep(t, p.ctx, executor, backend.recorder, step)
			}
			scalarRunStep(t, p.ctx, executor, backend.recorder, referenceSelect(q, []string{"case"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
			coalesce := "SELECT COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=1"
			step := referenceSelect(coalesce, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(0)}}, "bad")
			if backend.pg {
				step = referenceImplicitError(coalesce, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad")
				step.selectRows = true
				step.errorStage = "execute"
			}
			scalarRunStep(t, p.ctx, backend.db, backend.recorder, step)
			scalarRunStep(t, p.ctx, executor, backend.recorder, referenceSelect(q, []string{"case"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
		})
	}
}

func TestReferenceConditionalCoalescePreparedDemand(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,0),(2,NULL)", 2))
	q := "SELECT COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=$2"
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			bad := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad", 2)
			bad.selectRows = true
			bad.errorStage = "execute"
			scalarRunStep(t, p.ctx, backend.db, backend.recorder, bad)
			scalarRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect(q, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
			held, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			executor := referenceHeldStatement{backend.db, held, q}
			scalarRunStep(t, p.ctx, executor, backend.recorder, bad)
			scalarRunStep(t, p.ctx, executor, backend.recorder, referenceSelect(q, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
			step := referenceSelect(q, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(0)}}, "bad", 1)
			if backend.pg {
				step = referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad", 1)
				step.selectRows = true
				step.errorStage = "execute"
			}
			scalarRunStep(t, p.ctx, executor, backend.recorder, step)
			scalarRunStep(t, p.ctx, executor, backend.recorder, referenceSelect(q, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
		})
	}
}

func TestReferenceConditionalExtremaDemand(t *testing.T) {
	p := conditionalReferenceSetup(t)
	for index, q := range []string{"SELECT GREATEST(100,10/v) FROM t WHERE id=1", "SELECT LEAST(0,10/v) FROM t WHERE id=1", "SELECT NULLIF(NULL,10/v) FROM t WHERE id=1"} {
		for _, backend := range p.backends() {
			for _, prepared := range []bool{false, true} {
				if backend.pg && index == 2 {
					// PG independently folds the comparable NULL first operand;
					// native still demands the division and reports full22012.
					var db referenceExecutor = backend.db
					if prepared {
						held, err := backend.db.PrepareContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), q)
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if err := held.Close(); err != nil {
								t.Error(err)
							}
						}()
						db = referenceHeldStatement{backend.db, held, q}
					}
					executor := &scalarMetadataExecutor{referenceExecutor: db, t: t, pg: true, computed: []int{0}, unknownLength: []int{0}}
					step := referenceSelect(q, []string{"nullif"}, []string{"INT4"}, [][]any{{nil}})
					referenceRunStep(t, p.ctx, executor, backend.recorder, step)
					scalarAssertMetadata(t, true, executor.metadata, 0)
					scalarAssertUnknownLength(t, executor.metadata, 0)
					scalarAssertDescription(t, executor.metadata, step.columns, step.types)
				} else {
					step := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
					step.selectRows = true
					step.errorStage = "execute"
					step.prepared = prepared
					scalarRunStep(t, p.ctx, backend.db, backend.recorder, step)
				}
			}
		}
	}
}

func TestReferenceConditionalConcurrentPreparedFrames(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(v INT)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(0)", 1))
	q := "SELECT CASE WHEN $1 THEN COALESCE($2,7) ELSE NULLIF($2,9) END FROM t"
	for _, backend := range p.backends() {
		held, err := backend.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error(err)
			}
		}()
		scalarConcurrentFrames(t, p, backend, held, q, 16, []string{"nullif"}, []string{"INT4"}, func(n int) ([]any, [][]any) {
			var arg, want any = int64(n), int64(n)
			if n%4 == 0 {
				arg = nil
				want = int64(7)
			} else if n == 9 {
				want = nil
			}
			return []any{n%2 == 0, arg}, [][]any{{want}}
		})
		held.Close()
	}
}
