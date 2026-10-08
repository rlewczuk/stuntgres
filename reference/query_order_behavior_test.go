package reference_test

import "testing"

func TestReferenceQueryOrderBehavior(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
	for _, step := range []referenceStep{
		referenceSelect("SELECT id FROM t ORDER BY a,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(4)}, {int64(3)}}),
		referenceSelect("SELECT id FROM t ORDER BY a ASC NULLS LAST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(4)}, {int64(3)}}),
		referenceSelect("SELECT id FROM t ORDER BY a DESC,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(1)}, {int64(4)}, {int64(2)}}),
		referenceSelect("SELECT id FROM t ORDER BY a DESC NULLS FIRST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(1)}, {int64(4)}, {int64(2)}}),
		referenceSelect("SELECT id FROM t ORDER BY a DESC NULLS LAST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}, {int64(2)}, {int64(3)}}),
		referenceSelect("SELECT a AS b,id FROM t ORDER BY b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(2), int64(4)}, {nil, int64(3)}}),
		referenceSelect("SELECT a AS b,id FROM t ORDER BY t.b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}),
		referenceSelect("SELECT a AS b,id FROM t ORDER BY b+0,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}),
		referenceSelect("SELECT id,a FROM t ORDER BY 2 DESC NULLS LAST,1", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(4), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}}),
		referenceSelect("SELECT * FROM t ORDER BY 3,1", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(4), int64(2), int64(0)}, {int64(1), int64(2), int64(1)}, {int64(3), nil, int64(2)}, {int64(2), int64(1), int64(5)}}),
		referenceSelect("SELECT a AS x,a AS x FROM t ORDER BY x,id", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {nil, nil}}),
		referenceSelect("SELECT a,count(*) AS n FROM t GROUP BY a ORDER BY n DESC,a NULLS LAST", []string{"a", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(2)}, {int64(1), int64(1)}, {nil, int64(1)}}),
		referenceSelect("SELECT a FROM t GROUP BY a ORDER BY count(*) DESC,a NULLS LAST", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {nil}}),
		referenceSelect("SELECT count(*) AS n FROM t ORDER BY count(*)", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}),
	} {
		t.Run(step.query, func(t *testing.T) { p.step(t, step); p.step(t, referencePrepared(step)) })
	}
	step := referenceSelect("SELECT id FROM t ORDER BY a*$1 NULLS LAST,id", []string{"id"}, []string{"INT4"}, nil)
	for _, backend := range p.backends() {
		stmt, err := backend.db.PrepareContext(p.ctx, step.query)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := referenceHeldStatement{backend.db, stmt, step.query}
			for i, arg := range []any{1, -1, nil, 1} {
				ids := []int64{2, 1, 4, 3}
				if i == 1 {
					ids = []int64{1, 4, 2, 3}
				}
				if i == 2 {
					ids = []int64{1, 2, 3, 4}
				}
				step.rows = referenceLimitIDs(ids...)
				step.args = []any{arg}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
				referenceRunStep(t, p.ctx, held, backend.recorder, step)
			}
		}()
	}
}

func TestReferenceQueryOrderDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,a INT,b INT)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(1,2,3)", 1))
		}
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT id FROM t ORDER BY 0", "42P10", "ORDER BY position 0 is not in select list", "27"},
			{"SELECT id FROM t ORDER BY -1", "42P10", "ORDER BY position -1 is not in select list", "27"},
			{"SELECT id FROM t ORDER BY (0)", "42P10", "ORDER BY position 0 is not in select list", "28"},
			{"SELECT id FROM t ORDER BY 2", "42P10", "ORDER BY position 2 is not in select list", "27"},
			{"SELECT id FROM t ORDER BY NULL", "42601", "non-integer constant in ORDER BY", "27"},
			{"SELECT id FROM t ORDER BY 1.5", "42601", "non-integer constant in ORDER BY", "27"},
			{"SELECT id AS x FROM t ORDER BY x+0", "42703", `column "x" does not exist`, "32"},
			{"SELECT id AS x FROM t ORDER BY t.x", "42703", `column t.x does not exist`, "32"},
			{"SELECT count(*) FROM t ORDER BY a", "42803", `column "t.a" must appear in the GROUP BY clause or be used in an aggregate function`, "33"},
			{"SELECT id FROM t ORDER BY id NULLS", "42601", `syntax error at or near "NULLS"`, "30"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				step := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				step.selectRows = true
				step.errorStage = "execute"
				p.step(t, step)
				step.errorStage = "prepare"
				p.step(t, referencePrepared(step))
			})
		}
	}
}
