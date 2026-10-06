package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func identifierAffected(result sql.Result, err error, want int64) error {
	if err != nil {
		return err
	}
	got, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("identifierAffected=%d want %d", got, want)
	}
	if _, err := result.LastInsertId(); err == nil {
		return fmt.Errorf("LastInsertId succeeded")
	}
	return nil
}
func identifierRows(rows *sql.Rows, err error, columns, types []string, want [][]any) error {
	if err != nil {
		return err
	}
	defer rows.Close()
	gotColumns, err := rows.Columns()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(gotColumns, columns) {
		return fmt.Errorf("columns=%v want %v", gotColumns, columns)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	for i, c := range metadata {
		if c.DatabaseTypeName() != types[i] {
			return fmt.Errorf("type %d=%s", i, c.DatabaseTypeName())
		}
	}
	got := [][]any{}
	for rows.Next() {
		row := make([]any, len(columns))
		dest := make([]any, len(row))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("rows=%#v want %#v", got, want)
	}
	return rows.Close()
}
func identifierDiagnostic(err error, code, message, position string) error {
	var got *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	if !errors.As(err, &got) || *got != want {
		return fmt.Errorf("identifierDiagnostic=%#v (%v) want %#v", got, err, want)
	}
	return nil
}
func identifierContract() error {
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("identifier-smoke")
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	if err := db.Ping(); err != nil {
		return err
	}
	exec := func(q string, want int64, args ...any) error {
		r, e := db.Exec(q, args...)
		return identifierAffected(r, e, want)
	}
	if err := exec("CREATE TABLE abort (by INT, between TEXT, values BOOL, integer BIGINT, char TEXT)", 0); err != nil {
		return err
	}
	if err := exec("INSERT INTO public.abort (by,between,values,integer,char) VALUES (1,'one',TRUE,10,'a'),(2,'two',FALSE,20,'b')", 2); err != nil {
		return err
	}
	columns := []string{"by", "between", "values", "integer", "char"}
	types := []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}
	want := [][]any{{int64(2), "two", false, int64(20), "b"}}
	query := "SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC"
	rows, e := db.Query(query)
	if err := identifierRows(rows, e, columns, types, want); err != nil {
		return err
	}
	stmt, err := db.Prepare("INSERT INTO abort (by,between,values,integer,char) VALUES ($1,$2,$3,$4,$5)")
	if err != nil {
		return err
	}
	r, e := stmt.Exec(3, "three", true, 30, "c")
	stmt.Close()
	if err := identifierAffected(r, e, 1); err != nil {
		return err
	}
	stmt, err = db.Prepare("SELECT by,between,values,integer,char FROM abort WHERE by=$1 ORDER BY by DESC")
	if err != nil {
		return err
	}
	rows, e = stmt.Query(2)
	if err := identifierRows(rows, e, columns, types, want); err != nil {
		stmt.Close()
		return err
	}
	stmt.Close()
	if err := exec(query, 1); err != nil {
		return err
	}
	stmt, err = db.Prepare("CREATE TABLE insert (by INT)")
	if err != nil {
		return err
	}
	r, e = stmt.Exec()
	stmt.Close()
	if err := identifierAffected(r, e, 0); err != nil {
		return err
	}
	for _, q := range []string{"INSERT INTO insert (by) VALUES (7)", `CREATE TABLE "select" ("join" INT, "authorization" TEXT, "a""b" BOOL)`, `INSERT INTO "select" VALUES (7,'kept',TRUE)`} {
		count := int64(0)
		if q[0] == 'I' {
			count = 1
		}
		if err := exec(q, count); err != nil {
			return err
		}
	}
	rows, e = db.Query("SELECT by FROM insert ORDER BY by")
	if err := identifierRows(rows, e, []string{"by"}, []string{"INT4"}, [][]any{{int64(7)}}); err != nil {
		return err
	}
	rows, e = db.Query(`SELECT "join","authorization","a""b" FROM "select" ORDER BY "join"`)
	if err := identifierRows(rows, e, []string{"join", "authorization", "a\"b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "kept", true}}); err != nil {
		return err
	}
	for _, operation := range []func(string) error{
		func(q string) error { _, e := db.Exec(q); return e },
		func(q string) error {
			rows, e := db.Query(q)
			if rows != nil {
				rows.Close()
			}
			return e
		},
		func(q string) error {
			stmt, e := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
			}
			return e
		},
	} {
		for _, s := range []struct{ q, word, pos string }{
			{"CREATE TABLE select (x INT)", "select", "14"}, {"CREATE TABLE SELECT (x INT)", "SELECT", "14"}, {"/*é*/ CREATE TABLE SELECT (x INT)", "SELECT", "20"}, {"CREATE TABLE t (authorization INT)", "authorization", "17"}, {"SELECT select FROM abort", "select", "8"},
		} {
			if err := identifierDiagnostic(operation(s.q), "42601", fmt.Sprintf("syntax error at or near \"%s\"", s.word), s.pos); err != nil {
				return err
			}
		}
		for _, s := range []struct{ label, code, msg, pos string }{
			{"check", "0A000", "projection aliases are unsupported", "11"}, {"authorization", "0A000", "projection aliases are unsupported", "11"}, {"AS char", "0A000", "projection aliases are unsupported", "14"}, {"char", "42601", "syntax error at or near \"char\"", "11"}, {"day", "42601", "syntax error at or near \"day\"", "11"},
		} {
			if err := identifierDiagnostic(operation("SELECT by "+s.label+" FROM insert"), s.code, s.msg, s.pos); err != nil {
				return err
			}
		}
	}
	ctx := context.Background()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		return err
	}
	old, err := db.Query(query)
	if err != nil {
		return err
	}
	defer old.Close()
	err = stuntgres.Restore(ctx, db, bytes.NewBufferString("CREATE TABLE staged (x INT); CREATE TABLE select (x INT)"))
	if err := identifierDiagnostic(err, "42601", "syntax error at or near \"select\"", "43"); err != nil {
		return err
	}
	var unchanged bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &unchanged); err != nil {
		return err
	}
	if !bytes.Equal(dump.Bytes(), unchanged.Bytes()) {
		return fmt.Errorf("failed restore changed catalog")
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		return err
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		return err
	}
	if err := identifierRows(old, nil, columns, types, want); err != nil {
		return err
	}
	rows, e = db.Query(query)
	if err := identifierRows(rows, e, columns, types, want); err != nil {
		return err
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil {
		return err
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		return fmt.Errorf("redump differs")
	}
	return nil
}
func TestIdentifierKeywords(t *testing.T) {
	if err := identifierContract(); err != nil {
		t.Fatal(err)
	}
}

var identifierSyntaxCases = []struct{ q, word, pos string }{
	{"CREATE TABLE select (x INT)", "select", "14"}, {"CREATE TABLE t (select INT)", "select", "17"}, {"CREATE TABLE join (x INT)", "join", "14"}, {"CREATE TABLE t (authorization INT)", "authorization", "17"},
	{"INSERT INTO select VALUES (1)", "select", "13"}, {"INSERT INTO t (select) VALUES (1)", "VALUES", "24"}, {"SELECT select FROM t", "select", "8"}, {"SELECT x FROM t WHERE select = 1", "select", "23"}, {"SELECT x FROM t ORDER BY select", "select", "26"},
	{"INSERT INTO t ((SELECT)) VALUES (1)", "VALUES", "26"}, {"INSERT INTO t (select) values (1)", "values", "24"}, {"INSERT INTO t (authorization) VALUES (1)", "authorization", "16"},
	{"CREATE TABLE end (x INT)", "end", "14"}, {"CREATE TABLE analyze (x INT)", "analyze", "14"}, {"CREATE TABLE do (x INT)", "do", "14"}, {"CREATE TABLE grant (x INT)", "grant", "14"}, {"CREATE TABLE SELECT (x INT)", "SELECT", "14"}, {"/*é*/ CREATE TABLE SELECT (x INT)", "SELECT", "20"}, {"CREATE TABLE select.t (x INT)", "select", "14"},
}

func TestIdentifierDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "identifier-diagnostics")
	for _, s := range identifierSyntaxCases {
		for _, operation := range []func(string) error{
			func(q string) error { _, e := db.Exec(q); return e },
			func(q string) error {
				rows, e := db.Query(q)
				if rows != nil {
					rows.Close()
				}
				return e
			},
			func(q string) error {
				stmt, e := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
				}
				return e
			},
		} {
			if err := identifierDiagnostic(operation(s.q), "42601", fmt.Sprintf("syntax error at or near \"%s\"", s.word), s.pos); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestIdentifierLabels(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "identifier-labels")
	execSQL(t, db, "CREATE TABLE t (x INT)")
	execSQL(t, db, "INSERT INTO t VALUES (7)")
	for _, s := range []struct{ label, code, msg, pos string }{
		{"check", "0A000", "projection aliases are unsupported", "10"}, {"authorization", "0A000", "projection aliases are unsupported", "10"},
		{"AS char", "0A000", "projection aliases are unsupported", "13"}, {"AS grant", "0A000", "projection aliases are unsupported", "13"}, {"AS \"Mixed\"", "0A000", "projection aliases are unsupported", "13"},
		{"char", "42601", "syntax error at or near \"char\"", "10"}, {"day", "42601", "syntax error at or near \"day\"", "10"},
	} {
		_, err := db.Exec("SELECT x " + s.label + " FROM t")
		if err := identifierDiagnostic(err, s.code, s.msg, s.pos); err != nil {
			t.Fatal(err)
		}
	}
}
func TestIdentifierControls(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "identifier-controls")
	execSQL(t, db, "CREATE TABLE sentinel (x INT)")
	execSQL(t, db, "INSERT INTO sentinel VALUES (7)")
	for _, q := range []string{"CREATE TABLE staged (x INT); CREATE TABLE select (x INT)", "CREATE TABLE staged (x INT); SELECT select FROM sentinel"} {
		if _, err := db.Exec(q); err == nil {
			t.Fatal("malformed whole script succeeded")
		}
	}
	var before, after bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
		t.Fatal(err)
	}
	err := stuntgres.Restore(context.Background(), db, bytes.NewBufferString("CREATE TABLE staged (x INT); CREATE TABLE select (x INT)"))
	if err := identifierDiagnostic(err, "42601", "syntax error at or near \"select\"", "43"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(context.Background(), db, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("failed restore changed catalog")
	}
}

var identifierNormalizationCases = []struct{ spelling, name string }{
	{"MiXeD", "mixed"}, {"ÉCOLE", "École"}, {"_select$1", "_select$1"}, {"select1", "select1"},
	{`"Odd"`, "Odd"}, {`"a""b"`, `a"b`}, {`"a.b"`, "a.b"},
	{strings.Repeat("A", 63), strings.Repeat("a", 63)},
	{strings.Repeat("B", 64), strings.Repeat("b", 63)},
	{strings.Repeat("C", 62) + "éZ", strings.Repeat("c", 62)},
	{`"` + strings.Repeat("D", 62) + `éZ"`, strings.Repeat("D", 62)},
}

func TestIdentifierNormalization(t *testing.T) {
	for _, s := range identifierNormalizationCases {
		db := openLocal(t, stuntgres.NewDriver(), s.name)
		execSQL(t, db, "CREATE TABLE "+s.spelling+" ("+s.spelling+" INT)")
		execSQL(t, db, "INSERT INTO "+s.spelling+" ("+s.spelling+") VALUES (7)")
		rows, err := db.Query("SELECT " + s.spelling + " FROM " + s.spelling + " ORDER BY " + s.spelling)
		if err := identifierRows(rows, err, []string{s.name}, []string{"INT4"}, [][]any{{int64(7)}}); err != nil {
			t.Fatal(err)
		}
	}
}

func identifierQualifiedNames() error {
	d := stuntgres.NewDriver()
	c, err := d.OpenConnector("identifier-qualified")
	if err != nil {
		return err
	}
	db := sql.OpenDB(c)
	defer db.Close()
	if err := db.Ping(); err != nil {
		return err
	}
	for i, name := range []string{"select", "authorization", "from", "check"} {
		q := "CREATE TABLE public." + name + " (x INT)"
		var r sql.Result
		if i%2 == 0 {
			r, err = db.Exec(q)
		} else {
			var stmt *sql.Stmt
			stmt, err = db.Prepare(q)
			if err != nil {
				return err
			}
			r, err = stmt.Exec()
			stmt.Close()
		}
		if err := identifierAffected(r, err, 0); err != nil {
			return err
		}
		stmt, err := db.Prepare("INSERT INTO public." + name + " (x) VALUES ($1)")
		if err != nil {
			return err
		}
		r, err = stmt.Exec(7 + i)
		stmt.Close()
		if err := identifierAffected(r, err, 1); err != nil {
			return err
		}
		q = "SELECT x FROM public." + name + " WHERE x=$1 ORDER BY x"
		rows, err := db.Query(q, 7+i)
		if err := identifierRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}); err != nil {
			return err
		}
		stmt, err = db.Prepare(q)
		if err != nil {
			return err
		}
		rows, err = stmt.Query(7 + i)
		if err := identifierRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}); err != nil {
			stmt.Close()
			return err
		}
		stmt.Close()
		rows, err = db.Query("SELECT x FROM \"" + name + "\" ORDER BY x")
		if err := identifierRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}); err != nil {
			return err
		}
	}
	return nil
}
func TestIdentifierQualifiedNames(t *testing.T) {
	if err := identifierQualifiedNames(); err != nil {
		t.Fatal(err)
	}
}
func TestIdentifierInsertQueries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "identifier-insert-query")
	execSQL(t, db, "CREATE TABLE t (x INT)")
	execSQL(t, db, "CREATE TABLE source_rows (x INT)")
	execSQL(t, db, "INSERT INTO source_rows VALUES (7)")
	for _, s := range []struct{ q, pos string }{{"INSERT INTO t (SELECT)", "16"}, {"INSERT INTO t SELECT x FROM source_rows", "15"}, {"INSERT INTO t ((SELECT x FROM source_rows WHERE x=7 ORDER BY x))", "17"}, {"INSERT INTO t (x) SELECT x FROM source_rows", "19"}} {
		for _, prepare := range []bool{false, true} {
			var err error
			if prepare {
				var stmt *sql.Stmt
				stmt, err = db.Prepare(s.q)
				if stmt != nil {
					stmt.Close()
				}
			} else {
				_, err = db.Exec(s.q)
			}
			if err := identifierDiagnostic(err, "0A000", "INSERT SELECT is unsupported", s.pos); err != nil {
				t.Fatal(err)
			}
		}
	}
	rows, err := db.Query("SELECT x FROM t ORDER BY x")
	if err := identifierRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{}); err != nil {
		t.Fatal(err)
	}
}

// PostgreSQL outcomes are independently specified; native capability errors
// deliberately differ for legal calls, expressions, ordering and aliases.
var identifierContextCases = []struct {
	q, code, message, position, column, typ string
	row                                     any
}{
	{"SELECT x default FROM t", "0A000", "projection aliases are unsupported", "10", "default", "INT4", int64(7)},
	{"SELECT between(x) FROM t", "42601", "syntax error at or near \"(\"", "15", "", "", nil},
	{"SELECT integer(x) FROM t", "42601", "syntax error at or near \"(\"", "15", "", "", nil},
	{"SELECT abs(x) FROM t", "0A000", "functions are unsupported", "8", "abs", "INT4", int64(7)},
	{"SELECT int4(x) FROM t", "0A000", "functions are unsupported", "8", "int4", "INT4", int64(7)},
	{"SELECT text(x) FROM t", "0A000", "functions are unsupported", "8", "text", "TEXT", "7"},
	{"SELECT authorization(x) FROM t", "0A000", "functions are unsupported", "8", "authorization", "INT4", int64(7)},
	{"SELECT coalesce(x,0) FROM t", "0A000", "functions are unsupported", "8", "coalesce", "INT4", int64(7)},
	{"SELECT nullif(x,7) FROM t", "0A000", "functions are unsupported", "8", "nullif", "INT4", nil},
	{"SELECT greatest(x,0) FROM t", "0A000", "functions are unsupported", "8", "greatest", "INT4", int64(7)},
	{"SELECT least(x,9) FROM t", "0A000", "functions are unsupported", "8", "least", "INT4", int64(7)},
	{"SELECT f(x) FROM t", "0A000", "functions are unsupported", "8", "f", "INT4", int64(7)},
	{"SELECT abort(x) FROM t", "0A000", "functions are unsupported", "8", "abort", "INT4", int64(7)},
	{"SELECT \"between\"(x) FROM t", "0A000", "functions are unsupported", "8", "between", "INT4", int64(7)},
	{"SELECT CAST(x AS INT) FROM t", "0A000", "casts are unsupported", "8", "x", "INT4", int64(7)},
	{"INSERT INTO t VALUES (between(7))", "42601", "syntax error at or near \"(\"", "30", "", "", nil},
	{"SELECT x FROM t WHERE x=between(7)", "42601", "syntax error at or near \"(\"", "32", "", "", nil},
	{"SELECT x \"isnull\" FROM t", "0A000", "projection aliases are unsupported", "10", "isnull", "INT4", int64(7)},
	{"SELECT x \"notnull\" FROM t", "0A000", "projection aliases are unsupported", "10", "notnull", "INT4", int64(7)},
	{"SELECT x FROM t ORDER BY x NULLS FIRST", "0A000", "NULLS ordering is unsupported", "28", "x", "INT4", int64(7)},
	{"SELECT x FROM t ORDER BY x DESC NULLS LAST", "0A000", "NULLS ordering is unsupported", "33", "x", "INT4", int64(7)},
	{"SELECT x FROM t ORDER BY x NULLS bogus", "42601", "syntax error at or near \"NULLS\"", "28", "", "", nil},
	{"SELECT x FROM t ORDER BY x NULLS \"FIRST\"", "42601", "syntax error at or near \"NULLS\"", "28", "", "", nil},
	{"SELECT x FROM t ORDER BY x NULLS", "42601", "syntax error at or near \"NULLS\"", "28", "", "", nil},
	{"SELECT x FROM t ORDER BY x nulls bogus", "42601", "syntax error at or near \"nulls\"", "28", "", "", nil},
	{"SELECT x FROM t ORDER BY x NULLS /*é*/ FIRST", "0A000", "NULLS ordering is unsupported", "28", "x", "INT4", int64(7)},
	{"SELECT x FROM t ORDER BY x NULLS 1", "42601", "syntax error at or near \"NULLS\"", "28", "", "", nil},
	{"/*é*/ SELECT x FROM t ORDER BY x NULLS bogus", "42601", "syntax error at or near \"NULLS\"", "34", "", "", nil},
	{"SELECT x FROM t ORDER BY x NULLS /* never closed", "42601", "unterminated /* comment at or near \"/* never closed\"", "34", "", "", nil},
}

func TestIdentifierContextualCapabilities(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "identifier-context-capabilities")
	execSQL(t, db, "CREATE TABLE t (x INT)")
	execSQL(t, db, "INSERT INTO t VALUES (7)")
	for _, s := range []struct {
		q     string
		value bool
	}{
		{"SELECT x isnull FROM t", false},
		{"SELECT x notnull FROM t", true},
	} {
		result, err := db.Exec(s.q)
		if err := identifierAffected(result, err, 1); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(s.q)
		if err := identifierRows(rows, err, []string{"?column?"}, []string{"BOOL"}, [][]any{{s.value}}); err != nil {
			t.Fatal(err)
		}
		stmt, err := db.Prepare(s.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query()
		if err := identifierRows(rows, err, []string{"?column?"}, []string{"BOOL"}, [][]any{{s.value}}); err != nil {
			stmt.Close()
			t.Fatal(err)
		}
		stmt.Close()
	}
	for _, s := range identifierContextCases {
		for _, op := range []func(string) error{
			func(q string) error { _, err := db.Exec(q); return err },
			func(q string) error {
				rows, err := db.Query(q)
				if rows != nil {
					rows.Close()
				}
				return err
			},
			func(q string) error {
				stmt, err := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
				}
				return err
			},
		} {
			if err := identifierDiagnostic(op(s.q), s.code, s.message, s.position); err != nil {
				t.Fatal(err)
			}
		}
	}
}

var identifierLaterCallCases = []struct {
	q, code, message, position string
	columns, types             []string
	rows                       [][]any
}{
	{"SELECT x, abs(x) FROM t", "0A000", "functions are unsupported", "11", []string{"x", "abs"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"/*é*/ SELECT x, abs(x) FROM t", "0A000", "functions are unsupported", "17", []string{"x", "abs"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, abort(x) FROM t", "0A000", "functions are unsupported", "11", []string{"x", "abort"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, authorization(x) FROM t", "0A000", "functions are unsupported", "11", []string{"x", "authorization"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, \"between\"(x) FROM t", "0A000", "functions are unsupported", "11", []string{"x", "between"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, coalesce(x,0) FROM t", "0A000", "functions are unsupported", "11", []string{"x", "coalesce"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, between(x) FROM t", "42601", "syntax error at or near \"(\"", "18", nil, nil, nil},
	{"SELECT x, CAST(x AS INT) FROM t", "0A000", "casts are unsupported", "11", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, x, abs(x) FROM t", "0A000", "functions are unsupported", "14", []string{"x", "x", "abs"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(7), int64(7)}}},
	{"SELECT x, x, between(x) FROM t", "42601", "syntax error at or near \"(\"", "21", nil, nil, nil},
}

func TestIdentifierLaterProjectionCalls(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "identifier-later-projection-calls")
	execSQL(t, db, "CREATE TABLE t (x INT)")
	execSQL(t, db, "INSERT INTO t VALUES (7)")
	var before, after bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
		t.Fatal(err)
	}
	for _, s := range identifierLaterCallCases {
		for _, op := range []func(string) error{
			func(q string) error { _, err := db.Exec(q); return err },
			func(q string) error {
				rows, err := db.Query(q)
				if rows != nil {
					rows.Close()
				}
				return err
			},
			func(q string) error {
				stmt, err := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
				}
				return err
			},
		} {
			if err := identifierDiagnostic(op(s.q), s.code, s.message, s.position); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, prefixErr := db.Exec("CREATE TABLE staged (x INT); SELECT x, abs(x) FROM t")
	if err := identifierDiagnostic(prefixErr, "0A000", "functions are unsupported", "40"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(context.Background(), db, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("later projection rejection published schema or data changes")
	}
	rows, err := db.Query("SELECT x FROM t ORDER BY x")
	if err := identifierRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}); err != nil {
		t.Fatal(err)
	}
}
