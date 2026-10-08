package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestNameSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("name-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	check := func(q string, want [][]any, args ...any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range types {
			if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != reflect.TypeFor[string]() {
				t.Fatal(typ)
			}
			if _, ok := typ.Length(); ok {
				t.Fatal("NAME length")
			}
			if _, _, ok := typ.DecimalSize(); ok {
				t.Fatal("NAME precision")
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			row := make([]any, len(types))
			dst := make([]any, len(types))
			for i := range row {
				dst[i] = &row[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, want)
		}
	}
	diagnostic := func(err error, want stuntgres.Error) {
		t.Helper()
		want.Severity = "ERROR"
		want.SeverityUnlocalized = "ERROR"
		var got *stuntgres.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v), want %#v", got, err, want)
		}
	}
	exec(`CREATE TABLE "MixedObject"(id INT,n NAME,t TEXT)`)
	exec("CREATE TABLE empty(n NAME)")
	exec(`INSERT INTO "MixedObject" VALUES(1,'MiXeD','text')`)
	stmt, err := db.PrepareContext(ctx, `INSERT INTO "MixedObject" VALUES($1,$2,$3)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, args := range [][]any{{2, strings.Repeat("a", 61) + "éZ", "unicode"}, {3, []byte(strings.Repeat("x", 64)), "bytes"}, {4, nil, nil}, {5, true, "bound"}} {
		if _, err = stmt.ExecContext(ctx, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO "MixedObject" VALUES(6,TRUE,'literal')`)
	want := [][]any{{"MiXeD"}, {strings.Repeat("a", 61) + "é"}, {strings.Repeat("x", 63)}, {nil}, {"true"}, {"t"}}
	check(`SELECT n FROM "MixedObject" ORDER BY id`, want)
	check("SELECT n FROM empty", [][]any{})
	var text string
	rows, err := db.Query(`SELECT t FROM "MixedObject" WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || types[0].DatabaseTypeName() != "TEXT" {
		t.Fatal(types, err)
	}
	if !rows.Next() {
		t.Fatal("missing text row")
	}
	if err = rows.Scan(&text); err != nil || text != "text" {
		t.Fatal(text, err)
	}
	rows.Close()
	exec("CREATE TABLE ordered(n NAME)")
	exec("INSERT INTO ordered VALUES(NULL),(''),('A'),('a'),('a '),('é')")
	check("SELECT n FROM ordered ORDER BY n", [][]any{{""}, {"A"}, {"a"}, {"a "}, {"é"}, {nil}})
	check("SELECT n FROM ordered ORDER BY n DESC", [][]any{{nil}, {"é"}, {"a "}, {"a"}, {"A"}, {""}})
	exec("CREATE TABLE longnames(n NAME,t TEXT)")
	long := strings.Repeat("x", 64)
	short := strings.Repeat("x", 63)
	exec("INSERT INTO longnames VALUES($1,$2)", long, long)
	check("SELECT n FROM longnames WHERE n<t", [][]any{{short}})
	check("SELECT n FROM longnames WHERE n=t", [][]any{})
	check("SELECT n FROM longnames WHERE n='"+long+"'", [][]any{{short}})
	check("SELECT n FROM longnames WHERE n=$1", [][]any{{short}}, long)
	_, err = db.Exec("INSERT INTO ordered VALUES('new'),($1)", strings.Repeat("x", 63)+"\xff")
	diagnostic(err, stuntgres.Error{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0xff`, Context: "unnamed portal parameter $1"})
	check("SELECT n FROM ordered ORDER BY n", [][]any{{""}, {"A"}, {"a"}, {"a "}, {"é"}, {nil}})
	_, err = db.Prepare("/*é*/ SELECT n FROM ordered WHERE n=TRUE")
	diagnostic(err, stuntgres.Error{Code: "42883", Message: "operator does not exist: name = boolean", Position: "36", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	utility, err := db.Prepare("/*é*/ CREATE TABLE bad(n name(3))")
	if err != nil {
		t.Fatal(err)
	}
	_, err = utility.Exec()
	utility.Close()
	diagnostic(err, stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "name"`, Position: "26"})
	raw, err := d.Open("name-smoke")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, `SELECT n FROM "MixedObject" ORDER BY id`, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	check(`SELECT n FROM "MixedObject" ORDER BY id`, want)
	check("SELECT n FROM empty", [][]any{})
	if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "NAME" {
		t.Fatal("old NAME description changed")
	}
	dst := make([]driver.Value, 1)
	for _, row := range want {
		if err = held.Next(dst); err != nil || dst[0] != row[0] {
			t.Fatal(dst, row, err)
		}
	}
	if err = held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(n NAME); INSERT INTO replacement VALUES('ok'); bad")); err == nil {
		t.Fatal("bad restore accepted")
	}
	check(`SELECT n FROM "MixedObject" ORDER BY id`, want)
}
