//go:build reference

package reference_test

import "testing"

func referenceLimitFixture(t *testing.T) *referencePair {
	t.Helper()
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,v INT)",0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2),(2,0),(3,3),(4,4)",4))
	return p
}

func referenceLimitIDs(ids ...int64) [][]any {
	rows := make([][]any, len(ids))
	for i, id := range ids { rows[i] = []any{id} }
	return rows
}

func TestReferenceQueryLimitBasics(t *testing.T) {
	p := referenceLimitFixture(t)
	for _, tc := range []struct{ suffix string; ids []int64 }{
		{"OFFSET 1 LIMIT 2", []int64{2,3}}, {"LIMIT 2 OFFSET 1", []int64{2,3}},
		{"LIMIT 0", nil}, {"LIMIT NULL", []int64{1,2,3,4}}, {"LIMIT ALL", []int64{1,2,3,4}},
		{"OFFSET NULL", []int64{1,2,3,4}}, {"OFFSET 0", []int64{1,2,3,4}},
		{"OFFSET 99", nil}, {"OFFSET 9223372036854775807 LIMIT 9223372036854775807", nil},
		{"OFFSET 1 LIMIT 9223372036854775807", []int64{2,3,4}},
		{"FETCH FIRST ROW ONLY", []int64{1}}, {"FETCH NEXT ROWS ONLY", []int64{1}},
		{"FETCH FIRST 2 ROWS ONLY", []int64{1,2}}, {"FETCH NEXT (1+1) ROW ONLY", []int64{1,2}},
		{"OFFSET 1 ROW FETCH NEXT 2 ROWS ONLY", []int64{2,3}},
		{"FETCH FIRST 2 ROWS ONLY OFFSET 1 ROWS", []int64{2,3}},
		{"FETCH FIRST NULL ROWS ONLY", []int64{1,2,3,4}},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			step := referenceSelect("SELECT id FROM t ORDER BY id "+tc.suffix, []string{"id"}, []string{"INT4"}, referenceLimitIDs(tc.ids...))
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
	p.step(t, referenceSelect("SELECT 7 AS n FROM t LIMIT 2", []string{"n"}, []string{"INT4"}, referenceLimitIDs(7,7)))
}

func TestReferenceQueryLimitTyping(t *testing.T) {
	p := referenceLimitFixture(t)
	for _, tc := range []struct{ bound string; ids []int64 }{
		{"'2'", []int64{1,2}}, {"2.5", []int64{1,2,3}}, {"-0.4", nil}, {"(1+1)", []int64{1,2}},
	} {
		t.Run(tc.bound, func(t *testing.T) {
			step := referenceSelect("SELECT id FROM t ORDER BY id LIMIT "+tc.bound, []string{"id"}, []string{"INT4"}, referenceLimitIDs(tc.ids...))
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM t WHERE id >= $1 ORDER BY id LIMIT $1", []string{"id"}, []string{"INT4"}, referenceLimitIDs(2,3), int32(2))))
	// Already-known float8 and text parameters are constrained by WHERE first;
	// assignment casts, rather than re-inference or text I/O, own the bound.
	p.step(t, referenceExec("CREATE TABLE typed(f DOUBLE PRECISION,s TEXT)",0))
	p.step(t, referenceExec("INSERT INTO typed VALUES(2.5,'2')",1))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM t,typed WHERE typed.f=$1 ORDER BY id LIMIT $1", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1,2), float64(2.5))))
	step := referenceImplicitError("SELECT id FROM t,typed WHERE typed.s=$1 ORDER BY id LIMIT $1", referenceDiagnostic{Code:"42804", Message:"argument of LIMIT must be type bigint, not type text", Position:"59"}, "2")
	step.selectRows = true
	step.errorStage = "prepare"
	p.step(t, referencePrepared(step))
}

func TestReferenceQueryLimitPrepared(t *testing.T) {
	p := referenceLimitFixture(t)
	// Hold a statement across executions on each backend rather than preparing
	// an equivalent fresh statement for each input.
	for _, backend := range []struct{ name string; db referenceExecutor }{
		{"postgres",p.postgres}, {"memory",p.memory},
	} {
		t.Run(backend.name, func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.ctx,"SELECT id FROM t ORDER BY id LIMIT $1 OFFSET $2")
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			for _, tc := range []struct{ count, offset any; ids []int64 }{
				{int64(2),int64(1),[]int64{2,3}}, {int64(0),int64(0),nil}, {nil,nil,[]int64{1,2,3,4}}, {int64(1),int64(3),[]int64{4}},
			} {
				rows, err := stmt.QueryContext(p.ctx,tc.count,tc.offset)
				referenceViewValues(t, relationOutcome{rows:rows,err:err}, "id","INT4",tc.ids...)
			}
			rows, err := stmt.QueryContext(p.ctx,int64(-1),int64(0))
			if rows != nil { rows.Close(); t.Fatal("failed Execute exposed rows") }
			want := referenceDiagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"2201W",Message:"LIMIT must not be negative"}
			referenceAssertDiagnostic(t,err,&want)
		})
	}
}

func TestReferenceQueryLimitDiagnostics(t *testing.T) {
	p := referenceLimitFixture(t)
	for _, tc := range []struct{ query, code, message, position string; runtime bool }{
		{"SELECT id FROM t LIMIT TRUE", "42804", "argument of LIMIT must be type bigint, not type boolean", "24", false},
		{"SELECT id FROM t OFFSET TRUE", "42804", "argument of OFFSET must be type bigint, not type boolean", "25", false},
		{"SELECT id FROM t LIMIT id", "42P10", "argument of LIMIT must not contain variables", "24", false},
		{"SELECT id FROM t OFFSET id", "42P10", "argument of OFFSET must not contain variables", "25", false},
		{"SELECT id FROM t LIMIT missing", "42703", `column "missing" does not exist`, "24", false},
		{"SELECT /*é*/ id FROM t LIMIT id", "42P10", "argument of LIMIT must not contain variables", "30", false},
		{"SELECT id FROM t LIMIT count(*)", "42803", "aggregate functions are not allowed in LIMIT", "24", false},
		{"SELECT id FROM t OFFSET count(*)", "42803", "aggregate functions are not allowed in OFFSET", "25", false},
		{"SELECT id FROM t LIMIT 'bad'", "22P02", `invalid input syntax for type bigint: "bad"`, "24", false},
		{"SELECT id FROM t LIMIT -1", "2201W", "LIMIT must not be negative", "", true},
		{"SELECT id FROM t FETCH FIRST -1 ROW ONLY", "2201W", "LIMIT must not be negative", "", true},
		{"SELECT id FROM t OFFSET -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM t LIMIT -1 OFFSET -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM t OFFSET -1 LIMIT -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM t LIMIT 0 OFFSET -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM t LIMIT -0.5", "2201W", "LIMIT must not be negative", "", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			step := referenceImplicitError(tc.query, referenceDiagnostic{Code:tc.code,Message:tc.message,Position:tc.position})
			step.selectRows = true
			step.errorStage = "execute"
			p.step(t, step)
			if !tc.runtime { step.errorStage = "prepare" }
			p.step(t, referencePrepared(step))
		})
	}
	t.Run("unauthorized even zero", referenceLimitACL)
	t.Run("occurrence conflict",func(t *testing.T) {
		// The ACL fixture resets PostgreSQL's public schema. This witness owns
		// fresh source setup instead of relying on the earlier pair's table.
		p := referenceLimitFixture(t)
		step := referenceImplicitError("SELECT $1 FROM t LIMIT $1",referenceDiagnostic{Code:"42P08",Message:"inconsistent types deduced for parameter $1",Detail:"bigint versus text",Position:"8"},int64(1))
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t,step)
		step.errorStage = "prepare"
		p.step(t,referencePrepared(step))
	})
}
