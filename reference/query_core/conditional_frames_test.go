package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConditionalHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(1)", 1))
	q := "SELECT COALESCE($1,1),NULLIF($1,1) FROM t WHERE id=1"
	for _, backend := range p.Backends() {
		held, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error(err)
			}
		}()
		executor := fixture.HeldStatement{backend.DB, held, q}
		for _, tc := range []struct {
			arg any
			row []any
		}{{2, []any{int64(2), int64(2)}}, {nil, []any{int64(1), nil}}, {1, []any{int64(1), nil}}, {3, []any{int64(3), int64(3)}}} {
			fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select(q, []string{"coalesce", "nullif"}, []string{"INT4", "INT4"}, [][]any{tc.row}, tc.arg))
		}
		bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
		bad.SelectRows = true
		bad.ErrorStage = "execute"
		fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, bad)
		fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select(q, []string{"coalesce", "nullif"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(4)}}, 4))
		held.Close()
	}
}

func TestReferenceConditionalPreparedDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT,v INT,b BOOL)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(1,0,TRUE),(2,NULL,FALSE)", 2))
	q := "SELECT CASE WHEN b THEN 1 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END FROM t WHERE id=$2"
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			held, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			executor := fixture.HeldStatement{backend.DB, held, q}
			for _, id := range []int{1, 2, 1} {
				step := fixture.Select(q, []string{"case"}, []string{"INT4"}, [][]any{{int64(1)}}, "bad", id)
				if backend.PG || id == 2 {
					step = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad", id)
					step.SelectRows = true
					step.ErrorStage = "execute"
				}
				fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, step)
			}
			fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select(q, []string{"case"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
			coalesce := "SELECT COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=1"
			step := fixture.Select(coalesce, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(0)}}, "bad")
			if backend.PG {
				step = fixture.ImplicitError(coalesce, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad")
				step.SelectRows = true
				step.ErrorStage = "execute"
			}
			fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select(q, []string{"case"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
		})
	}
}

func TestReferenceConditionalCoalescePreparedDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(1,0),(2,NULL)", 2))
	q := "SELECT COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=$2"
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad", 2)
			bad.SelectRows = true
			bad.ErrorStage = "execute"
			fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, bad)
			fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select(q, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
			held, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			executor := fixture.HeldStatement{backend.DB, held, q}
			fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, bad)
			fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select(q, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
			step := fixture.Select(q, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(0)}}, "bad", 1)
			if backend.PG {
				step = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad", 1)
				step.SelectRows = true
				step.ErrorStage = "execute"
			}
			fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, step)
			fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select(q, []string{"coalesce"}, []string{"INT4"}, [][]any{{int64(7)}}, "7", 2))
		})
	}
}

func TestReferenceConditionalExtremaDemand(t *testing.T) {
	p := conditionalReferenceSetup(t)
	for index, q := range []string{"SELECT GREATEST(100,10/v) FROM t WHERE id=1", "SELECT LEAST(0,10/v) FROM t WHERE id=1", "SELECT NULLIF(NULL,10/v) FROM t WHERE id=1"} {
		for _, backend := range p.Backends() {
			for _, prepared := range []bool{false, true} {
				if backend.PG && index == 2 {
					// PG independently folds the comparable NULL first operand;
					// native still demands the division and reports full22012.
					var db fixture.Executor = backend.DB
					if prepared {
						held, err := backend.DB.PrepareContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), q)
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if err := held.Close(); err != nil {
								t.Error(err)
							}
						}()
						db = fixture.HeldStatement{backend.DB, held, q}
					}
					executor := &fixture.ScalarMetadataExecutor{Executor: db, T: t, PG: true, Computed: []int{0}, UnknownLength: []int{0}}
					step := fixture.Select(q, []string{"nullif"}, []string{"INT4"}, [][]any{{nil}})
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
					fixture.ScalarAssertMetadata(t, true, executor.Metadata, 0)
					fixture.ScalarAssertUnknownLength(t, executor.Metadata, 0)
					fixture.ScalarAssertDescription(t, executor.Metadata, step.Columns, step.Types)
				} else {
					step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					step.SelectRows = true
					step.ErrorStage = "execute"
					step.Prepared = prepared
					fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
				}
			}
		}
	}
}

func TestReferenceConditionalConcurrentPreparedFrames(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(v INT)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(0)", 1))
	q := "SELECT CASE WHEN $1 THEN COALESCE($2,7) ELSE NULLIF($2,9) END FROM t"
	for _, backend := range p.Backends() {
		held, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error(err)
			}
		}()
		fixture.ScalarConcurrentFrames(t, p, backend, held, q, 16, []string{"nullif"}, []string{"INT4"}, func(n int) ([]any, [][]any) {
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
