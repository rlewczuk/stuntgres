package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceUpdateReturningCommentSnapshot(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceExec("COMMENT ON TABLE t IS 'committed table'", 0))
			run(referenceExec("COMMENT ON COLUMN t.id IS 'committed column'", 0))
			for _, prepared := range []bool{false, true} {
				tx, err := db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				s := referenceSelect("UPDATE t SET id=id RETURNING id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment", []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}})
				s.prepared = prepared
				referenceRunStep(t, p.ctx, tx, backend.recorder, s)
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment FROM t", s.columns, s.types, s.rows))
			}
		})
	}
}

func TestReferenceUpdateReturningOwnPredecessor(t *testing.T) {
	for _, commit := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			t.Run(backend.name, func(t *testing.T) {
				db := backend.db
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT,a INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
				tx, err := db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, s := range []referenceStep{
					referenceExec("INSERT INTO t VALUES(3,30)", 1), referenceExec("UPDATE t SET a=11 WHERE id=1", 1),
					referenceSelect("UPDATE t SET a=a+1 WHERE id=3 RETURNING old.a,new.a", []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(30), int64(31)}}),
					referenceSelect("UPDATE t SET a=a+1 WHERE id=1 RETURNING old.a,new.a", []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(12)}}),
					referenceSelect("DELETE FROM t WHERE id=3 RETURNING a", []string{"a"}, []string{"INT4"}, [][]any{{int64(31)}}),
				} {
					referenceRunStep(t, p.ctx, tx, backend.recorder, s)
				}
				run(referenceSelect("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				value := int64(10)
				if commit {
					value = 12
				}
				run(referenceSelect("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), value}}))
			})
		}
	}
}

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

func TestReferenceUpdateReturningCapturedRegclass(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
			rows, err := backend.db.QueryContext(p.ctx, "UPDATE t SET id=id RETURNING 't'::regclass AS target")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			run(referenceExec("ALTER TABLE t RENAME TO renamed", 0))
			if !backend.pg {
				if err = stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
			}
			updateCoreRetained(t, backend.pg, rows, []string{"target"}, []string{"REGCLASS"}, [][]any{{"t"}, {"t"}}, true)
		})
	}
}

func TestReferenceUpdateReturningTypedVersions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT,d DATE,clock TIME(3),stamp TIMESTAMP(3),u UUID,n NUMERIC(8,2),b BYTEA)", 0))
			run(referenceExec(`INSERT INTO t VALUES(1,'2000-01-02','04:05:06.124','2000-01-02 04:05:06.124','a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11',12.30,'\x0102'),(2,NULL,NULL,NULL,NULL,NULL,NULL)`, 2))
			var date, clock any = "2000-01-02", "04:05:06.124"
			if backend.pg {
				date = time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
				clock = time.Date(0, 1, 1, 4, 5, 6, 124000000, time.UTC)
			}
			stamp := time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC)
			s := referenceSelect("UPDATE t SET id=id RETURNING old.d,new.d,old.clock,new.clock,old.stamp,new.stamp,old.u,new.u,old.n,new.n,old.b,new.b", []string{"d", "d", "clock", "clock", "stamp", "stamp", "u", "u", "n", "n", "b", "b"}, []string{"DATE", "DATE", "TIME", "TIME", "TIMESTAMP", "TIMESTAMP", "UUID", "UUID", "NUMERIC", "NUMERIC", "BYTEA", "BYTEA"}, [][]any{{date, date, clock, clock, stamp, stamp, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "12.30", "12.30", "\x01\x02", "\x01\x02"}, {nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}})
			s.prepared, s.bag = true, true
			run(s)
			run(referenceSelect("UPDATE t SET id=3 WHERE d=$1 RETURNING old.id,new.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{}, nil))
		})
	}
}

func TestReferenceUpdateReturningPreCanceled(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
			if !backend.pg {
				ctx, cancel := context.WithCancel(p.ctx)
				cancel()
				rows, err := backend.db.QueryContext(ctx, "UPDATE t SET id=id+1 RETURNING *")
				if rows != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(rows, err)
				}
			}
			run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
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
