//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func TestReferenceDeleteReturningScalarProviders(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT,v TEXT,f BOOL,n NUMERIC(8,2),ts TIMESTAMP(3))", 0)
			for _, prepared := range []bool{false, true} {
				referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(2,'value',TRUE,12.30,'2024-01-02 03:04:05.123')", 1)
				q := "DELETE FROM t RETURNING CAST(old.id AS BIGINT) AS wide,CASE WHEN new.id IS NULL THEN old.id ELSE 0 END AS conditional,NULLIF(old.id,2) AS nulled,GREATEST(new.id,old.id) AS greatest,new.f IS UNKNOWN AS unknown,COALESCE(new.v,old.v) AS text_value,num_nulls(new.id,old.id,new.v,old.v) AS null_count,scale(n) AS numeric_scale,ts AT TIME ZONE 'UTC' AS instant,col_description(0,1) AS comment"
				s := referenceSelect(q, []string{"wide", "conditional", "nulled", "greatest", "unknown", "text_value", "null_count", "numeric_scale", "instant", "comment"}, []string{"INT8", "INT4", "INT4", "INT4", "BOOL", "TEXT", "INT4", "INT4", "TIMESTAMPTZ", "TEXT"}, [][]any{{int64(2), int64(2), nil, int64(2), true, "value", int64(2), int64(2), time.Date(2024, 1, 2, 3, 4, 5, 123000000, time.UTC), nil}})
				s.prepared = prepared
				referenceRunStep(t, p.ctx, db, r, s)
				// A random output retains its ordinary provider bounds rather than
				// inventing cross-backend equality of random draws.
				referenceLockExec(t, p.ctx, db, "INSERT INTO t(id) VALUES(2)", 1)
				rows, err := db.QueryContext(p.ctx, "DELETE FROM t RETURNING random() AS draw,new.id")
				if err != nil {
					t.Fatal(err)
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var draw float64
				var newValue any
				if err = rows.Scan(&draw, &newValue); err != nil || draw < 0 || draw >= 1 || newValue != nil {
					t.Fatal(draw, newValue, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				rows.Close()
			}
		})
	}
}
