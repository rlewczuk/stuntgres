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
			referenceLockExec(t, p.ctx, db, "COMMENT ON COLUMN t.v IS 'payload'", 0)
			referenceLockExec(t, p.ctx, db, "COMMENT ON TABLE t IS 'returning target'", 0)
			for _, prepared := range []bool{false, true} {
				referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(2,'value',TRUE,12.30,'2024-01-02 03:04:05.123')", 1)
				q := "DELETE FROM t RETURNING CAST(old.id AS BIGINT) AS wide,CASE WHEN new.id IS NULL THEN old.id ELSE 0 END AS conditional,NULLIF(old.id,2) AS nulled,GREATEST(new.id,old.id) AS greatest,new.f IS UNKNOWN AS unknown,COALESCE(new.v,old.v) AS text_value,num_nulls(new.id,old.id,new.v,old.v) AS null_count,scale(n) AS numeric_scale,ts AT TIME ZONE 'UTC' AS instant,col_description(0,1) AS comment"
				s := referenceSelect(q, []string{"wide", "conditional", "nulled", "greatest", "unknown", "text_value", "null_count", "numeric_scale", "instant", "comment"}, []string{"INT8", "INT4", "INT4", "INT4", "BOOL", "TEXT", "INT4", "INT4", "TIMESTAMPTZ", "TEXT"}, [][]any{{int64(2), int64(2), nil, int64(2), true, "value", int64(2), int64(2), time.Date(2024, 1, 2, 3, 4, 5, 123000000, time.UTC), nil}})
				s.prepared = prepared
				referenceRunStep(t, p.ctx, db, r, s)
				referenceLockExec(t, p.ctx, db, "INSERT INTO t(id) VALUES(2)", 1)
				s = referenceSelect("DELETE FROM t RETURNING col_description('t'::regclass,2) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment,NULL::regclass AS absent,'t'::regclass AS target,$1::regclass AS parameter", []string{"column_comment", "table_comment", "absent", "target", "parameter"}, []string{"TEXT", "TEXT", "REGCLASS", "REGCLASS", "REGCLASS"}, [][]any{{"payload", "returning target", nil, "t", "t"}})
				s.prepared = prepared
				s.args = []any{"t"}
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

func TestReferenceDeleteReturningRegclassBoundary(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, db, "CREATE TABLE rt(v TEXT)", 0)
			for _, prepared := range []bool{false, true} {
				referenceLockExec(t, p.ctx, db, "INSERT INTO rt VALUES(NULL)", 1)
				if i == 0 {
					for _, tc := range []struct {
						query string
						want  referenceDiagnostic
					}{
						{"DELETE FROM rt RETURNING old.v::regclass,new.v::regclass", referenceDiagnostic{Code: "0A000", Message: "qualified column references are unsupported", Position: "26"}},
						{"DELETE FROM rt RETURNING new.v::regclass", referenceDiagnostic{Code: "0A000", Message: "qualified column references are unsupported", Position: "26"}},
						{"DELETE FROM rt RETURNING v::regclass", referenceDiagnostic{Code: "42804", Message: "incompatible source and destination types"}},
					} {
						s := referenceImplicitError(tc.query, tc.want)
						s.prepared, s.selectRows, s.errorStage = prepared, true, "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, db, r, s)
						referenceRunStep(t, p.ctx, db, r, referenceSelect("SELECT v FROM rt", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
					}
					referenceLockExec(t, p.ctx, db, "DELETE FROM rt", 1)
				} else {
					// PostgreSQL admits TEXT-column casts; native's existing bounded
					// REGCLASS provider does not. NULL data cannot change admission.
					s := referenceSelect("DELETE FROM rt RETURNING old.v::regclass,new.v::regclass", []string{"v", "v"}, []string{"REGCLASS", "REGCLASS"}, [][]any{{nil, nil}})
					s.prepared = prepared
					referenceRunStep(t, p.ctx, db, r, s)
				}
			}
		})
	}
}
