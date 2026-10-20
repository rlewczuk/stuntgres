package engine

import (
	"context"
	"testing"
)

func TestDeleteUsingZeroWidthCardinality(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t()", "CREATE TABLE s()", "INSERT INTO t DEFAULT VALUES", "INSERT INTO t DEFAULT VALUES", "INSERT INTO s DEFAULT VALUES", "INSERT INTO s DEFAULT VALUES"} {
		sessionSQL(t, session, q)
	}
	op, scope, b, target, inputs := updateFromBoundSources(t, e, session, context.Background(), "DELETE FROM t USING s RETURNING 1 AS one")
	if b.Delete.SourceWidth != 0 || len(b.Relation.Columns) != 0 || target.Count() != 2 {
		t.Fatal("width confused with occurrence count", b.Delete.SourceWidth, target.Count())
	}
	r, err := scope.deleteSource(b, target, inputs, nil)
	if r.Rows != nil {
		t.Cleanup(func() { r.Rows.Close() })
	}
	target.Close()
	if err != nil || r.Count != 2 || len(scope.stage.deletes[b.Ref.Address].Rows) != 2 {
		t.Fatal(r, err)
	}
	got := tuples(r)
	r.Rows.Close()
	if len(got) != 2 || len(got[0]) != 1 || got[0][0] != int64(1) || got[1][0] != int64(1) {
		t.Fatal(got)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	result := sessionSQL(t, session, "SELECT 1 FROM t")
	if rows := tuples(result); len(rows) != 0 {
		t.Fatal(rows)
	}
	result.Rows.Close()
	result = sessionSQL(t, session, "SELECT 1 FROM s")
	if rows := tuples(result); len(rows) != 2 {
		t.Fatal(rows)
	}
	result.Rows.Close()
	lockEmpty(t, e)
}
