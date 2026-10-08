package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceDeleteReturningAliases(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db, r := backend.db, backend.recorder
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
				{"DELETE FROM t AS d WHERE d.v IS NULL RETURNING id", []string{"id"}, []string{"INT4"}, []any{int64(2)}},
				{"DELETE FROM t RETURNING CAST(COALESCE(new.id,old.id) AS BIGINT) AS kept,int8(old.id) AS wide,new.id::BIGINT AS gone", []string{"kept", "wide", "gone"}, []string{"INT8", "INT8", "INT8"}, []any{int64(2), int64(2), nil}},
				{"DELETE FROM t RETURNING WITH (OLD AS o,NEW AS n) CAST(o.id AS BIGINT) AS predecessor,CAST(n.id AS BIGINT) AS replacement", []string{"predecessor", "replacement"}, []string{"INT8", "INT8"}, []any{int64(2), nil}},
				{"DELETE FROM t RETURNING NULL::regclass AS predecessor,NULL::regclass AS replacement", []string{"predecessor", "replacement"}, []string{"REGCLASS", "REGCLASS"}, []any{nil, nil}},
				{"DELETE FROM t RETURNING CASE WHEN new.id IS NULL THEN 'ok' ELSE v END AS label,abs(old.id),old.id IS DISTINCT FROM new.id", []string{"label", "abs", "?column?"}, []string{"TEXT", "INT4", "BOOL"}, []any{"ok", int64(2), true}},
				{"DELETE FROM t RETURNING WITH (NEW AS n,OLD AS o) o.id,n.id,COALESCE(n.v,'fallback') AS label", []string{"id", "id", "label"}, []string{"INT4", "INT4", "TEXT"}, []any{int64(2), nil, "fallback"}},
			} {
				t.Run(tc.query, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						var executor referenceExecutor = db
						if prepared {
							held, err := db.PrepareContext(p.ctx, tc.query)
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
							executor = referenceHeldStatement{referenceExecutor: db, statement: held, query: tc.query}
						}
						referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(2,NULL),(2,NULL)", 2)
						s := referenceSelect(tc.query, tc.names, tc.types, [][]any{tc.want, tc.want})
						s.bag = true
						referenceRunStep(t, p.ctx, executor, r, s)
						s.rows = [][]any{}
						referenceRunStep(t, p.ctx, executor, r, s)
					}
				})
			}
		})
	}
}

func TestReferenceDeleteReturningPreparedMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			a, _ := referenceLockConnections(t, p.ctx, db)
			r := backend.recorder
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
				if !backend.pg {
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
				var oldID any
				var newID any
				var n, b any
				var newN, newB any
				var f any
				var newF any
				var ts any
				var newTS any
				var label any
				if err = rows.Scan(&oldID, &newID, &n, &newN, &b, &newB, &f, &newF, &ts, &newTS, &label); err != nil {
					t.Fatal(err)
				}
				for i, value := range []any{oldID, newID, n, newN, b, newB, f, newF, ts, newTS, label} {
					referenceAssertRawValue(t, backend.pg, []string{"INT4", "INT4", "NUMERIC", "NUMERIC", "BYTEA", "BYTEA", "BOOL", "BOOL", "TIMESTAMP", "TIMESTAMP", "TEXT"}[i], value)
				}
				if oldID != int64(id) || newID != nil || referenceNormalize(n) != "12.30" || referenceNormalize(b) != string([]byte{1, 2}) || newN != nil || newB != nil || f != true || newF != nil || newTS != nil || label != "fresh" {
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

func TestReferenceDeleteReturningCrossKindAliases(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db, r := backend.db, backend.recorder
			a, _ := referenceLockConnections(t, p.ctx, db)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT NOT NULL,v TEXT NOT NULL)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,'survivor')", 1)
			for _, tc := range []struct {
				options, output string
				want            []any
				nullable        []bool
			}{
				{"OLD AS new", "new.id AS alias_id,new.v AS alias_v", []any{int64(2), "payload", int64(2), "payload"}, []bool{false, false, false, false}},
				{"NEW AS old", "old.id AS alias_id,old.v AS alias_v", []any{int64(2), "payload", nil, nil}, []bool{false, false, true, true}},
				{"OLD AS new,NEW AS n", "new.id AS old_id,new.v AS old_v,n.id AS new_id,n.v AS new_v", []any{int64(2), "payload", int64(2), "payload", nil, nil}, []bool{false, false, false, false, true, true}},
				{"NEW AS old,OLD AS o", "o.id AS old_id,o.v AS old_v,old.id AS new_id,old.v AS new_v", []any{int64(2), "payload", int64(2), "payload", nil, nil}, []bool{false, false, false, false, true, true}},
				{"OLD AS new,NEW AS old", "new.id AS old_id,new.v AS old_v,old.id AS new_id,old.v AS new_v", []any{int64(2), "payload", int64(2), "payload", nil, nil}, []bool{false, false, false, false, true, true}},
				{"NEW AS old,OLD AS new", "new.id AS old_id,new.v AS old_v,old.id AS new_id,old.v AS new_v", []any{int64(2), "payload", int64(2), "payload", nil, nil}, []bool{false, false, false, false, true, true}},
			} {
				for _, prepared := range []bool{false, true} {
					query := "DELETE FROM t WHERE id=2 RETURNING WITH (" + tc.options + ") id AS target_id,v AS target_v," + tc.output
					names, types := []string{"target_id", "target_v", "alias_id", "alias_v"}, []string{"INT4", "TEXT", "INT4", "TEXT"}
					if len(tc.want) == 6 {
						names, types = []string{"target_id", "target_v", "old_id", "old_v", "new_id", "new_v"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}
					}
					var executor referenceExecutor = a
					var held *sql.Stmt
					if prepared {
						var err error
						held, err = a.PrepareContext(p.ctx, query)
						if err != nil {
							t.Fatal(tc.options, err)
						}
						executor = referenceHeldStatement{referenceExecutor: a, statement: held, query: query}
					}
					for step, want := range [][][]any{{tc.want, tc.want}, {}, {tc.want, tc.want}} {
						if step != 1 {
							referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(2,'payload'),(2,'payload')", 2)
						}
						s := referenceSelect(query, names, types, want)
						s.bag = true
						{
							rows, err := executor.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, r), query)
							if err != nil {
								t.Fatal(err)
							}
							meta, err := rows.ColumnTypes()
							if err != nil || len(meta) != len(tc.nullable) {
								t.Fatal(meta, err)
							}
							for i, wantNullable := range tc.nullable {
								n, k := meta[i].Nullable()
								if !backend.pg && (!k || n != wantNullable) {
									t.Fatal(tc.options, i, n, k, wantNullable)
								}
								if backend.pg && k {
									t.Fatal("pq unexpectedly claims nullability", i, n)
								}
							}
							deleteReferenceConsume(t, backend.pg, rows, nil, s)
							deleteReferenceRecorded(t, r, s)
						}
					}
					if held != nil {
						held.Close()
					}
					referenceRunStep(t, p.ctx, a, r, referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "survivor"}}))
				}
			}
		})
	}
}
