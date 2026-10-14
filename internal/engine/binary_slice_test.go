package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinarySliceTypedMetadata(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(v BYTEA,s BYTEA,i INTEGER,n INTEGER)")
	for _, q := range []string{"SELECT overlay(v,s,i,n),substring(v FROM i FOR n),position(s IN v) FROM t", "SELECT overlay(v,s,i,n),substring(v FROM i FOR n),position(s IN v) FROM t WHERE n<0"} {
		result := run(t, e, q)
		cursor := independentFinalRows(result.Rows)
		for i, col := range cursor.Schema() {
			id := sqltype.Bytea
			if i == 2 {
				id = sqltype.Int4
			}
			if col.Usage.Type.ID() != id || col.Usage.Typmod != -1 || col.Usage.Collation != (sqltype.CollationRef{}) {
				t.Fatal(i, col)
			}
		}
		var frame [3]sqltype.Value
		present, err := cursor.Next(context.Background(), frame[:])
		cursor.Close()
		if present || err != nil {
			t.Fatal(present, err)
		}
	}
}
