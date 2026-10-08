//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteReturningAliases(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT NOT NULL,v TEXT)", 0)
			for _, tc := range []struct {
				query        string
				names, types []string
				want         []any
			}{
				{"DELETE FROM t RETURNING *,old.*,new.*,id+10 AS bumped,new.id IS NULL AS gone,COALESCE(new.id,old.id) AS kept", []string{"id", "v", "id", "v", "id", "v", "bumped", "gone", "kept"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT", "INT4", "BOOL", "INT4"}, []any{int64(2), nil, int64(2), nil, nil, nil, int64(12), true, int64(2)}},
				{"DELETE FROM t RETURNING WITH (NEW AS n, OLD AS o) o.id,n.id,COALESCE(n.v,'fallback') AS label", []string{"id", "id", "label"}, []string{"INT4", "INT4", "TEXT"}, []any{int64(2), nil, "fallback"}},
				{`DELETE FROM t RETURNING WITH (OLD AS "O") "O".id,new.id`, []string{"id", "id"}, []string{"INT4", "INT4"}, []any{int64(2), nil}},
				{"DELETE FROM t AS new RETURNING new.id,id", []string{"id", "id"}, []string{"INT4", "INT4"}, []any{int64(2), int64(2)}},
				{"DELETE FROM t AS new RETURNING WITH (NEW AS n) new.id,n.id", []string{"id", "id"}, []string{"INT4", "INT4"}, []any{int64(2), nil}},
				{"DELETE FROM t RETURNING WITH (OLD AS new) new.id", []string{"id"}, []string{"INT4"}, []any{int64(2)}},
				{"DELETE FROM t RETURNING WITH (NEW AS old) old.id", []string{"id"}, []string{"INT4"}, []any{nil}},
				{"DELETE FROM public.t RETURNING public.t.id,CAST(old.id AS BIGINT),new.id::BIGINT", []string{"id", "id", "id"}, []string{"INT4", "INT8", "INT8"}, []any{int64(2), int64(2), nil}},
			} {
				t.Run(tc.query, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(2,NULL),(2,NULL)", 2)
						s := referenceSelect(tc.query, tc.names, tc.types, [][]any{tc.want, tc.want})
						s.prepared, s.bag = prepared, true
						referenceRunStep(t, p.ctx, db, r, s)
						s.rows = [][]any{}
						referenceRunStep(t, p.ctx, db, r, s)
					}
				})
			}
		})
	}
}

func TestReferenceDeleteReturningPreparedMetadata(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, db)
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT NOT NULL,n NUMERIC(8,2),b BYTEA,f BOOL,ts TIMESTAMP(3))", 0)
			query := "DELETE FROM t WHERE id=$1 RETURNING old.id,new.id,n,new.n,b,new.b,f,new.f,ts,new.ts,$2 AS label"
			held, err := a.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, id := range []int{2, 3} {
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES("+[]string{"2", "3"}[id-2]+",12.30,'\\x0102',TRUE,'2024-01-02 03:04:05.123')", 1)
				// Native nullable/typmod capability is tested separately; lib/pq does
				// not advertise nullable or temporal precision through database/sql.
				rows, err := held.QueryContext(p.ctx, id, "fresh")
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(meta) != 11 || meta[0].Name() != "id" || meta[1].Name() != "id" {
					t.Fatal(meta)
				}
				if p, s, ok := meta[2].DecimalSize(); !ok || p != 8 || s != 2 {
					t.Fatal(p, s, ok)
				}
				if i == 0 {
					if n, k := meta[1].Nullable(); !k || !n {
						t.Fatal(n, k)
					}
				} else {
					if _, k := meta[1].Nullable(); k {
						t.Fatal("pq unexpectedly claims nullable")
					}
				}
				if !rows.Next() {
					t.Fatal("missing deleted row", rows.Err())
				}
				var oldID int64
				var newID any
				var n, b any
				var newN, newB any
				var f bool
				var newF any
				var ts any
				var newTS any
				var label string
				if err = rows.Scan(&oldID, &newID, &n, &newN, &b, &newB, &f, &newF, &ts, &newTS, &label); err != nil {
					t.Fatal(err)
				}
				if oldID != int64(id) || newID != nil || referenceNormalize(n) != "12.30" || referenceNormalize(b) != string([]byte{1, 2}) || newN != nil || newB != nil || !f || newF != nil || newTS != nil || label != "fresh" {
					t.Fatal(oldID, newID, n, b, newN, newB, f, newF, ts, newTS, label)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				rows.Close()
			}
			s := referenceSelect(query, []string{"id", "id", "n", "n", "b", "b", "f", "f", "ts", "ts", "label"}, []string{"INT4", "INT4", "NUMERIC", "NUMERIC", "BYTEA", "BYTEA", "BOOL", "BOOL", "TIMESTAMP", "TIMESTAMP", "TEXT"}, [][]any{})
			s.args = []any{nil, nil}
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: held, query: query}, r, s)
			star, err := a.PrepareContext(p.ctx, "DELETE FROM t RETURNING *")
			if err != nil {
				t.Fatal(err)
			}
			defer star.Close()
			referenceLockExec(t, p.ctx, a, "ALTER TABLE t RENAME COLUMN f TO flag", 0)
			failed, err := star.QueryContext(p.ctx)
			if failed != nil {
				failed.Close()
				t.Fatal("changed signature exposed cursor")
			}
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"})
		})
	}
}
