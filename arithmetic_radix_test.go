package stuntgres_test

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestArithmeticPromotedRadixLiteralCompatibility(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE n(v NUMERIC)", 0)
	arithmeticExec(t, db, "INSERT INTO n VALUES(+0x8000000000000000)", 1)
	stmt, err := db.Prepare("INSERT INTO n VALUES(+0x8000000000000000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.Exec(); err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	arithmeticRows(t, db, "SELECT v FROM n ORDER BY v", []string{"NUMERIC"}, [][]any{{"9223372036854775808"}, {"9223372036854775808"}})
	if err := stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE n(v NUMERIC); INSERT INTO n VALUES(+0x8000000000000000)")); err != nil {
		t.Fatal(err)
	}
	arithmeticRows(t, db, "SELECT v FROM n", []string{"NUMERIC"}, [][]any{{"9223372036854775808"}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "'9223372036854775808'") || strings.Contains(dump.String(), "0x8000000000000000") {
		t.Fatal("promoted radix was not canonically dumped", dump.String())
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	arithmeticRows(t, db, "SELECT v FROM n", []string{"NUMERIC"}, [][]any{{"9223372036854775808"}})
	var redump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("canonical promoted-radix redump changed", redump.String(), err)
	}
	arithmeticExec(t, db, "CREATE TABLE d(f BOOL)", 0)
	q := "INSERT INTO d VALUES(+0x8000000000000000)"
	want := stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type numeric`, Hint: "You will need to rewrite or cast the expression.", Position: "22"}
	_, err = db.Prepare(q)
	arithmeticError(t, err, want)
	_, err = db.Exec(q)
	arithmeticError(t, err, want)
	script := "CREATE TABLE d(f BOOL); INSERT INTO d VALUES(+0x8000000000000000)"
	err = stuntgres.Restore(context.Background(), db, strings.NewReader(script))
	want.Position = strconv.Itoa(strings.Index(script, "+") + 1)
	arithmeticError(t, err, want)
	arithmeticRows(t, db, "SELECT v FROM n", []string{"NUMERIC"}, [][]any{{"9223372036854775808"}})
	_, err = db.Prepare("SELECT +1e131072 FROM n")
	arithmeticError(t, err, stuntgres.Error{Code: "22003", Message: "value overflows numeric format", Position: "9"})
	rows, err := db.Query("SELECT +1e131072 FROM n")
	if rows != nil {
		rows.Close()
		t.Fatal("numeric input failure exposed rows")
	}
	arithmeticError(t, err, stuntgres.Error{Code: "22003", Message: "value overflows numeric format", Position: "9"})
}
