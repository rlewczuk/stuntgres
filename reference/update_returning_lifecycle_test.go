package reference_test

import (
	"bytes"
	"reflect"
	"testing"
)

func TestReferenceUpdateReturningPriorWritesRollback(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []bool{false, true} {
			for _, savepoint := range []bool{false, true} {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						db := backend.db
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, backend.recorder, s) }
						run(referenceExec("CREATE TABLE t(id INT,a INT,s TEXT)", 0))
						run(referenceExec("INSERT INTO t VALUES(1,10,'7'),(2,20,'bad'),(3,30,'9')", 3))
						tx, err := db.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						for _, s := range []referenceStep{referenceExec("UPDATE t SET a=11 WHERE id=1", 1), referenceExec("DELETE FROM t WHERE id=3", 1)} {
							referenceRunStep(t, p.ctx, tx, backend.recorder, s)
						}
						if savepoint {
							referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("SAVEPOINT before_output", 0))
						}
						s := referenceImplicitError("UPDATE t SET a=a+1 RETURNING CAST(s AS INT)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
						s.prepared, s.selectRows, s.errorStage = prepared, query, "execute"
						referenceRunStep(t, p.ctx, tx, backend.recorder, s)
						referenceRunStep(t, p.ctx, tx, backend.recorder, referenceImplicitError("UPDATE t SET a=1", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						want := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}
						if savepoint {
							referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("ROLLBACK TO before_output", 0))
							want = [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}
							referenceRunStep(t, p.ctx, tx, backend.recorder, referenceSelect("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, want))
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						run(referenceSelect("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, want))
					})
				}
			}
		}
	}
}

func TestReferenceUpdateReturningOwnedPreparedBytes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT NOT NULL,n NUMERIC(8,2),v VARCHAR(7),ts TIMESTAMP(3),b BYTEA)", 0))
			run(referenceExec(`INSERT INTO t VALUES(1,12.30,'value','2024-01-02 03:04:05.123','\x0102')`, 1))
			held, err := db.PrepareContext(p.ctx, "UPDATE t SET b=$1 WHERE id=$2 RETURNING old.id,new.id,old.n,new.n,old.v,new.v,old.ts,new.ts,old.b,new.b")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, id := range []int{99, 1, 1} {
				input := []byte{3, 4}
				rows, err := held.QueryContext(p.ctx, input, id)
				if err != nil {
					t.Fatal(err)
				}
				input[0] = 99
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, []string{"id", "id", "n", "n", "v", "v", "ts", "ts", "b", "b"}) {
					t.Fatal(names, err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for i, typ := range []string{"INT4", "INT4", "NUMERIC", "NUMERIC", "VARCHAR", "VARCHAR", "TIMESTAMP", "TIMESTAMP", "BYTEA", "BYTEA"} {
					if columns[i].DatabaseTypeName() != typ {
						t.Fatal(i, columns[i])
					}
				}
				for _, i := range []int{0, 1} {
					nullable, known := columns[i].Nullable()
					if backend.pg {
						if known {
							t.Fatal("pq claims nullability", i)
						}
					} else if !known || nullable {
						t.Fatal("lost NOT NULL", i, nullable, known)
					}
				}
				for _, i := range []int{2, 3} {
					if precision, scale, known := columns[i].DecimalSize(); !known || precision != 8 || scale != 2 {
						t.Fatal(i, precision, scale, known)
					}
				}
				for _, i := range []int{4, 5} {
					if length, known := columns[i].Length(); !known || length != 7 {
						t.Fatal(i, length, known)
					}
				}
				if id == 99 {
					if rows.Next() {
						t.Fatal("no-match row")
					}
				} else {
					if !rows.Next() {
						t.Fatal(rows.Err())
					}
					dst := make([]any, 10)
					pointers := make([]any, 10)
					for i := range dst {
						pointers[i] = &dst[i]
					}
					if err = rows.Scan(pointers...); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(dst[9], []byte{3, 4}) {
						t.Fatal(dst)
					}
					dst[9].([]byte)[0] = 88
					if rows.Next() {
						t.Fatal("extra row")
					}
				}
				if err = rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
			}
			rows, err := held.QueryContext(p.ctx, nil, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			dst := make([]any, 10)
			pointers := make([]any, 10)
			for i := range dst {
				pointers[i] = &dst[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dst[8].([]byte), []byte{3, 4}) || dst[9] != nil {
				t.Fatal(dst)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
			run(referenceSelect("SELECT b FROM t", []string{"b"}, []string{"BYTEA"}, [][]any{{nil}}))
		})
	}
}
