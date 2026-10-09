package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceUpdateReturningOwnedMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT NOT NULL,n NUMERIC(8,2),v VARCHAR(7),ts TIMESTAMP(3),b BYTEA)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,12.30,'value','2024-01-02 03:04:05.123','\\x0102')")
		held, err := db.Prepare("UPDATE t SET b=$1 WHERE id=$2 RETURNING old.id,new.id,old.n,new.n,old.v,new.v,old.ts,new.ts,old.b,new.b")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, id := range []int{99, 1, 1} {
			input := []byte{3, 4}
			rows, err := held.Query(input, id)
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
				if nullable, known := columns[i].Nullable(); known != !backend.pg || nullable {
					t.Fatal("real version lost NOT NULL", i, nullable, known)
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
		rows, err := held.Query(nil, 1)
		if err != nil {
			t.Fatal(err)
		}
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
		if !reflect.DeepEqual(dst[8], []byte{3, 4}) || dst[9] != nil {
			t.Fatal(dst)
		}
		rows.Close()
		rows, err = db.Query("SELECT b FROM t")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"b"}, types: []string{"BYTEA"}, rows: [][]any{{nil}}, bag: false})
	}
}

func TestReferenceUpdateReturningPreflightEmpty(t *testing.T) {
	updateReturningPreflightContextReplay(t)
	for _, populate := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			db := backend.db
			updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
			if populate {
				updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1)")
			}
			held, err := db.Prepare("UPDATE t SET id=2 WHERE FALSE RETURNING CAST($1::TEXT AS INTEGER)")
			if err != nil {
				t.Fatal(err)
			}
			rows, err := held.Query("bad")
			if rows != nil {
				rows.Close()
				t.Fatal("preflight failure cursor")
			}
			updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
			rows, err = held.Query("7")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"int4"}, types: []string{"INT4"}, rows: [][]any{}, bag: false})
			held.Close()

		}
	}
}

func updateReturningPreflightContextReplay(t *testing.T) {
	t.Helper()
	for _, populate := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			t.Run(backend.name, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				if populate {
					run(referenceExec("INSERT INTO t VALUES(1)", 1))
				}
				q := "UPDATE t SET id=2 WHERE FALSE RETURNING CAST($1::TEXT AS INTEGER)"
				held, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				x := referenceHeldStatement{referenceExecutor: backend.db, statement: held, query: q}
				bad := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad")
				bad.selectRows, bad.errorStage = true, "execute"
				referenceRunStep(t, p.ctx, x, backend.recorder, bad)
				referenceRunStep(t, p.ctx, x, backend.recorder, referenceSelect(q, []string{"int4"}, []string{"INT4"}, [][]any{}, "7"))
				want := [][]any{}
				if populate {
					want = [][]any{{int64(1)}}
				}
				run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, want))
			})
		}
	}
}
