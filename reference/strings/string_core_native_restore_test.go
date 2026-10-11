package reference_test

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCoreReviewRestore(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	db := p.Native
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
	fixture.StringCoreControlTuples(t, db, "SELECT id,v,p FROM sc_restore ORDER BY id", want)
	var before bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &before); err != nil {
		t.Fatal(err)
	}
	raw, err := p.NativeConnector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,v,p FROM sc_restore ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	sourceRaw, err := p.NativeConnector.Driver().Open("reference/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer sourceRaw.Close()
	sourceHeld, err := sourceRaw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,v,p FROM sc_restore ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceHeld.Close()
	err = stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE sc_staged(v TEXT); INSERT INTO sc_staged VALUES(pg_catalog.normalize('x','bad'));`))
	fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: bad"})
	fixture.StringCoreControlTuples(t, db, "SELECT id,v,p FROM sc_restore ORDER BY id", want)
	fixture.StringCoreControlTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='sc_staged'", [][]any{})
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
	for _, row := range want {
		if err := sourceHeld.Next(values); err != nil || !reflect.DeepEqual([]any{values[0], values[1], values[2]}, row) {
			t.Fatal("Driver.Open retained cursor", values, row, err)
		}
	}
	if err := sourceHeld.Next(values); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	for _, expression := range []string{`normalize($1)`, `btrim('x',$1)`, `'['||normalize($1)||']'`} {
		script := "CREATE TABLE sc_staged(v TEXT); INSERT INTO sc_staged VALUES(" + expression + ");"
		err := stuntgres.Restore(ctx, db, strings.NewReader(script))
		position := len([]rune(script[:strings.Index(script, "$1")])) + 1
		fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "42P02", Message: "parameters are not allowed in restore", Position: fmt.Sprint(position)})
		fixture.StringCoreControlTuples(t, db, "SELECT id,v,p FROM sc_restore ORDER BY id", want)
	}
	stringCoreReviewRestoreSQLReference(t, p)
}

func TestReferenceStringCoreReviewRestoreFailure(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	db := p.Native
	for _, backend := range p.Backends() {
		if backend.PG {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE sc_restore_saved(v TEXT,p BOOL)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO sc_restore_saved VALUES('é!',true)", 1))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v,p FROM sc_restore_saved", []string{"v", "p"}, []string{"TEXT", "BOOL"}, [][]any{{"é!", true}}))
		}
	}
	fixture.StringCoreControlExec(t, db, "CREATE TABLE sc_restore_saved(v TEXT,p BOOL)")
	fixture.StringCoreControlExec(t, db, "INSERT INTO sc_restore_saved VALUES('é!',true)")
	var before bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &before); err != nil {
		t.Fatal(err)
	}
	raw, err := p.NativeConnector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v,p FROM sc_restore_saved", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	sourceRaw, err := p.NativeConnector.Driver().Open("reference/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer sourceRaw.Close()
	sourceHeld, err := sourceRaw.(driver.QueryerContext).QueryContext(ctx, "SELECT v,p FROM sc_restore_saved", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceHeld.Close()
	err = stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE sc_bad(v TEXT); INSERT INTO sc_bad VALUES(pg_catalog.normalize('x','bad'))`))
	fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: bad"})
	fixture.StringCoreControlTuples(t, db, "SELECT v,p FROM sc_restore_saved", [][]any{{"é!", true}})
	fixture.StringCoreControlTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='sc_bad'", [][]any{})
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
	if err := sourceHeld.Next(values); err != nil || !reflect.DeepEqual(values, []driver.Value{"é!", true}) {
		t.Fatal("Driver.Open retained cursor", values, err)
	}
	if err := sourceHeld.Next(values); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestReferenceStringCoreReviewRestoreExcludedProducers(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	db := p.Native
	for _, backend := range p.Backends() {
		if backend.PG {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE sc_restore_saved(v TEXT)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO sc_restore_saved VALUES('sentinel')", 1))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM sc_restore_saved", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
		}
	}
	fixture.StringCoreControlExec(t, db, "CREATE TABLE sc_restore_saved(v TEXT)")
	fixture.StringCoreControlExec(t, db, "INSERT INTO sc_restore_saved VALUES('sentinel')")
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
			fixture.StringCoreControlTuples(t, db, "SELECT v FROM sc_restore_saved", [][]any{{"sentinel"}})
			fixture.StringCoreControlTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='sc_unselected'", [][]any{})
		})
	}
}
