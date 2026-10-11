package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningPreparedMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		t.Run(backend.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, db)
			r := backend.Recorder
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT NOT NULL,n NUMERIC(8,2),b BYTEA,f BOOL,ts TIMESTAMP(3))", 0)
			query := "UPDATE t SET id=id WHERE id=$1 RETURNING old.id,new.id,n,new.n,b,new.b,f,new.f,ts,new.ts,$2 AS label"
			held, err := a.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, id := range []int{2, 3} {
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES("+[]string{"2", "3"}[id-2]+",12.30,'\\x0102',TRUE,'2024-01-02 03:04:05.123')", 1)
				// Native nullable/typmod capability is tested separately; lib/pq does
				// not advertise nullable or temporal precision through database/sql.
				rows, err := held.QueryContext(p.Ctx, id, "fresh")
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
				if !backend.PG {
					if n, k := meta[1].Nullable(); !k || n {
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
				if oldID != int64(id) || newID != int64(id) || fixture.Normalize(n) != "12.30" || fixture.Normalize(b) != string([]byte{1, 2}) || fixture.Normalize(newN) != "12.30" || fixture.Normalize(newB) != string([]byte{1, 2}) || !f || newF != true || fixture.Normalize(newTS) != fixture.Normalize(ts) || label != "fresh" {
					t.Fatal(oldID, newID, n, b, newN, newB, f, newF, ts, newTS, label)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				rows.Close()
			}
			s := fixture.Select(query, []string{"id", "id", "n", "n", "b", "b", "f", "f", "ts", "ts", "label"}, []string{"INT4", "INT4", "NUMERIC", "NUMERIC", "BYTEA", "BYTEA", "BOOL", "BOOL", "TIMESTAMP", "TIMESTAMP", "TEXT"}, [][]any{})
			s.Args = []any{nil, nil}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: query}, r, s)
			star, err := a.PrepareContext(p.Ctx, "UPDATE t SET id=id RETURNING *")
			if err != nil {
				t.Fatal(err)
			}
			defer star.Close()
			fixture.LockExec(t, p.Ctx, a, "ALTER TABLE t RENAME COLUMN f TO flag", 0)
			failed, err := star.QueryContext(p.Ctx)
			if failed != nil {
				failed.Close()
				t.Fatal("changed signature exposed cursor")
			}
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"})
		})
	}
}
