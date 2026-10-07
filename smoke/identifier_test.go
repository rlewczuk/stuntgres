package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
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
		return fmt.Errorf("affected=%d want %d", got, want)
	}
	if _, err := result.LastInsertId(); err == nil {
		return fmt.Errorf("LastInsertId succeeded")
	}
	return nil
}
func identifierCheckRows(rows *sql.Rows, err error, columns, types []string, want [][]any) error {
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
		return fmt.Errorf("diagnostic=%#v (%v) want %#v", got, err, want)
	}
	return nil
}
func identifierRun() error {
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
	if err := identifierCheckRows(rows, e, columns, types, want); err != nil {
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
	if err := identifierCheckRows(rows, e, columns, types, want); err != nil {
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
	if err := identifierCheckRows(rows, e, []string{"by"}, []string{"INT4"}, [][]any{{int64(7)}}); err != nil {
		return err
	}
	rows, e = db.Query(`SELECT "join","authorization","a""b" FROM "select" ORDER BY "join"`)
	if err := identifierCheckRows(rows, e, []string{"join", "authorization", "a\"b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "kept", true}}); err != nil {
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
			{"INSERT INTO t (select) VALUES (1)", "VALUES", "24"},
		} {
			if err := identifierDiagnostic(operation(s.q), "42601", fmt.Sprintf("syntax error at or near \"%s\"", s.word), s.pos); err != nil {
				return err
			}
		}
		for _, s := range []struct{ label, code, msg, pos string }{
			{"check", "0A000", "projection aliases are unsupported", "11"}, {"authorization", "0A000", "projection aliases are unsupported", "11"}, {"char", "42601", "syntax error at or near \"char\"", "11"}, {"day", "42601", "syntax error at or near \"day\"", "11"},
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
	if err := identifierCheckRows(old, nil, columns, types, want); err != nil {
		return err
	}
	rows, e = db.Query(query)
	if err := identifierCheckRows(rows, e, columns, types, want); err != nil {
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
func TestIdentifierSmoke(t *testing.T) {
	if err := identifierQualifiedNames(); err != nil {
		t.Fatal(err)
	}
	if err := identifierContextualCapabilities(); err != nil {
		t.Fatal(err)
	}
	if err := identifierRun(); err != nil {
		t.Fatal(err)
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
		if err := identifierCheckRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}); err != nil {
			return err
		}
		stmt, err = db.Prepare(q)
		if err != nil {
			return err
		}
		rows, err = stmt.Query(7 + i)
		if err := identifierCheckRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}); err != nil {
			stmt.Close()
			return err
		}
		stmt.Close()
		rows, err = db.Query("SELECT x FROM \"" + name + "\" ORDER BY x")
		if err := identifierCheckRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}); err != nil {
			return err
		}
	}
	return nil
}

func identifierContextualCapabilities() error {
	d := stuntgres.NewDriver()
	c, err := d.OpenConnector("identifier-contexts")
	if err != nil {
		return err
	}
	db := sql.OpenDB(c)
	defer db.Close()
	if err := db.Ping(); err != nil {
		return err
	}
	for _, q := range []string{"CREATE TABLE t (x INT)", "CREATE TABLE source_rows (x INT)", "INSERT INTO source_rows VALUES (7)"} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	for _, s := range []struct {
		q     string
		value bool
	}{
		{"SELECT x isnull FROM source_rows", false},
		{"SELECT x notnull FROM source_rows", true},
	} {
		rows, err := db.Query(s.q)
		if err := identifierCheckRows(rows, err, []string{"?column?"}, []string{"BOOL"}, [][]any{{s.value}}); err != nil {
			return err
		}
		stmt, err := db.Prepare(s.q)
		if err != nil {
			return err
		}
		rows, err = stmt.Query()
		err = identifierCheckRows(rows, err, []string{"?column?"}, []string{"BOOL"}, [][]any{{s.value}})
		stmt.Close()
		if err != nil {
			return err
		}
	}
	for _, s := range []struct{ q, code, msg, pos string }{
		{"SELECT x default FROM t", "0A000", "projection aliases are unsupported", "10"},
		{"SELECT between(x) FROM t", "42601", "syntax error at or near \"(\"", "15"},
		{"SELECT f(x) FROM t", "0A000", "functions are unsupported", "8"},
		{"SELECT authorization(x) FROM t", "0A000", "functions are unsupported", "8"},
		{"SELECT x, abs(x) FROM t", "0A000", "functions are unsupported", "11"},
		{"SELECT x, authorization(x) FROM t", "0A000", "functions are unsupported", "11"},
		{"SELECT x, \"between\"(x) FROM t", "0A000", "functions are unsupported", "11"},
		{"SELECT x, between(x) FROM t", "42601", "syntax error at or near \"(\"", "18"},
		{"SELECT x, x, abs(x) FROM t", "0A000", "functions are unsupported", "14"},
		{"SELECT x FROM t ORDER BY x NULLS FIRST", "0A000", "NULLS ordering is unsupported", "28"},
		{"SELECT x FROM t ORDER BY x NULLS bogus", "42601", "syntax error at or near \"NULLS\"", "28"},
	} {
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
			if err := identifierDiagnostic(op(s.q), s.code, s.msg, s.pos); err != nil {
				return err
			}
		}
	}
	for _, q := range []string{"INSERT INTO t (SELECT)", "INSERT INTO t SELECT x FROM source_rows", "INSERT INTO t (x) SELECT x FROM source_rows"} {
		result, err := db.Exec(q)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return fmt.Errorf("%s count=%d: %v", q, count, err)
		}
		stmt, err := db.Prepare(q)
		if err != nil {
			return err
		}
		result, err = stmt.Exec()
		stmt.Close()
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return fmt.Errorf("%s prepared count=%d: %v", q, count, err)
		}
	}
	rows, err := db.Query("SELECT x FROM t ORDER BY x")
	if err := identifierCheckRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {nil}, {nil}}); err != nil {
		return err
	}
	rows, err = db.Query("SELECT x FROM source_rows ORDER BY x")
	return identifierCheckRows(rows, err, []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}})
}
