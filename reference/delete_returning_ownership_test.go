package reference_test

import (
	"bytes"
	"context"
	"testing"
)

func TestReferenceDeleteReturningMutableOutputOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(b BYTEA)", 0)
			referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES('\\x0102'),('\\x0102')", 2)
			rows, err := db.QueryContext(ctx, "DELETE FROM t RETURNING old.b,old.b,new.b")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for range 2 {
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var first, second []byte
				var newValue any
				if err = rows.Scan(&first, &second, &newValue); err != nil || !bytes.Equal(first, []byte{1, 2}) || !bytes.Equal(second, []byte{1, 2}) || newValue != nil {
					t.Fatal(first, second, newValue, err)
				}
				first[0] = 99
				if !bytes.Equal(second, []byte{1, 2}) {
					t.Fatal("duplicate output exported shared mutable bytes", second)
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			step := referenceSelect("DELETE FROM t RETURNING old.b,old.b,new.b", []string{"b", "b", "b"}, []string{"BYTEA", "BYTEA", "BYTEA"}, [][]any{{string([]byte{1, 2}), string([]byte{1, 2}), nil}, {string([]byte{1, 2}), string([]byte{1, 2}), nil}})
			deleteReferenceRecorded(t, backend.recorder, step)
		})
	}
}
