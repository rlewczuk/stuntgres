package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceNumericLiteralsSourceConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db := backend.db
					exec := func(query string, count int64, args ...any) {
						t.Helper()
						var result sql.Result
						var err error
						if prepared {
							stmt, e := db.Prepare(query)
							if e != nil {
								t.Fatal(e)
							}
							defer stmt.Close()
							result, err = stmt.Exec(args...)
						} else {
							result, err = db.Exec(query, args...)
						}
						if err != nil {
							t.Fatal(err)
						}
						if n, e := result.RowsAffected(); e != nil || n != count {
							t.Fatalf("affected %d: %v, want %d", n, e, count)
						}
						if _, e := result.LastInsertId(); e == nil {
							t.Fatal("LastInsertId unexpectedly supported")
						}
					}
					check := func(s referenceStep) {
						t.Helper()
						var rows *sql.Rows
						var err error
						if prepared {
							stmt, e := db.Prepare(s.query)
							if e != nil {
								t.Fatal(e)
							}
							defer stmt.Close()
							rows, err = stmt.Query()
						} else {
							rows, err = db.Query(s.query)
						}
						if err != nil {
							t.Fatal(err)
						}
						lexicalRows(t, backend.pg, rows, s, nil)
					}
					exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0)
					exec("INSERT INTO t VALUES (1,0b1010,0o12),(2,0xa,1_000)", 2)
					check(referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}}))
					stmt, err := db.Prepare("INSERT INTO t VALUES ($1,0B_1_010,0X_A)")
					if err != nil {
						t.Fatal(err)
					}
					result, err := stmt.Exec(int64(3))
					stmt.Close()
					if err != nil {
						t.Fatal(err)
					}
					if n, e := result.RowsAffected(); e != nil || n != 1 {
						t.Fatalf("affected %d: %v", n, e)
					}
					check(referenceSelect("SELECT id,i,b FROM t WHERE i=0b1010 ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}, {int64(3), int64(10), int64(10)}}))
					check(referenceSelect("SELECT id,i,b FROM t WHERE b=1_000", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(10), int64(1000)}}))
					exec("CREATE TABLE endpoints(id INT,b BIGINT)", 0)
					var want [][]any
					for i, literal := range numericEndpoints() {
						exec(fmt.Sprintf("INSERT INTO endpoints VALUES (%d,%s)", i, literal), 1)
						value := int64(9223372036854775807)
						if i%2 == 1 {
							value = -9223372036854775808
						}
						want = append(want, []any{int64(i), value})
					}
					check(referenceSelect("SELECT id,b FROM endpoints ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want))
					exec("CREATE TABLE bounds(i INT,b BIGINT,label TEXT)", 0)
					exec("INSERT INTO bounds VALUES (-0x8000_0000,0x8000_0000,0xa),(0x7FFF_FFFF,010,1_000)", 2)
					check(referenceSelect("SELECT i,b,label FROM bounds ORDER BY i", []string{"i", "b", "label"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(-2147483648), int64(2147483648), "10"}, {int64(2147483647), int64(10), "1000"}}))
				})
			}
		})
	}
}
