package stuntgres_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"testing"
)

func TestDefaultDestinationTypesAndSpecialValues(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, `CREATE TABLE types(s SMALLINT DEFAULT 32767,l BIGINT DEFAULT 2147483648,u UUID DEFAULT '550e8400-e29b-41d4-a716-446655440000',r REAL DEFAULT 1.25,f DOUBLE PRECISION DEFAULT 2.5,n NUMERIC DEFAULT 'NaN')`, 0)
			tableCoreExec(t, db, prepared, `INSERT INTO types DEFAULT VALUES`, 1)
			want := [][]any{{int64(32767), int64(2147483648), "550e8400-e29b-41d4-a716-446655440000", float64(1.25), float64(2.5), "NaN"}}
			wantTuples(t, db, `SELECT * FROM types`, want)
			var dump bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			tableCoreExec(t, db, prepared, `INSERT INTO types DEFAULT VALUES`, 1)
			wantTuples(t, db, `SELECT * FROM types`, append(want, want[0]))
		})
	}
}

func TestDefaultTransactionFailureAndExplicitBypass(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE d(id INT,v INT DEFAULT 2147483648)`)
	execSQL(t, db, `INSERT INTO d VALUES(1,9)`)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO d VALUES(2,8)`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`INSERT INTO d(id) VALUES(3),(4)`)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
	_, err = tx.Exec(`SELECT * FROM d`)
	sqlState(t, err, "25P02")
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, `SELECT * FROM d`, [][]any{{int64(1), int64(9)}})
}
