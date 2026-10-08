package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

// This is the public feature entrypoint, not a baseline or wiring-only smoke.
func TestStringCoreSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(1)
	checkRows := func(rows *sql.Rows, err error, types []string, want []any) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		gotTypes, e := rows.ColumnTypes()
		if e != nil {
			t.Fatal(e)
		}
		if len(gotTypes) != len(types) {
			t.Fatal(gotTypes, types)
		}
		for i, typ := range gotTypes {
			scan := reflect.TypeFor[string]()
			if types[i] == "BOOL" {
				scan = reflect.TypeFor[bool]()
			} else if types[i] == "BYTEA" {
				scan = reflect.TypeFor[[]byte]()
			}
			if typ.DatabaseTypeName() != types[i] || typ.ScanType() != scan {
				t.Fatal(i, typ)
			}
			if types[i] == "TEXT" {
				n, ok := typ.Length()
				if !ok || n != math.MaxInt64 {
					t.Fatal(n, ok)
				}
			}
		}
		if !rows.Next() {
			t.Fatal("missing row", rows.Err())
		}
		got := make([]any, len(want))
		dst := make([]any, len(want))
		for i := range dst {
			dst[i] = &got[i]
		}
		if e = rows.Scan(dst...); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra row or scan error", rows.Err())
		}
	}
	check := func(q string, types []string, want []any, args ...any) {
		t.Helper()
		rows, e := db.QueryContext(ctx, q, args...)
		checkRows(rows, e, types, want)
		s, e := db.PrepareContext(ctx, q)
		if e != nil {
			t.Fatal(q, e)
		}
		defer s.Close()
		rows, e = s.QueryContext(ctx, args...)
		checkRows(rows, e, types, want)
	}
	exec := func(q string) {
		t.Helper()
		if _, e := db.ExecContext(ctx, q); e != nil {
			t.Fatal(q, e)
		}
	}
	check("SELECT 'a '::char(3)||'b '::char(3),'a '::varchar||'b '::varchar", []string{"TEXT", "TEXT"}, []any{"ab", "a b "})
	check("SELECT btrim('é🙂X🙂é','é🙂'),btrim(' x ',' '::char(1))", []string{"TEXT", "TEXT"}, []any{"X", " x "})
	check("SELECT normalize('äⓑc',NFC),normalize('äⓑc',NFD),normalize('äⓑc',NFKC),normalize('äⓑc',NFKD)", []string{"TEXT", "TEXT", "TEXT", "TEXT"}, []any{"äⓑc", "äⓑc", "äbc", "äbc"})
	check("SELECT 'é' IS NORMALIZED,'é' IS NOT NORMALIZED,NULL::text IS NOT NORMALIZED", []string{"BOOL", "BOOL", "BOOL"}, []any{false, true, nil})
	check("SELECT normalize('a')||normalize('̈'),normalize('a'||'̈')", []string{"TEXT", "TEXT"}, []any{"ä", "ä"})
	check(`SELECT "normalize"($1,$2)`, []string{"TEXT"}, []any{"é"}, "é", "nFc")
	s, e := db.PrepareContext(ctx, `SELECT 'x'::text||$1::bytea`)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ setting, want string }{{"hex", `x\x41`}, {"escape", "xA"}} {
		exec("SET bytea_output='" + tc.setting + "'")
		rows, e := s.QueryContext(ctx, []byte{65})
		checkRows(rows, e, []string{"TEXT"}, []any{tc.want})
	}
	s.Close()
	_, err = db.ExecContext(ctx, `SELECT "normalize"('x','bad')`)
	var diagnostic *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid normalization form: bad"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(diagnostic, err, want)
	}
	exec(`CREATE TABLE string_core_smoke(v TEXT DEFAULT normalize(trim(' é ')),CHECK (v IS NORMALIZED))`)
	exec("INSERT INTO string_core_smoke DEFAULT VALUES")
	r, e := db.PrepareContext(ctx, `SELECT '['::text||'public.string_core_smoke'::regclass,'public.string_core_smoke'::regclass||']'::text`)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ path, spelling string }{{"public", "string_core_smoke"}, {"pg_catalog", "public.string_core_smoke"}} {
		exec("SET search_path='" + tc.path + "'")
		rows, e := r.QueryContext(ctx)
		checkRows(rows, e, []string{"TEXT", "TEXT"}, []any{"[" + tc.spelling, tc.spelling + "]"})
	}
	r.Close()
	exec("SET search_path='public'")
	exec(`CREATE VIEW string_core_smoke_view AS SELECT btrim(v)||'!' AS v FROM string_core_smoke`)
	exec(`CREATE TABLE string_core_smoke_deparse(v TEXT DEFAULT btrim(' x '),s TEXT,e TEXT,CONSTRAINT string_core_smoke_ck CHECK(btrim(v,s)=e))`)
	check("SELECT pg_get_expr(a.adbin,a.adrelid,true) AS d FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON a.adrelid=c.oid WHERE c.relname='string_core_smoke_deparse'", []string{"TEXT"}, []any{"btrim(' x '::text)"})
	check("SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='string_core_smoke_ck'", []string{"TEXT"}, []any{"CHECK (btrim(v, s) = e)"})
	var tree string
	if err := db.QueryRowContext(ctx, "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='string_core_smoke_ck'").Scan(&tree); err != nil || !strings.Contains(tree, "String btrim") || !strings.Contains(tree, "Var attnum 2") {
		t.Fatal("structural string carrier", tree, err)
	}
	exec(`CREATE TABLE string_core_smoke_char(v CHAR(4),s CHAR(4),e TEXT,CONSTRAINT string_core_smoke_char_ck CHECK(btrim(v,s)=e))`)
	exec(`INSERT INTO string_core_smoke_char VALUES(' x ',' ',' x')`)
	trimEdges := []struct{ direction, provider, value string }{{"BOTH", "btrim", "Y"}, {"LEADING", "ltrim", "Yxx"}, {"TRAILING", "rtrim", "xxY"}}
	for _, edge := range trimEdges {
		name := "string_core_smoke_bytea_" + strings.ToLower(edge.direction)
		exec("CREATE TABLE " + name + "(v BYTEA,s BYTEA,e BYTEA,CONSTRAINT " + name + "_ck CHECK(trim(" + edge.direction + " s FROM v)=e))")
		exec("INSERT INTO " + name + " VALUES('xxYxx','x','" + edge.value + "')")
	}
	checkDeparseEdges := func() {
		check("SELECT pg_get_constraintdef(oid,false) AS d FROM pg_catalog.pg_constraint WHERE conname='string_core_smoke_char_ck'", []string{"TEXT"}, []any{"CHECK ((btrim((v)::text, (s)::text) = e))"})
		check("SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='string_core_smoke_char_ck'", []string{"TEXT"}, []any{"CHECK (btrim(v::text, s::text) = e)"})
		for _, edge := range trimEdges {
			name := "string_core_smoke_bytea_" + strings.ToLower(edge.direction)
			check("SELECT pg_get_constraintdef(oid,false) AS d FROM pg_catalog.pg_constraint WHERE conname='"+name+"_ck'", []string{"TEXT"}, []any{"CHECK ((TRIM(" + edge.direction + " s FROM v) = e))"})
			check("SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='"+name+"_ck'", []string{"TEXT"}, []any{"CHECK (TRIM(" + edge.direction + " s FROM v) = e)"})
			check("SELECT trim("+edge.direction+" s FROM v) AS value FROM "+name, []string{"BYTEA"}, []any{[]byte(edge.value)})
			var tree string
			if err := db.QueryRowContext(ctx, "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='"+name+"_ck'").Scan(&tree); err != nil || !strings.Contains(tree, "Binary "+edge.provider) || !strings.Contains(tree, "Var attnum 2") {
				t.Fatal("recursive BYTEA carrier", tree, err)
			}
		}
	}
	checkDeparseEdges()
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
	check("SELECT v FROM string_core_smoke_view", []string{"TEXT"}, []any{"é!"})
	checkDeparseEdges()
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("stored expression redump", err)
	}
	// Exercise actual parameter-free selected-function Restore inputs, not just
	// the evaluated row constants emitted by the earlier native dump.
	if err := stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE string_core_smoke_restore(v TEXT); INSERT INTO string_core_smoke_restore VALUES(normalize(btrim(' é '))||'!')`)); err != nil {
		t.Fatal("selected-function Restore", err)
	}
	check("SELECT v FROM string_core_smoke_restore", []string{"TEXT"}, []any{"é!"})
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM string_core_smoke_restore", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	err = stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE string_core_smoke_bad(v TEXT); INSERT INTO string_core_smoke_bad VALUES(pg_catalog.normalize('x','bad'))`))
	diagnostic = nil
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal("Restore must reach the runtime form diagnostic", diagnostic, err, want)
	}
	check("SELECT v FROM string_core_smoke_restore", []string{"TEXT"}, []any{"é!"})
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_catalog.pg_class WHERE relname='string_core_smoke_bad'").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed Restore published its target", count, err)
	}
	values := make([]driver.Value, 1)
	if err := held.Next(values); err != nil || values[0] != "é!" {
		t.Fatal("failed Restore changed retained cursor", values, err)
	}
	if err := held.Next(values); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
