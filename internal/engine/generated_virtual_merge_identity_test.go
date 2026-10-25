package engine

import (
	"context"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"testing"
)

func TestGeneratedVirtualMergeSavedSourceIdentity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{`CREATE TABLE t(id INT,v INT)`, `INSERT INTO t VALUES(1,10),(2,20),(3,30)`, `CREATE TABLE l(x INT)`, `INSERT INTO l VALUES(1),(2),(3)`, `CREATE TABLE g(a INT,c INT GENERATED ALWAYS AS(1) VIRTUAL)`, `INSERT INTO g(a) VALUES(NULL)`, `CREATE TABLE witness(n INT)`, `INSERT INTO witness VALUES(1)`} {
		sessionSQL(t, s, q)
	}
	op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), `MERGE INTO t USING l LEFT JOIN g p ON l.x=1 LEFT JOIN g q ON l.x=2 ON t.id=l.x WHEN MATCHED THEN UPDATE SET v=(SELECT CASE WHEN p.c IS NULL THEN q.c ELSE p.c END FROM witness WHERE n=1)`)
	defer op.Done()
	defer target.Close()
	defer inputs.Close()
	if err := exec.SelectQuerySources(op.Ctx, inputs); err != nil {
		t.Fatal(err)
	}
	state := &exec.State{Source: inputs, Input: scope.Input, Output: scope.Output, VirtualReads: dml.MergeHasVirtualReads(b.Merge)}
	if !state.VirtualReads {
		t.Fatal("MERGE action did not establish virtual demand")
	}
	cursor, err := exec.OpenNode(op.Ctx, state, b.Merge.Source)
	if err != nil {
		t.Fatal(err)
	}
	authority := inputs.Authority
	rows, err := dml.AssembleMergeRows(scope.WriteHost(), scope.mergeInput(op.Ctx, b, target, nil, nil, "public"), &cursor, state, false)
	if err != nil || !cursor.Closed || rows == nil || len(rows.Values) != 3 || len(scope.Stage.Rows) != 0 {
		t.Fatal("saved source phase or publication fence", rows, err, cursor.Closed, scope.Stage.Rows)
	}
	if inputs.Authority != authority || authority == nil || inputs.Sources == nil || scope.Active() != nil {
		t.Fatal("borrowed cursor close retired live input authority")
	}
	seen := [3]bool{}
	for _, row := range rows.Values {
		id, _ := row[0].Integer()
		switch id {
		case 1, 2:
			v, access := row[1].Integer()
			if access != nil || v != 1 {
				t.Fatal("present drained candidate lost logical value", row)
			}
			seen[id-1] = true
		case 3:
			if !row[1].IsNull() || row[1].Type() != b.Relation.Columns[1].Usage.Type {
				t.Fatal("absent drained candidate recomputed generation", row)
			}
			seen[2] = true
		default:
			t.Fatal("unexpected candidate", row)
		}
	}
	if seen != [3]bool{true, true, true} {
		t.Fatal("lost candidate", seen)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}
