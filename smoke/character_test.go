package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestCharacterSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	check := func(q string, want [][]any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		got := [][]any{}
		for rows.Next() {
			row := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range dest {
				dest[i] = &row[i]
			}
			if err = rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got %#v want %#v", q, got, want)
		}
	}
	diagnostic := func(err error, message string) {
		t.Helper()
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22001", Message: message}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("diagnostic=%#v err=%v want=%#v", got, err, want)
		}
	}
	exec("CREATE TABLE c(id INT,c CHAR(3),v VARCHAR(2),t TEXT,u BPCHAR)")
	exec("INSERT INTO c VALUES(1,'é🙂','é🙂','a  ','a  '),(2,'a','a ','a ',NULL)")
	s, err := db.PrepareContext(ctx, "INSERT INTO c VALUES($1,$2,$3,$4,$5)")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.ExecContext(ctx, 3, []byte("x"), "x", "tail  ", "tail  "); err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(1), "é🙂 ", "é🙂", "a  ", "a  "}, {int64(2), "a  ", "a ", "a ", nil}, {int64(3), "x  ", "x", "tail  ", "tail  "}}
	check("SELECT * FROM c ORDER BY id", want)
	rows, err := db.QueryContext(ctx, "SELECT c,v,t,u FROM c")
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR"} {
		if types[i].DatabaseTypeName() != name || types[i].ScanType() != reflect.TypeFor[string]() {
			t.Fatal(types[i])
		}
	}
	for i, n := range []int64{3, 2} {
		got, ok := types[i].Length()
		if !ok || got != n {
			t.Fatal(got, ok, n)
		}
	}
	exec("CREATE TABLE sized(c CHAR(5),v VARCHAR(5))")
	exec("INSERT INTO sized VALUES('good      ','good      ')")
	check("SELECT * FROM sized", [][]any{{"good ", "good "}})
	_, err = db.ExecContext(ctx, "INSERT INTO sized(v) VALUES($1)", "good \t")
	diagnostic(err, "value too long for type character varying(5)")
	check("SELECT CAST('é🙂x' AS varchar(2)), 'ok'::char(3), CAST(NULL AS char(2)) FROM c WHERE id=1", [][]any{{"é🙂", "ok ", nil}})
	castQuery := "SELECT CAST(c AS varchar(2)),CAST(v AS char(3)),CAST(t AS varchar(2)),c::char(4)::text FROM c WHERE id=2"
	for _, prepared := range []bool{false, true} {
		var castRows *sql.Rows
		if prepared {
			stmt, e := db.PrepareContext(ctx, castQuery)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			castRows, err = stmt.QueryContext(ctx)
		} else {
			castRows, err = db.QueryContext(ctx, castQuery)
		}
		if err != nil {
			t.Fatal(err)
		}
		labels, e := castRows.Columns()
		if e != nil || !reflect.DeepEqual(labels, []string{"c", "v", "t", "c"}) {
			castRows.Close()
			t.Fatal("column-derived cast labels", prepared, labels, e)
		}
		castTypes, e := castRows.ColumnTypes()
		if e != nil || len(castTypes) != 4 {
			castRows.Close()
			t.Fatal(castTypes, e)
		}
		for i, name := range []string{"VARCHAR", "BPCHAR", "VARCHAR", "TEXT"} {
			length, known := castTypes[i].Length()
			wantLength := []int64{2, 3, 2, math.MaxInt64}[i]
			if castTypes[i].Name() != labels[i] || castTypes[i].DatabaseTypeName() != name || castTypes[i].ScanType() != reflect.TypeFor[string]() || !known || length != wantLength {
				castRows.Close()
				t.Fatal("cast metadata", prepared, i, castTypes[i], length, known)
			}
		}
		if !castRows.Next() {
			castRows.Close()
			t.Fatal("missing cast row", prepared, castRows.Err())
		}
		var a, b, c, d string
		if e = castRows.Scan(&a, &b, &c, &d); e != nil || !reflect.DeepEqual([]string{a, b, c, d}, []string{"a", "a  ", "a ", "a"}) {
			castRows.Close()
			t.Fatal("cast values", prepared, a, b, c, d, e)
		}
		if castRows.Next() || castRows.Err() != nil {
			castRows.Close()
			t.Fatal("unexpected cast row/error", prepared, castRows.Err())
		}
		castRows.Close()
	}
	parameterQuery := "SELECT CAST($1 AS bpchar),CAST($1 AS text),CAST($1 AS bpchar)='a',CAST($1 AS text)='a ' FROM c WHERE id=2"
	for _, prepared := range []bool{false, true} {
		var stmt *sql.Stmt
		if prepared {
			stmt, err = db.PrepareContext(ctx, parameterQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
		}
		for _, frame := range []struct {
			input any
			want  []any
		}{
			{"a  ", []any{"a  ", "a", true, false}},
			{nil, []any{nil, nil, nil, nil}},
			{"z ", []any{"z ", "z", false, false}},
		} {
			var parameterRows *sql.Rows
			if prepared {
				parameterRows, err = stmt.QueryContext(ctx, frame.input)
			} else {
				parameterRows, err = db.QueryContext(ctx, parameterQuery, frame.input)
			}
			if err != nil {
				t.Fatal("fixed parameter cast", prepared, frame.input, err)
			}
			labels, e := parameterRows.Columns()
			if e != nil || !reflect.DeepEqual(labels, []string{"bpchar", "text", "?column?", "?column?"}) {
				parameterRows.Close()
				t.Fatal(labels, e)
			}
			if !parameterRows.Next() {
				parameterRows.Close()
				t.Fatal("missing parameter row", parameterRows.Err())
			}
			var a, b, c, d any
			if e = parameterRows.Scan(&a, &b, &c, &d); e != nil || !reflect.DeepEqual([]any{a, b, c, d}, frame.want) {
				parameterRows.Close()
				t.Fatal("parameter frame", prepared, frame.input, []any{a, b, c, d}, frame.want, e)
			}
			if parameterRows.Next() || parameterRows.Err() != nil {
				parameterRows.Close()
				t.Fatal("unexpected parameter row/error", parameterRows.Err())
			}
			parameterRows.Close()
		}
	}
	for _, prepared := range []bool{false, true} {
		q := "SELECT v AND TRUE FROM c"
		var diagnosticError error
		if prepared {
			stmt, e := db.PrepareContext(ctx, q)
			diagnosticError = e
			if stmt != nil {
				stmt.Close()
				t.Fatal("Boolean failure exposed handle")
			}
		} else {
			failedRows, e := db.QueryContext(ctx, q)
			diagnosticError = e
			if failedRows != nil {
				failedRows.Close()
				t.Fatal("Boolean failure exposed rows")
			}
		}
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "argument of AND must be type boolean, not type character varying", Position: "8"}
		if !errors.As(diagnosticError, &got) || *got != want {
			t.Fatal("canonical character diagnostic", prepared, got, diagnosticError, want)
		}
	}
	exec("CREATE TABLE dst(i INT)")
	exec("INSERT INTO dst VALUES(9)")
	exec("CREATE TABLE src(c CHAR(2),v VARCHAR(2))")
	exec("INSERT INTO src VALUES('a','a')")
	for _, tc := range []struct {
		cast, diagnostic, column string
	}{
		{"bpchar(2)", "character", "c"},
		{"char(2)", "character", "c"},
		{"varchar(2)", "character varying", "v"},
	} {
		for _, sourceQuery := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				q := "/*é🙂*/ INSERT INTO dst VALUES(CAST('a' AS " + tc.cast + "))"
				if sourceQuery {
					q = "/*é🙂*/ INSERT INTO dst SELECT " + tc.column + " FROM src"
				}
				var diagnosticError error
				if prepared {
					stmt, e := db.PrepareContext(ctx, q)
					diagnosticError = e
					if stmt != nil {
						stmt.Close()
						t.Fatal("incompatible character assignment exposed handle")
					}
				} else {
					result, e := db.ExecContext(ctx, q)
					diagnosticError = e
					if result != nil {
						t.Fatal("incompatible character assignment exposed result")
					}
				}
				var got *stuntgres.Error
				want := stuntgres.Error{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804",
					Message: `column "i" is of type integer but expression is of type ` + tc.diagnostic,
					Hint:    "You will need to rewrite or cast the expression.", Position: "31",
				}
				if !errors.As(diagnosticError, &got) || *got != want {
					t.Fatal("resolved INSERT source diagnostic", q, prepared, got, diagnosticError, want)
				}
				check("SELECT i FROM dst", [][]any{{int64(9)}})
			}
		}
	}
	exec("DROP TABLE src")
	exec("DROP TABLE dst")
	rows, err = db.QueryContext(ctx, "SELECT CAST(NULL AS char(2)) FROM c WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	types, err = rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	n, known := types[0].Length()
	if types[0].DatabaseTypeName() != "BPCHAR" || !known || n != 2 {
		t.Fatal(types[0], n, known)
	}
	check("SELECT id FROM c WHERE c=t", [][]any{})
	check("SELECT id FROM c WHERE t=c", [][]any{})
	check("SELECT id FROM c WHERE c='a    '", [][]any{{int64(2)}})
	for _, tc := range []struct {
		first, second string
		values        [][]any
	}{
		{"CAST('b  ' AS text)", "CAST('a ' AS varchar)", [][]any{{"a "}, {"b  "}}},
		{"CAST('a ' AS varchar)", "CAST('b  ' AS text)", [][]any{{"a "}, {"b  "}}},
		{"CAST('b  ' AS text)", "CAST('a ' AS char(2))", [][]any{{"a"}, {"b  "}}},
		{"CAST('a ' AS char(2))", "CAST('b  ' AS text)", [][]any{{"a"}, {"b"}}},
	} {
		for _, prepared := range []bool{false, true} {
			exec("CREATE TABLE source_values(v TEXT)")
			q := "INSERT INTO source_values VALUES(" + tc.first + "),(" + tc.second + ") ORDER BY 1"
			var result sql.Result
			if prepared {
				stmt, e := db.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				result, err = stmt.ExecContext(ctx)
				stmt.Close()
			} else {
				result, err = db.ExecContext(ctx, q)
			}
			if err != nil {
				t.Fatal("character source VALUES", prepared, q, err)
			}
			count, e := result.RowsAffected()
			if e != nil || count != 2 {
				t.Fatal("source affected count", count, e)
			}
			check("SELECT v FROM source_values ORDER BY v", tc.values)
			metadataRows, e := db.QueryContext(ctx, "SELECT v FROM source_values")
			if e != nil {
				t.Fatal(e)
			}
			metadata, e := metadataRows.ColumnTypes()
			metadataRows.Close()
			if e != nil || len(metadata) != 1 {
				t.Fatal(metadata, e)
			}
			length, known := metadata[0].Length()
			if metadata[0].DatabaseTypeName() != "TEXT" || metadata[0].ScanType() != reflect.TypeFor[string]() || !known || length != math.MaxInt64 {
				t.Fatal("source destination metadata", metadata[0], length, known)
			}
			exec("DROP TABLE source_values")
		}
	}
	// Unlimited BPCHAR readback exposes pads that TEXT assignment would hide.
	for _, tc := range []struct {
		first, second string
		values        [][]any
	}{
		{"CAST('b' AS char(2))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b "}}},
		{"CAST('a' AS varchar(2))", "CAST('b' AS char(2))", [][]any{{"a"}, {"b"}}},
	} {
		for _, prepared := range []bool{false, true} {
			exec("CREATE TABLE source_typmod(v BPCHAR)")
			q := "INSERT INTO source_typmod VALUES(" + tc.first + "),(" + tc.second + ") ORDER BY 1"
			var result sql.Result
			if prepared {
				stmt, e := db.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				result, err = stmt.ExecContext(ctx)
				stmt.Close()
			} else {
				result, err = db.ExecContext(ctx, q)
			}
			if err != nil {
				t.Fatal("cross-identity source typmod", prepared, q, err)
			}
			count, e := result.RowsAffected()
			if e != nil || count != 2 {
				t.Fatal("cross-identity source affected count", count, e)
			}
			check("SELECT v FROM source_typmod ORDER BY v", tc.values)
			metadataRows, e := db.QueryContext(ctx, "SELECT v FROM source_typmod")
			if e != nil {
				t.Fatal(e)
			}
			metadata, e := metadataRows.ColumnTypes()
			metadataRows.Close()
			if e != nil || len(metadata) != 1 {
				t.Fatal(metadata, e)
			}
			length, known := metadata[0].Length()
			if metadata[0].DatabaseTypeName() != "BPCHAR" || metadata[0].ScanType() != reflect.TypeFor[string]() || !known || length != math.MaxInt64 {
				t.Fatal("cross-identity source destination metadata", metadata[0], length, known)
			}
			exec("DROP TABLE source_typmod")
		}
	}
	exec("CREATE TABLE source_atomic(v VARCHAR(2))")
	exec("INSERT INTO source_atomic VALUES('s')")
	for _, prepared := range []bool{false, true} {
		q := "INSERT INTO source_atomic VALUES(CAST('aa' AS text)),(CAST('zzx' AS varchar)) ORDER BY 1"
		var result sql.Result
		if prepared {
			stmt, e := db.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal("source assignment rejected at Prepare", e)
			}
			result, err = stmt.ExecContext(ctx)
			stmt.Close()
		} else {
			result, err = db.ExecContext(ctx, q)
		}
		if result != nil {
			t.Fatal("failed source insert exposed result")
		}
		diagnostic(err, "value too long for type character varying(2)")
		check("SELECT v FROM source_atomic", [][]any{{"s"}})
	}
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
	check("SELECT * FROM c ORDER BY id", want)
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE bad(v VARCHAR(2)); INSERT INTO bad VALUES('ok'),('bad')"))
	diagnostic(err, "value too long for type character varying(2)")
	check("SELECT * FROM c ORDER BY id", want)
}
