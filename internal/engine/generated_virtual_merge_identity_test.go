package engine

import (
	"context"
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
	defer target.close()
	defer inputs.close()
	if err := selectQuerySources(op.ctx, inputs); err != nil {
		t.Fatal(err)
	}
	state := &queryState{source: inputs, input: scope.input, output: scope.output, virtualReads: b.merge.hasVirtualReads()}
	if !state.virtualReads {
		t.Fatal("MERGE action did not establish virtual demand")
	}
	cursor, err := openQueryNode(op.ctx, state, b.merge.source)
	if err != nil {
		t.Fatal(err)
	}
	authority := inputs.authority
	rows, err := e.assembleMergeRows(op.ctx, scope, b, target, &cursor, state, "public", false)
	if err != nil || !cursor.closed || rows == nil || len(rows.values) != 3 || len(scope.stage.rows) != 0 {
		t.Fatal("saved source phase or publication fence", rows, err, cursor.closed, scope.stage.rows)
	}
	if inputs.authority != authority || authority == nil || inputs.sources == nil || scope.active() != nil {
		t.Fatal("borrowed cursor close retired live input authority")
	}
	seen := [3]bool{}
	for _, row := range rows.values {
		id, _ := row[0].Integer()
		switch id {
		case 1, 2:
			v, access := row[1].Integer()
			if access != nil || v != 1 {
				t.Fatal("present drained candidate lost logical value", row)
			}
			seen[id-1] = true
		case 3:
			if !row[1].IsNull() || row[1].Type() != b.relation.columns[1].Usage.Type {
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
