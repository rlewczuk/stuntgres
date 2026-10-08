package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestStringCoreLifecycle(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, `CREATE TABLE string_core_source(id INT,raw CHAR(8),form TEXT)`)
	execSQL(t, db, `INSERT INTO string_core_source VALUES(1,' é ','NFC'),(2,' x ','NFC'),(3,NULL,'NFC')`)
	execSQL(t, db, `CREATE TABLE string_core_dest(id INT,v TEXT DEFAULT normalize(trim(' é ')),CHECK (v IS NORMALIZED))`)
	execSQL(t, db, `INSERT INTO string_core_dest(id) VALUES(0)`)
	result, err := db.Exec(`INSERT INTO string_core_dest SELECT id,"normalize"(btrim(raw),form)||'!' FROM string_core_source`)
	if err != nil {
		t.Fatal(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	wantTuples(t, db, "SELECT id,v FROM string_core_dest ORDER BY id", [][]any{{int64(0), "é"}, {int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}})
	execSQL(t, db, `CREATE VIEW string_core_view AS SELECT id,normalize(btrim(raw))||'!' AS v FROM string_core_source`)
	for _, q := range []string{
		"SELECT id,v FROM string_core_view WHERE v IS NORMALIZED ORDER BY id",
		"SELECT s.id,d.v FROM string_core_source s JOIN string_core_dest d ON normalize(btrim(s.raw))||'!'=d.v ORDER BY s.id",
	} {
		wantTuples(t, db, q, [][]any{{int64(1), "é!"}, {int64(2), "x!"}})
	}
	wantTuples(t, db, "SELECT btrim(raw) AS v,count(*) AS n FROM string_core_source GROUP BY btrim(raw) HAVING btrim(raw) IS NORMALIZED ORDER BY v", [][]any{{"x", int64(1)}})
	rows, err := db.Query("UPDATE string_core_dest SET v=normalize(v||'a') WHERE id=2 RETURNING id,v")
	queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "x!a"}}, false)
	rows, err = db.Query("DELETE FROM string_core_dest WHERE btrim(v)='x!a' RETURNING id,v")
	queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "x!a"}}, false)
	// Runtime body failure on the second source row is statement-atomic.
	execSQL(t, db, "UPDATE string_core_source SET form='bad' WHERE id=2")
	_, err = db.Exec(`INSERT INTO string_core_dest SELECT id+10,"normalize"(raw,form) FROM string_core_source`)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "invalid normalization form: bad"})
	wantTuples(t, db, "SELECT id FROM string_core_dest ORDER BY id", [][]any{{int64(0)}, {int64(1)}, {int64(3)}})
	execSQL(t, db, "UPDATE string_core_source SET form='NFC' WHERE id=2")
	// A failed CHECK also leaves publication untouched.
	_, err = db.Exec("INSERT INTO string_core_dest VALUES(20,'é')")
	sqlState(t, err, "23514")
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,v FROM string_core_view ORDER BY id", nil)
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
	want := [][]any{{int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}}
	wantTuples(t, db, "SELECT id,v FROM string_core_view ORDER BY id", want)
	var after bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &after); err != nil || after.String() != dump.String() {
		t.Fatal("redump mismatch", err)
	}
	dst := make([]driver.Value, 2)
	for _, row := range want {
		if err = held.Next(dst); err != nil || !reflect.DeepEqual([]any{dst[0], dst[1]}, row) {
			t.Fatal(dst, row, err)
		}
	}
	if err = held.Next(dst); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	err = stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE staged(v TEXT); INSERT INTO staged VALUES("normalize"('x','bad'))`))
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "invalid normalization form: bad"})
	wantTuples(t, db, "SELECT id,v FROM string_core_view ORDER BY id", want)
	after.Reset()
	if err = stuntgres.Dump(ctx, db, &after); err != nil || after.String() != dump.String() {
		t.Fatal("failed restore changed publication", err)
	}
	execSQL(t, db, "INSERT INTO string_core_dest(id) VALUES(21)")
	wantTuples(t, db, "SELECT v FROM string_core_dest WHERE id=21", [][]any{{"é"}})
}

func TestStringCorePreparedOwnershipAndReanalysis(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE string_core_owned(id INT,v TEXT)")
	input := []byte(" é ")
	execSQL(t, db, "INSERT INTO string_core_owned VALUES(1,normalize(btrim($1)))", input)
	input[1] = 'X'
	s, err := db.Prepare("SELECT btrim(v)||'!' AS v FROM string_core_owned ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check := func() {
		t.Helper()
		rows, e := s.Query()
		queryCoreRows(t, rows, e, []string{"v"}, []string{"TEXT"}, [][]any{{"é!"}}, false)
	}
	check()
	execSQL(t, db, "ALTER TABLE string_core_owned ALTER COLUMN v TYPE VARCHAR(20)")
	check()
	execSQL(t, db, "ALTER TABLE string_core_owned ALTER COLUMN v TYPE CHAR(4)")
	check()
}

func TestStringCoreReviewRestore(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	script := `CREATE TABLE sc_restore(id INT,v TEXT,p BOOL);
INSERT INTO sc_restore VALUES
(1,btrim('é🙂X🙂é','é🙂'),is_normalized('é')),
(2,ltrim(' xy ',' '),is_normalized('é','NFD')),
(3,rtrim(' xy ',' '),is_normalized('é','NFD')),
(4,trim(leading 'x' from 'xxYxx'),is_normalized('é','NFC')),
(5,trim(trailing 'x' from 'xxYxx'),is_normalized('é','NFD')),
(6,trim(both 'xy' from 'yxTomxy'),is_normalized('é','NFKC')),
(7,normalize('é'),is_normalized(NULL)),
(8,normalize('é',NFD),is_normalized('é','NFD')),
(9,pg_catalog.normalize('ﬃ','nfKc'),is_normalized('ffi','NFKC')),
(10,"normalize"('é','nFkD'),is_normalized('é',NULL)),
(11,'['||normalize(btrim(' é '))||']',is_normalized(normalize('é')));`
	if err := stuntgres.Restore(ctx, db, strings.NewReader(script)); err != nil {
		t.Fatal("valid scalar-function Restore", err)
	}
	want := [][]any{
		{int64(1), "X", true}, {int64(2), "xy ", true}, {int64(3), " xy", true},
		{int64(4), "Yxx", true}, {int64(5), "xxY", false}, {int64(6), "Tom", false},
		{int64(7), "é", nil}, {int64(8), "é", true}, {int64(9), "ffi", true},
		{int64(10), "é", nil}, {int64(11), "[é]", true},
	}
	wantTuples(t, db, "SELECT id,v,p FROM sc_restore ORDER BY id", want)
	var before bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &before); err != nil {
		t.Fatal(err)
	}
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,v,p FROM sc_restore ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	err = stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE sc_staged(v TEXT); INSERT INTO sc_staged VALUES(pg_catalog.normalize('x','bad'));`))
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "invalid normalization form: bad"})
	wantTuples(t, db, "SELECT id,v,p FROM sc_restore ORDER BY id", want)
	wantTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='sc_staged'", [][]any{})
	var after bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &after); err != nil || after.String() != before.String() {
		t.Fatal("failed Restore changed publication", err)
	}
	values := make([]driver.Value, 3)
	for _, row := range want {
		if err := held.Next(values); err != nil || !reflect.DeepEqual([]any{values[0], values[1], values[2]}, row) {
			t.Fatal("failed Restore changed held cursor", values, row, err)
		}
	}
	if err := held.Next(values); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	for _, expression := range []string{`normalize($1)`, `btrim('x',$1)`, `'['||normalize($1)||']'`} {
		script := "CREATE TABLE sc_staged(v TEXT); INSERT INTO sc_staged VALUES(" + expression + ");"
		err := stuntgres.Restore(ctx, db, strings.NewReader(script))
		position := len([]rune(script[:strings.Index(script, "$1")])) + 1
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: fmt.Sprint(position)})
		wantTuples(t, db, "SELECT id,v,p FROM sc_restore ORDER BY id", want)
	}
}

func TestStringCoreReviewRestoreFailure(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE sc_restore_saved(v TEXT,p BOOL)")
	execSQL(t, db, "INSERT INTO sc_restore_saved VALUES('é!',true)")
	var before bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &before); err != nil {
		t.Fatal(err)
	}
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v,p FROM sc_restore_saved", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	err = stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE sc_bad(v TEXT); INSERT INTO sc_bad VALUES(pg_catalog.normalize('x','bad'))`))
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "invalid normalization form: bad"})
	wantTuples(t, db, "SELECT v,p FROM sc_restore_saved", [][]any{{"é!", true}})
	wantTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='sc_bad'", [][]any{})
	var after bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &after); err != nil || after.String() != before.String() {
		t.Fatal("failed Restore changed old publication", err)
	}
	values := make([]driver.Value, 2)
	if err := held.Next(values); err != nil || !reflect.DeepEqual(values, []driver.Value{"é!", true}) {
		t.Fatal("failed Restore changed old cursor", values, err)
	}
	if err := held.Next(values); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestStringCoreReviewRestoreExcludedProducers(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE sc_restore_saved(v TEXT)")
	execSQL(t, db, "INSERT INTO sc_restore_saved VALUES('sentinel')")
	for _, expression := range []string{"random()", "random_normal()", "setseed(0)", "num_nulls('x')", "abs(-1)"} {
		t.Run(expression, func(t *testing.T) {
			script := "CREATE TABLE sc_unselected(v TEXT); INSERT INTO sc_unselected VALUES(btrim(" + expression + "));"
			err := stuntgres.Restore(ctx, db, strings.NewReader(script))
			if err == nil || err.Error() != "stuntgres: invalid restore scalar" {
				t.Fatal("Restore admitted or changed the unselected FunctionExpr boundary", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary Restore traversal error became SQLSTATE", err)
			}
			wantTuples(t, db, "SELECT v FROM sc_restore_saved", [][]any{{"sentinel"}})
			wantTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='sc_unselected'", [][]any{})
		})
	}
}
