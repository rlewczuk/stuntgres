package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestBinaryBasicConsumersAndNativeReplay(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)")
	execSQL(t, db, `INSERT INTO t VALUES(1,'\x00ff8041008042ff00','\xff8000')`)
	execSQL(t, db, "CREATE TABLE dst(v BYTEA)")
	result, err := db.Exec("INSERT INTO dst SELECT v||s FROM t WHERE length(v)=9")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	cat := []byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 255, 128, 0}
	wantTuples(t, db, "SELECT v FROM dst", [][]any{{cat}})
	wantTuples(t, db, "SELECT length(v),count(*) FROM t GROUP BY length(v)", [][]any{{int64(9), int64(1)}})
	wantTuples(t, db, "SELECT length(v) AS n,count(*) FROM t GROUP BY 1", [][]any{{int64(9), int64(1)}})
	wantTuples(t, db, `SELECT bit_length(v) FROM t WHERE btrim(v,s)='\x41008042' ORDER BY octet_length(v)`, [][]any{{int64(72)}})
	wantTuples(t, db, "SELECT CASE WHEN length(v)=9 THEN btrim(v,s) ELSE v END FROM t", [][]any{{[]byte{65, 0, 128, 66}}})
	stmt, err := db.Prepare("UPDATE dst SET v=btrim(v,$1) RETURNING v,length(v)")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := stmt.Query([]byte{0, 128, 255})
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var raw []byte
	var n int64
	if err := rows.Scan(&raw, &n); err != nil || !reflect.DeepEqual(raw, []byte{65, 0, 128, 66}) || n != 4 {
		t.Fatal(raw, n, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	stmt.Close()
}

func TestBinaryBasicReplayAtomicityAndCancellation(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)", `INSERT INTO t VALUES(1,'\x00ff8041008042ff00','\xff8000')`, "CREATE TABLE dst(v BYTEA)", "INSERT INTO dst SELECT v||s FROM t", "CREATE VIEW binary_view AS SELECT v||s AS cat,bit_length(v) AS bits,octet_length(v) AS octets,length(v) AS len,btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt FROM t", `CREATE VIEW binary_literal_view AS SELECT btrim(v,'\x0080ff') AS bt,v||'\x00' AS cat FROM t`} {
		execSQL(t, db, q)
	}
	cat := []byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 255, 128, 0}
	expected := [][]any{{cat, int64(72), int64(9), int64(9), []byte{65, 0, 128, 66}, []byte{65, 0, 128, 66, 255, 0}, []byte{0, 255, 128, 65, 0, 128, 66}}}
	held, err := db.Query("SELECT * FROM binary_view")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM binary_view", expected)
	wantTuples(t, db, "SELECT * FROM binary_literal_view", [][]any{{[]byte{65, 0, 128, 66}, []byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 0}}})
	wantTuples(t, db, "SELECT v FROM dst", [][]any{{cat}})
	wantTuples(t, db, "SELECT v,s FROM t", [][]any{{[]byte{0, 255, 128, 65, 0, 128, 66, 255, 0}, []byte{255, 128, 0}}})
	types, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		want := "BYTEA"
		if i >= 1 && i <= 3 {
			want = "INT4"
		}
		if typ.DatabaseTypeName() != want {
			t.Fatal(typ)
		}
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	got := make([]any, 7)
	dest := make([]any, 7)
	for i := range got {
		dest[i] = &got[i]
	}
	if err := held.Scan(dest...); err != nil || !reflect.DeepEqual(got, expected[0]) {
		t.Fatal(got, err)
	}
	got[0].([]byte)[0] = 99
	if held.Next() || held.Err() != nil {
		t.Fatal(held.Err())
	}
	held.Close()
	wantTuples(t, db, "SELECT v FROM dst", [][]any{{cat}})
	_, err = db.Exec(`INSERT INTO dst SELECT v||'\q' FROM t`)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "27"})
	wantTuples(t, db, "SELECT v FROM dst", [][]any{{cat}})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if rows, err := db.QueryContext(canceled, "SELECT btrim(v,s) FROM t"); !errors.Is(err, context.Canceled) {
		if rows != nil {
			rows.Close()
		}
		t.Fatal(err)
	}
	stmt, err := db.Prepare("INSERT INTO dst SELECT v||s FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := stmt.ExecContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT v FROM dst", [][]any{{cat}})
}

func TestBinaryBasicLateInputErrorPublishesNoPrefix(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE dst(v BYTEA)")
	execSQL(t, db, `INSERT INTO dst VALUES('\x41')`)
	q := `INSERT INTO dst VALUES($1),(btrim($1,'\q'))`
	_, err := db.Exec(q, []byte{0, 255})
	want := stuntgres.Error{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "38"}
	typeCoreDiagnostic(t, err, want)
	stmt, err := db.Prepare(q)
	if stmt != nil {
		stmt.Close()
		t.Fatal("failed input published statement")
	}
	typeCoreDiagnostic(t, err, want)
	wantTuples(t, db, "SELECT v FROM dst", [][]any{{[]byte{65}}})
}
