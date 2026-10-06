package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"sync"
	"testing"
)

func castExplicitSetup(t *testing.T) (*stuntgres.Driver, *sql.DB) {
	t.Helper()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT4,i INT4,n INT8,s TEXT,b BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2,2,'  +42 ',TRUE),(2,0,0,'no',FALSE),(3,NULL,NULL,NULL,NULL)")
	return d, db
}
func castExplicitSyntax(source, target string, syntax int) string {
	switch syntax {
	case 0:
		return "CAST(" + source + " AS " + target + ")"
	case 1:
		return source + "::" + target
	default:
		return target + "(" + source + ")"
	}
}
func castExplicitMetadata(t *testing.T, rows *sql.Rows, names, types []string) {
	t.Helper()
	got, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(got, names) {
		t.Fatal(got, names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != len(types) {
		t.Fatal(meta)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if typ == "TEXT" {
			scan = reflect.TypeFor[string]()
		} else if typ == "BOOL" {
			scan = reflect.TypeFor[bool]()
		}
		if meta[i].DatabaseTypeName() != typ || meta[i].ScanType() != scan {
			t.Fatal(meta[i], typ, scan)
		}
	}
}
func TestCastExplicitMatrix(t *testing.T) {
	_, db := castExplicitSetup(t)
	execSQL(t, db, "CREATE TABLE v(id INT,s TEXT)")
	execSQL(t, db, "INSERT INTO v VALUES(1,'  +42 '),(2,'-2'),(3,NULL)")
	execSQL(t, db, "CREATE TABLE vb(id INT,s TEXT)")
	execSQL(t, db, "INSERT INTO vb VALUES(1,' ye '),(2,'of'),(3,NULL)")
	targets := []string{"int4", "int8", "text", "bool"}
	values := [][][]any{{{int64(2), int64(0), nil}, {int64(2), int64(0), nil}, {"2", "0", nil}, {true, false, nil}}, {{int64(2), int64(0), nil}, {int64(2), int64(0), nil}, {"2", "0", nil}, nil}, {{int64(42), int64(-2), nil}, {int64(42), int64(-2), nil}, {"  +42 ", "no", nil}, {true, false, nil}}, {{int64(1), int64(0), nil}, nil, {"true", "false", nil}, {true, false, nil}}}
	for si, source := range []string{"i", "n", "s", "b"} {
		for ti, target := range targets {
			for syntax := range 3 {
				t.Run(fmt.Sprintf("%s-%s-%d", source, target, syntax), func(t *testing.T) {
					expr := castExplicitSyntax(source, target, syntax)
					q := "SELECT " + expr + " FROM t ORDER BY id"
					if values[si][ti] == nil {
						want := stuntgres.Error{Code: "42846", Message: "cannot cast type bigint to boolean", Position: "8"}
						if si == 3 {
							want.Message = "cannot cast type boolean to bigint"
						}
						if syntax == 1 {
							want.Position = "9"
						}
						if syntax == 2 {
							typ := "bigint"
							if si == 3 {
								typ = "boolean"
							}
							want.Code = "42883"
							want.Message = "function " + target + "(" + typ + ") does not exist"
							want.Position = "8"
							want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						}
						rows, err := db.Query(q)
						if rows != nil {
							rows.Close()
							t.Fatal("failed cast exposed cursor")
						}
						typeCoreDiagnostic(t, err, want)
						stmt, err := db.Prepare(q)
						if stmt != nil {
							stmt.Close()
							t.Fatal("failed cast prepared")
						}
						typeCoreDiagnostic(t, err, want)
						return
					}
					if si == 2 && ti != 2 {
						table := "v"
						if ti == 3 {
							table = "vb"
						}
						q = "SELECT " + expr + " FROM " + table + " ORDER BY id"
					}
					want := [][]any{}
					for _, v := range values[si][ti] {
						want = append(want, []any{v})
					}
					wantTuples(t, db, q, want)
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					rows, err := stmt.Query()
					if err != nil {
						t.Fatal(err)
					}
					name := source
					if syntax == 2 {
						name = target
					}
					castExplicitMetadata(t, rows, []string{name}, []string{strings.ToUpper(target)})
					rows.Close()
				})
			}
		}
	}
}
func TestCastExplicitSyntaxAndMetadata(t *testing.T) {
	_, db := castExplicitSetup(t)
	for _, target := range []string{"int", "integer", "int4", "bigint", "int8", "bool", "boolean", "text", "\"int4\"", "\"int8\"", "\"bool\"", "\"text\""} {
		for syntax := range 2 {
			t.Run(target+strconv.Itoa(syntax), func(t *testing.T) {
				expr := castExplicitSyntax("NULL", target, syntax)
				wantTuples(t, db, "SELECT "+expr+" FROM t ORDER BY id", [][]any{{nil}, {nil}, {nil}})
			})
		}
	}
	for _, target := range []string{"int4", "int8", "text", "bool"} {
		wantTuples(t, db, "SELECT \""+target+"\"(NULL) FROM t ORDER BY id", [][]any{{nil}, {nil}, {nil}})
	}
	q := "SELECT CAST(i AS bigint),i::int4,CAST(1 AS integer),1::bigint,1::boolean::text,int8(i),text(i),NULL::bool,int4(i),CAST(int8(i) AS text) FROM t ORDER BY id"
	names := []string{"i", "i", "int4", "int8", "text", "int8", "text", "bool", "int4", "int8"}
	types := []string{"INT8", "INT4", "INT4", "INT8", "TEXT", "INT8", "TEXT", "BOOL", "INT4", "TEXT"}
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	castExplicitMetadata(t, rows, names, types)
	rows.Close()
	wantTuples(t, db, q, [][]any{{int64(2), int64(2), int64(1), int64(1), "true", int64(2), "2", nil, int64(2), "2"}, {int64(0), int64(0), int64(1), int64(1), "true", int64(0), "0", nil, int64(0), "0"}, {nil, nil, int64(1), int64(1), "true", nil, nil, nil, nil, nil}})
	rows, err = db.Query(strings.Replace(q, " ORDER BY id", " WHERE FALSE", 1))
	if err != nil {
		t.Fatal(err)
	}
	castExplicitMetadata(t, rows, names, types)
	if rows.Next() {
		t.Fatal("empty filter returned row")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	wantTuples(t, db, "SELECT '  +42 '::int4,CAST(' ye ' AS bool),int8('9223372036854775807'),'-9223372036854775808'::int8,'-2147483648'::int4,'2147483647'::int4,(-2)::bool FROM t WHERE id=1", [][]any{{int64(42), true, int64(9223372036854775807), int64(-9223372036854775808), int64(-2147483648), int64(2147483647), true}})
}
func TestCastExplicitConsumersAndAssignment(t *testing.T) {
	_, db := castExplicitSetup(t)
	for _, predicate := range []string{"i=CAST('2' AS int4)", "i='2'::int4", "i=int4('2')", "CAST(i AS bool)", "i::bool", "bool(i)"} {
		wantTuples(t, db, "SELECT id FROM t WHERE "+predicate+" ORDER BY id", [][]any{{int64(1)}})
	}
	execSQL(t, db, "CREATE TABLE a(i INT4,b BOOL,s TEXT,n INT8)")
	for _, q := range []string{"INSERT INTO a VALUES(TRUE::int4,2::bool,TRUE::text,'42'::int8)", "INSERT INTO a VALUES(CAST(FALSE AS int4),CAST(0 AS bool),CAST(FALSE AS text),CAST('43' AS int8))", "INSERT INTO a VALUES(int4(TRUE),bool(-2),text(TRUE),int8('44'))", "INSERT INTO a VALUES(NULL::int4,NULL::bool,NULL::text,NULL::int8)"} {
		r := execSQL(t, db, q)
		n, err := r.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	wantTuples(t, db, "SELECT i,b,s,n FROM a ORDER BY n", [][]any{{int64(1), true, "true", int64(42)}, {int64(0), false, "false", int64(43)}, {int64(1), true, "true", int64(44)}, {nil, nil, nil, nil}})
	for _, tc := range []struct {
		q, message string
		position   int
	}{{"INSERT INTO a(i) VALUES(TRUE)", `column "i" is of type integer but expression is of type boolean`, 25}, {"INSERT INTO a(b) VALUES(1)", `column "b" is of type boolean but expression is of type integer`, 25}, {"INSERT INTO a(i) VALUES('2'::text)", `column "i" is of type integer but expression is of type text`, 25}} {
		_, err := db.Exec(tc.q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42804", Message: tc.message, Position: strconv.Itoa(tc.position), Hint: "You will need to rewrite or cast the expression."})
	}
	execSQL(t, db, "INSERT INTO a(i) VALUES('2')")
}
func TestCastExplicitDiagnosticsAndPhases(t *testing.T) {
	_, db := castExplicitSetup(t)
	cases := []struct {
		q        string
		want     stuntgres.Error
		analysis bool
	}{
		{"SELECT CAST(n AS bool) FROM t", stuntgres.Error{Code: "42846", Message: "cannot cast type bigint to boolean", Position: "8"}, true},
		{"SELECT n::bool FROM t", stuntgres.Error{Code: "42846", Message: "cannot cast type bigint to boolean", Position: "9"}, true},
		{"SELECT CAST(b AS bigint) FROM t", stuntgres.Error{Code: "42846", Message: "cannot cast type boolean to bigint", Position: "8"}, true},
		{"SELECT CAST('oops' AS integer) FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "13"}, true},
		{"SELECT int4('oops') FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "13"}, true},
		{"SELECT /*é*/ 'oops'::int4 FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "14"}, true},
		{"SELECT CAST('truth' AS boolean) FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "13"}, true},
		{"SELECT '2147483648'::int4 FROM t", stuntgres.Error{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "8"}, true},
		{"SELECT CAST('9223372036854775808' AS bigint) FROM t", stuntgres.Error{Code: "22003", Message: `value "9223372036854775808" is out of range for type bigint`, Position: "13"}, true},
		{"SELECT 2147483648::int4 FROM t", stuntgres.Error{Code: "22003", Message: "integer out of range"}, false},
		{"SELECT (-2147483649)::int4 FROM t", stuntgres.Error{Code: "22003", Message: "integer out of range"}, false},
		{"SELECT CAST('oops'::text AS int4) FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false},
		{"SELECT s::int4 FROM t ORDER BY id", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "no"`}, false},
		{"SELECT s::bool FROM t ORDER BY id", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "  +42 "`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			rows, err := db.Query(tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("failure exposed partial cursor")
			}
			typeCoreDiagnostic(t, err, tc.want)
			stmt, err := db.Prepare(tc.q)
			if tc.analysis {
				if stmt != nil {
					stmt.Close()
					t.Fatal("analysis returned statement")
				}
				typeCoreDiagnostic(t, err, tc.want)
			} else {
				if err != nil {
					t.Fatal("expected successful Prepare for execution-owned conversion", err)
				}
				defer stmt.Close()
				rows, err = stmt.Query()
				if rows != nil {
					rows.Close()
					t.Fatal("execute exposed partial cursor")
				}
				typeCoreDiagnostic(t, err, tc.want)
			}
		})
	}
	execSQL(t, db, "CREATE TABLE empty(n INT8,b BOOL)")
	for _, q := range []string{"SELECT n::bool FROM empty", "SELECT NULL::int8::bool FROM empty"} {
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("unsupported null/empty cast accepted")
		}
		var d *stuntgres.Error
		if !errors.As(err, &d) || d.Code != "42846" {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name     string
		position int
	}{{"integer", 15}, {"bigint", 14}, {"boolean", 15}, {"int", 11}} {
		_, err := db.Query("SELECT " + tc.name + "('42') FROM t")
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `syntax error at or near "("`, Position: strconv.Itoa(tc.position)})
	}
	_, err := db.Query(`SELECT "integer"('42') FROM t`)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "function integer(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
	for _, q := range []string{"SELECT 1::int2 FROM t", "SELECT CAST(i AS numeric) FROM t", "SELECT CAST(NULL AS uuid) FROM t"} {
		_, err := db.Query(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: map[bool]string{true: "9", false: "8"}[strings.Contains(q, "::")]})
	}
}
func TestCastExplicitPreparedFrames(t *testing.T) {
	_, db := castExplicitSetup(t)
	for _, tc := range []struct {
		q            string
		names, types []string
		arg          any
		row          []any
	}{{"SELECT $1::int4,$1::text FROM t ORDER BY id", []string{"int4", "text"}, []string{"INT4", "TEXT"}, 2, []any{int64(2), "2"}}, {"SELECT $1::int4,$1::int8 FROM t ORDER BY id", []string{"int4", "int8"}, []string{"INT4", "INT8"}, 2, []any{int64(2), int64(2)}}, {"SELECT text($1) FROM t ORDER BY id", []string{"text"}, []string{"TEXT"}, "42", []any{"42"}}, {"SELECT int4($1::text) FROM t ORDER BY id", []string{"int4"}, []string{"INT4"}, "42", []any{int64(42)}}} {
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range []any{tc.arg, nil, tc.arg} {
			rows, err := stmt.Query(arg)
			if err != nil {
				t.Fatal(err)
			}
			castExplicitMetadata(t, rows, tc.names, tc.types)
			count := 0
			for rows.Next() {
				got := make([]any, len(tc.row))
				dst := make([]any, len(got))
				for i := range got {
					dst[i] = &got[i]
				}
				if err := rows.Scan(dst...); err != nil {
					t.Fatal(err)
				}
				want := tc.row
				if arg == nil {
					want = make([]any, len(tc.row))
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal(got, want)
				}
				count++
			}
			if rows.Err() != nil || count != 3 {
				t.Fatal(count, rows.Err())
			}
			rows.Close()
		}
		stmt.Close()
	}
	stmt, err := db.Prepare("SELECT CAST($1 AS bool) FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			arg := []any{true, false, nil}[i%3]
			rows, err := stmt.Query(arg)
			if err != nil {
				t.Error(err)
				return
			}
			defer rows.Close()
			n := 0
			for rows.Next() {
				var got any
				if err := rows.Scan(&got); err != nil {
					t.Error(err)
				}
				if !reflect.DeepEqual(got, arg) {
					t.Error(got, arg)
				}
				n++
			}
			if n != 3 || rows.Err() != nil {
				t.Error(n, rows.Err())
			}
		})
	}
	wg.Wait()
	for _, name := range []string{"int4", "int8", "bool"} {
		q := "SELECT " + name + "($1) FROM t"
		want := stuntgres.Error{Code: "42725", Message: "function " + name + "(unknown) is not unique", Position: "8", Hint: "Could not choose a best candidate function. You might need to add explicit type casts."}
		_, err := db.Query(q, "42")
		typeCoreDiagnostic(t, err, want)
		s, err := db.Prepare(q)
		if s != nil {
			s.Close()
			t.Fatal("ambiguous function prepared")
		}
		typeCoreDiagnostic(t, err, want)
	}
	_, err = db.Query("SELECT $1,$1::int4 FROM t", 2)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"})
	for _, q := range []string{"SELECT $1::int4 FROM t", "SELECT int4($1::text) FROM t"} {
		s, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := s.Query("oops")
		if rows != nil {
			rows.Close()
			t.Fatal("bad input exposed cursor")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
		s.Close()
	}
}
func TestCastExplicitAtomicityAndControls(t *testing.T) {
	d, db := castExplicitSetup(t)
	ctx := context.Background()
	execSQL(t, db, "CREATE TABLE atom(i INT4)")
	execSQL(t, db, "INSERT INTO atom VALUES(7)")
	for _, tc := range []struct {
		q    string
		args []any
		want stuntgres.Error
	}{{"INSERT INTO atom VALUES(CAST('8' AS int4)),(CAST('oops' AS int4))", nil, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "50"}}, {"INSERT INTO atom VALUES(8),(2147483648::int4)", nil, stuntgres.Error{Code: "22003", Message: "integer out of range"}}, {"INSERT INTO atom VALUES(int4($1::text)),(int4($2::text))", []any{"8", "oops"}, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}}} {
		_, err := db.Exec(tc.q, tc.args...)
		typeCoreDiagnostic(t, err, tc.want)
		wantTuples(t, db, "SELECT i FROM atom", [][]any{{int64(7)}})
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.QueryContext(canceled, "SELECT i::bool FROM t"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(canceled, "INSERT INTO atom VALUES('8'::int4)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(canceled, db, strings.NewReader("CREATE TABLE x(i INT)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT i FROM atom", [][]any{{int64(7)}})
	execSQL(t, db, "CREATE TABLE saved(id INT,i INT4,n INT8,s TEXT,b BOOL)")
	execSQL(t, db, "INSERT INTO saved VALUES(1,TRUE::int4,'9223372036854775807'::int8,FALSE::text,(-2)::bool),(2,'-2147483648'::int4,'-9223372036854775808'::int8,42::text,0::bool),(3,NULL::int4,NULL::int8,NULL::text,NULL::bool)")
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT i::int8,text(b),n::text FROM saved ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
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
	wantTuples(t, db, "SELECT i,n,s,b FROM saved ORDER BY id", [][]any{{int64(1), int64(9223372036854775807), "false", true}, {int64(-2147483648), int64(-9223372036854775808), "42", false}, {nil, nil, nil, nil}})
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("nondeterministic dump", err)
	}
	for _, script := range []string{"CREATE TABLE replacement(i INT4); INSERT INTO replacement VALUES(CAST($1::text AS int4))", "CREATE TABLE replacement(i INT4); INSERT INTO replacement VALUES(8::int4),('oops'::int4)", "CREATE TABLE replacement(i INT4); INSERT INTO replacement VALUES(8),(2147483648::int4)"} {
		err := stuntgres.Restore(ctx, db, strings.NewReader(script))
		if strings.Contains(script, "$1") {
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: strconv.Itoa(strings.Index(script, "$1") + 1)})
		} else if err == nil {
			t.Fatal("bad staged restore accepted")
		}
		wantTuples(t, db, "SELECT i FROM atom", [][]any{{int64(7)}})
		var after bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &after); err != nil || !bytes.Equal(after.Bytes(), dump.Bytes()) {
			t.Fatal("failed restore changed catalog/data", err)
		}
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(i INT4,b BOOL,s TEXT,n INT8); INSERT INTO replacement VALUES(CAST(TRUE AS int4),bool(-2),text(FALSE),'42'::int8)")); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT i,b,s,n FROM replacement", [][]any{{int64(1), true, "false", int64(42)}})
	if !reflect.DeepEqual(cursor.Columns(), []string{"i", "text", "n"}) {
		t.Fatal(cursor.Columns())
	}
	meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
	for i, typ := range []string{"INT8", "TEXT", "TEXT"} {
		if meta.ColumnTypeDatabaseTypeName(i) != typ {
			t.Fatal("retained metadata changed")
		}
	}
	dst := make([]driver.Value, 3)
	for _, want := range [][]driver.Value{{int64(1), "true", "9223372036854775807"}, {int64(-2147483648), "false", "-9223372036854775808"}, {nil, nil, nil}} {
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, want, err)
		}
	}
	if err := cursor.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
}

func TestCastExplicitBoundariesAndCursorCancellation(t *testing.T) {
	d, db := castExplicitSetup(t)
	for _, q := range []string{
		"SELECT 1::int4",
		"SELECT i::int4 AS renamed FROM t",
		"SELECT CAST(i AS int4) trailing FROM t",
		"SELECT i:: FROM t",
		"SELECT CAST(i AS) FROM t",
		"INSERT INTO t(i) VALUES(CAST(i AS int4))",
		"INSERT INTO t(i) VALUES(int4(i))",
	} {
		if strings.HasPrefix(q, "SELECT") {
			rows, err := db.Query(q)
			if rows != nil {
				rows.Close()
			}
			if err == nil {
				t.Fatal("excluded or malformed syntax accepted", q)
			}
		} else if _, err := db.Exec(q); err == nil {
			t.Fatal("nested forbidden column accepted", q)
		}
	}
	execSQL(t, db, "CREATE TABLE adjacent(i INT2)")
	_, err := db.Query("SELECT CAST(i AS int4) FROM adjacent")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: "8"})
	ctx, cancel := context.WithCancel(context.Background())
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT i::bool FROM t ORDER BY id", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer cursor.Close()
	cancel()
	if err := cursor.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal("cast cursor cancellation lost ordinary context contract", err)
	}
	stmt, err := db.Prepare("SELECT $1::int4 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if rows, err := stmt.Query(); err == nil {
		rows.Close()
		t.Fatal("missing actual argument accepted")
	} else {
		var d *stuntgres.Error
		if errors.As(err, &d) {
			t.Fatal("adapter count error acquired SQLSTATE", err)
		}
	}
}
