package reference_test

import (
	"bytes"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningPriorWritesRollback(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []bool{false, true} {
			for _, savepoint := range []bool{false, true} {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						db := backend.DB
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, backend.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(id INT,a INT,s TEXT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,10,'7'),(2,20,'bad'),(3,30,'9')", 3))
						tx, err := db.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						for _, s := range []fixture.Step{fixture.Exec("UPDATE t SET a=11 WHERE id=1", 1), fixture.Exec("DELETE FROM t WHERE id=3", 1)} {
							fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s)
						}
						if savepoint {
							fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("SAVEPOINT before_output", 0))
						}
						s := fixture.ImplicitError("UPDATE t SET a=a+1 RETURNING CAST(s AS INT)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
						fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s)
						fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.ImplicitError("UPDATE t SET a=1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						want := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}
						if savepoint {
							fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("ROLLBACK TO before_output", 0))
							want = [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}
							fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, want))
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						run(fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, want))
					})
				}
			}
		}
	}
}

func TestReferenceUpdateReturningOwnedPreparedBytes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,n NUMERIC(8,2),v VARCHAR(7),ts TIMESTAMP(3),b BYTEA)", 0))
			run(fixture.Exec(`INSERT INTO t VALUES(1,12.30,'value','2024-01-02 03:04:05.123','\x0102')`, 1))
			held, err := db.PrepareContext(p.Ctx, "UPDATE t SET b=$1 WHERE id=$2 RETURNING old.id,new.id,old.n,new.n,old.v,new.v,old.ts,new.ts,old.b,new.b")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, id := range []int{99, 1, 1} {
				input := []byte{3, 4}
				rows, err := held.QueryContext(p.Ctx, input, id)
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
					if backend.PG {
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
			rows, err := held.QueryContext(p.Ctx, nil, 1)
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
			run(fixture.Select("SELECT b FROM t", []string{"b"}, []string{"BYTEA"}, [][]any{{nil}}))
		})
	}
}
