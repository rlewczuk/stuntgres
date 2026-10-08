package stuntgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

type oidValuer struct{ value driver.Value }

func (v oidValuer) Value() (driver.Value, error) { return v.value, nil }

func TestOIDBindsMetadata(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE o(id INT,v OID,w OID)")
	stmt, err := db.Prepare("INSERT INTO o VALUES($1,$2,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	inputs := []any{"0", []byte("2147483648"), int32(-1), int64(-1), uint64(4294967295), oidValuer{"1"}, nil}
	want := []any{int64(0), int64(2147483648), int64(4294967295), int64(4294967295), int64(4294967295), int64(1), nil}
	for i, in := range inputs {
		result, err := stmt.Exec(i, in)
		if err != nil {
			t.Fatal(i, err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	for i, in := range []any{"0", "4294967295", nil} {
		execSQL(t, db, "INSERT INTO o VALUES($1,$2,$2)", i+10, in)
	}
	owned := []byte("4294967295")
	execSQL(t, db, "INSERT INTO o VALUES(20,$1,$1)", owned)
	owned[0] = '0'
	for i, w := range want {
		wantTuples(t, db, "SELECT v,w FROM o WHERE id=$1", [][]any{{w, w}}, i)
	}
	wantTuples(t, db, "SELECT v FROM o WHERE id=20", [][]any{{int64(4294967295)}})
	query, err := db.Prepare("SELECT v FROM o WHERE v=$1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	for _, tc := range []struct {
		arg   any
		want  any
		count int
	}{{"0", int64(0), 2}, {"4294967295", int64(4294967295), 5}, {nil, nil, 0}} {
		rows, err := query.Query(tc.arg)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var v any
			if err = rows.Scan(&v); err != nil || v != tc.want {
				t.Fatal(v, tc.want, err)
			}
			count++
		}
		if err = rows.Err(); err != nil || count != tc.count {
			t.Fatal(count, tc.count, err)
		}
		rows.Close()
	}
	for _, q := range []string{"SELECT v FROM o WHERE id=20", "SELECT v FROM o WHERE id=6", "SELECT v FROM o WHERE id=999"} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		ct := types[0]
		if ct.DatabaseTypeName() != "OID" || ct.ScanType() != reflect.TypeFor[int64]() {
			t.Fatal(ct)
		}
		if _, ok := ct.Length(); ok {
			t.Fatal("length")
		}
		if _, _, ok := ct.DecimalSize(); ok {
			t.Fatal("precision")
		}
		for rows.Next() {
			var v sql.NullInt64
			if err = rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	// database/sql closes its Rows wrapper at EOF; metadata retention is the
	// native driver's public rows contract, not a post-close wrapper API.
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, q := range []string{"SELECT v FROM o WHERE id=20", "SELECT v FROM o WHERE id=6", "SELECT v FROM o WHERE id=999"} {
		rows, err := raw.(driver.QueryerContext).QueryContext(context.Background(), q, nil)
		if err != nil {
			t.Fatal(err)
		}
		checkMetadata := func() {
			t.Helper()
			if rows.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "OID" ||
				rows.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[int64]() {
				t.Fatal("retained OID metadata")
			}
		}
		checkMetadata()
		values := make([]driver.Value, 1)
		for {
			err = rows.Next(values)
			if err == io.EOF {
				break
			}
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
		}
		checkMetadata()
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
		checkMetadata()
	}
	for _, destination := range []any{new(int64), new(string), new([]byte), new(any), new(sql.NullInt64)} {
		if err = db.QueryRow("SELECT v FROM o WHERE id=20").Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	_, err = stmt.Exec(30, uint64(math.MaxInt64)+1)
	var sqlerr *stuntgres.Error
	if err == nil || errors.As(err, &sqlerr) {
		t.Fatal(err)
	}
	_, err = stmt.Exec(30, []byte(nil))
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type oid: ""`,Context:"unnamed portal parameter $2 = '...'"})
	_, err = db.Prepare("SELECT $1 FROM o WHERE v=$1")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "oid versus text", Position: "8"})
	var nonnullable int64
	err = db.QueryRow("SELECT v FROM o WHERE id=6").Scan(&nonnullable)
	if err == nil || errors.As(err, &sqlerr) {
		t.Fatal("NULL scan boundary", err)
	}
}

func TestOIDCastsComparisonsConsumers(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE one(s SMALLINT,i INT,b BIGINT,v OID)")
	execSQL(t, db, "INSERT INTO one VALUES(-1,-1,4294967295,'4294967295')")
	for _, q := range []string{"SELECT CAST(i AS oid),s::oid,b::oid,v::int4,v::int8,v::text,oid('0377') FROM one", "SELECT i::oid,s::oid,CAST(b AS oid),CAST(v AS int4),CAST(v AS int8),CAST(v AS text),oid('0377') FROM one"} {
		wantTuples(t, db, q, [][]any{{int64(4294967295), int64(4294967295), int64(4294967295), int64(-1), int64(4294967295), "4294967295", int64(255)}})
	}
	for _, q := range []string{"SELECT v=i,i=v,v=s,s=v,v=b,b=v FROM one", "SELECT v>0,0<v,v>=i,i<=v,v<>0,v!=0 FROM one"} {
		wantTuples(t, db, q, [][]any{{true, true, true, true, true, true}})
	}
	wantTuples(t, db, "SELECT v<v,v<=v,v=v,v<>v,v>v,v>=v,v=NULL FROM one", [][]any{{false, true, true, false, false, true, nil}})
	wantTuples(t, db, "SELECT '2147483648'::oid::int4,'2147483648'::oid::int8,NULL::oid::int8 FROM one", [][]any{{int64(-2147483648), int64(2147483648), nil}})
	execSQL(t, db, "CREATE TABLE dest(i INT,b BIGINT,v OID)")
	execSQL(t, db, "INSERT INTO dest SELECT v,v,i FROM one")
	wantTuples(t, db, "SELECT * FROM dest", [][]any{{int64(-1), int64(4294967295), int64(4294967295)}})
	wantTuples(t, db, "SELECT CASE WHEN TRUE THEN v ELSE i END,COALESCE(NULL::oid,v),NULLIF(v,i) FROM one", [][]any{{int64(4294967295), int64(4294967295), nil}})
	execSQL(t, db, "CREATE TABLE grouped(v OID)")
	execSQL(t, db, "INSERT INTO grouped VALUES('0'),('4294967295'),('4294967295'),(NULL),(NULL)")
	wantTuples(t, db, "SELECT v,count(*) FROM grouped GROUP BY v ORDER BY v", [][]any{{int64(0), int64(1)}, {int64(4294967295), int64(2)}, {nil, int64(2)}})
	wantTuples(t, db, "SELECT v IS NULL,v IS DISTINCT FROM NULL FROM grouped ORDER BY v", [][]any{{false, true}, {false, true}, {false, true}, {true, false}, {true, false}})
	for _, q := range []string{"SELECT (-1)::int8::oid FROM one", "SELECT v=(-1)::int8 FROM one", "SELECT (-1)::int8=v FROM one", "INSERT INTO grouped VALUES(4294967296::int8)"} {
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "OID out of range"})
	}
	for _, q := range []string{"SELECT v::smallint FROM one", "SELECT NULL::oid::smallint FROM one"} {
		_, err := db.Prepare(q)
		pos := utf8.RuneCountInString(q[:strings.LastIndex(q, "::")]) + 1
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42846", Message: "cannot cast type oid to smallint", Position: strconv.Itoa(pos)})
	}
	_, err := db.Query("SELECT '1'::oid")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"})
}

func TestOIDDiagnosticsAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE o(v OID)")
	execSQL(t, db, "INSERT INTO o VALUES('7')")
	execSQL(t, db, "CREATE TABLE empty(v OID)")
	_, eager := db.Prepare("SELECT 'x'::oid FROM empty")
	typeCoreDiagnostic(t, eager, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type oid: "x"`, Position: "8"})
	for _, tc := range []struct{ text, code string }{{"x", "22P02"}, {"0b10", "22P02"}, {"1_000", "22P02"}, {"4294967296x", "22P02"}, {"4294967296", "22003"}, {"-2147483649", "22003"}, {"18446744073709551616x", "22003"}} {
		message := `invalid input syntax for type oid: "` + tc.text + `"`
		if tc.code == "22003" {
			message = `value "` + tc.text + `" is out of range for type oid`
		}
		q := `/*é*/ INSERT INTO o VALUES('1'),('` + tc.text + `')`
		pos := utf8.RuneCountInString(q[:strings.LastIndex(q, "('")]) + 2
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message, Position: strconv.Itoa(pos)})
		_, err = db.Prepare(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message, Position: strconv.Itoa(pos)})
		stmt, err := db.Prepare("INSERT INTO o VALUES('1'),($1)")
		if err != nil {
			t.Fatal(err)
		}
		_, err = stmt.Exec(tc.text)
		stmt.Close()
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message,Context:"unnamed portal parameter $1 = '...'"})
		wantTuples(t, db, "SELECT v FROM o", [][]any{{int64(7)}})
	}
	_, castErr := db.Exec("INSERT INTO o VALUES('1'),((-1)::int8::oid)")
	typeCoreDiagnostic(t, castErr, stuntgres.Error{Code: "22003", Message: "OID out of range"})
	wantTuples(t, db, "SELECT v FROM o", [][]any{{int64(7)}})
	execSQL(t, db, "CREATE TABLE nn(v OID NOT NULL)")
	_, err := db.Exec("INSERT INTO nn VALUES('4294967295'),(NULL)")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"})
	wantTuples(t, db, "SELECT v FROM nn", [][]any{})
	execSQL(t, db, "CREATE TABLE detail(i INT NOT NULL,v OID)")
	_, err = db.Exec("INSERT INTO detail VALUES(1,'0'),(NULL,'4294967295')")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "i" of relation "detail" violates not-null constraint`, Detail: "Failing row contains (null, 4294967295).", Schema: "public", Table: "detail", Column: "i"})
	wantTuples(t, db, "SELECT * FROM detail", [][]any{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = db.PrepareContext(ctx, "SELECT v FROM o"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO o VALUES('0')"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	iteration, stop := context.WithCancel(context.Background())
	rows, err := db.QueryContext(iteration, "SELECT v FROM o")
	if err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	defer rows.Close()
	if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal("canceled OID iteration", rows.Err())
	}
}
