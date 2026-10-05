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
	"time"
)

func TestTableCoreSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	d := stuntgres.NewDriver()
	base, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	if err = db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	exec := func(prepared bool, q string, count int64, args ...any) {
		t.Helper()
		var r sql.Result
		var e error
		if prepared {
			s, err := db.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(q, err)
			}
			r, e = s.ExecContext(ctx, args...)
			s.Close()
		} else {
			r, e = db.ExecContext(ctx, q, args...)
		}
		if e != nil {
			t.Fatal(q, e)
		}
		n, e := r.RowsAffected()
		if e != nil || n != count {
			t.Fatal(q, n, e)
		}
	}
	diagnostic := func(err error, code string) {
		t.Helper()
		var e *stuntgres.Error
		if !errors.As(err, &e) || e.Code != code {
			t.Fatalf("got %v, want %s", err, code)
		}
	}
	query := func(prepared bool, q string, names, types []string, nullable []bool, want [][]any) {
		t.Helper()
		var rows *sql.Rows
		var err error
		if prepared {
			s, e := db.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			rows, err = s.QueryContext(ctx)
		} else {
			rows, err = db.QueryContext(ctx, q)
		}
		if err != nil {
			t.Fatal(q, err)
		}
		defer rows.Close()
		labels, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(labels, names) {
			t.Fatal(labels, err, names)
		}
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(types) {
			t.Fatal(columns, err)
		}
		for i, n := range types {
			c := columns[i]
			v, known := c.Nullable()
			var scan reflect.Type
			switch n {
			case "INT4", "INT8":
				scan = reflect.TypeFor[int64]()
			case "TEXT":
				scan = reflect.TypeFor[string]()
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			}
			if c.DatabaseTypeName() != n || c.ScanType() != scan || !known || v != nullable[i] {
				t.Fatal(i, c, v, known)
			}
		}
		got := make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(names))
			dest := make([]any, len(names))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			got = append(got, values)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if want == nil {
			want = [][]any{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, want)
		}
	}
	exec(false, "CREATE TABLE source(i INTEGER NOT NULL,s TEXT,f BOOLEAN)", 0)
	exec(true, "INSERT INTO source VALUES($1,$2,$3)", 1, 7, "O'Brien", true)
	exec(true, "CREATE TABLE typed(i INT4 NOT NULL,b INT8,s TEXT,f BOOL)", 0)
	exec(true, "INSERT INTO typed VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien", true)
	exec(false, `CREATE TABLE quoted_types(i "int4" NOT NULL,b "int8",s "text",f "bool")`, 0)
	exec(true, "INSERT INTO quoted_types VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien", true)
	typedRows := [][]any{{int64(7), int64(9223372036854775807), "O'Brien", true}}
	for _, prepared := range []bool{false, true} {
		query(prepared, "SELECT * FROM typed", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, typedRows)
	}
	query(true, "SELECT * FROM quoted_types", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, typedRows)
	quotedUnknown, err := db.PrepareContext(ctx, `CREATE TABLE bad (id "integer")`)
	if err != nil {
		t.Fatal("quoted unknown failed during preparation", err)
	}
	_, err = quotedUnknown.ExecContext(ctx)
	quotedUnknown.Close()
	diagnostic(err, "42704")
	exec(false, "CREATE TABLE clone(lead BIGINT,LIKE source,tail TEXT)", 0)
	names := []string{"lead", "i", "s", "f", "tail"}
	types := []string{"INT8", "INT4", "TEXT", "BOOL", "TEXT"}
	nullable := []bool{true, false, true, true, true}
	query(true, "SELECT * FROM clone", names, types, nullable, nil)
	exec(true, "INSERT INTO clone VALUES($1,$2,$3,$4,$5)", 1, 9, 7, "O'Brien", true, "tail")
	_, err = db.ExecContext(ctx, "INSERT INTO clone(i) VALUES(NULL)")
	diagnostic(err, "23502")
	cloneRows := [][]any{{int64(9), int64(7), "O'Brien", true, "tail"}}
	query(false, "SELECT * FROM clone", names, types, nullable, cloneRows)
	exec(false, "CREATE TABLE one(a INT NOT NULL)", 0)
	exec(false, "CREATE TABLE two(b TEXT)", 0)
	exec(true, "CREATE TABLE interleaved(head BOOL,LIKE one,middle BIGINT,LIKE two,tail TEXT)", 0)
	query(false, "SELECT * FROM interleaved", []string{"head", "a", "middle", "b", "tail"}, []string{"BOOL", "INT4", "INT8", "TEXT", "TEXT"}, []bool{true, false, true, true, true}, nil)
	for _, tc := range []struct{ q, code string }{{"CREATE TABLE bad(LIKE one,a TEXT)", "42701"}, {"CREATE TABLE bad(LIKE source INCLUDING ALL)", "0A000"}, {"CREATE TABLE bad(LIKE source EXCLUDING CONSTRAINTS)", "0A000"}} {
		s, e := db.PrepareContext(ctx, tc.q)
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.ExecContext(ctx)
		s.Close()
		diagnostic(e, tc.code)
		rows, e := db.QueryContext(ctx, "SELECT * FROM bad")
		if rows != nil {
			rows.Close()
		}
		diagnostic(e, "42P01")
	}
	exec(false, "CREATE TABLE z()", 0)
	exec(true, "CREATE TABLE zl(LIKE z)", 0)
	for _, z := range []string{"z", "zl"} {
		for _, prepared := range []bool{false, true} {
			query(prepared, "SELECT * FROM "+z, []string{}, []string{}, []bool{}, nil)
		}
	}
	for _, q := range []string{"CREATE TABLE IF NOT EXISTS source(i no_such_type)", `CREATE TABLE IF NOT EXISTS source(i "integer")`, "CREATE TABLE IF NOT EXISTS source(i SMALLINT)", "CREATE TABLE IF NOT EXISTS source(LIKE missing INCLUDING ALL)"} {
		notices = nil
		exec(true, q, 0)
		want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "source" already exists, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatal(notices)
		}
	}
	notices = nil
	_, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS source(i CHARACTER VARYING)")
	diagnostic(err, "0A000")
	excluded, err := db.PrepareContext(ctx, "CREATE TABLE IF NOT EXISTS source(i CHARACTER VARYING)")
	if excluded != nil {
		excluded.Close()
		t.Fatal("excluded family syntax exposed a prepared statement")
	}
	diagnostic(err, "0A000")
	if len(notices) != 0 {
		t.Fatal("excluded family syntax emitted skip notice", notices)
	}
	query(false, "SELECT * FROM source", []string{"i", "s", "f"}, []string{"INT4", "TEXT", "BOOL"}, []bool{false, true, true}, [][]any{{int64(7), "O'Brien", true}})
	exec(false, "DROP TABLE source RESTRICT", 0)
	query(true, "SELECT * FROM clone", names, types, nullable, cloneRows)
	exec(false, "CREATE TABLE a(id INT)", 0)
	exec(false, "CREATE TABLE b(id INT)", 0)
	exec(false, "INSERT INTO a VALUES(11)", 1)
	exec(false, "INSERT INTO b VALUES(22)", 1)
	_, err = db.ExecContext(ctx, "DROP TABLE a,missing,b")
	diagnostic(err, "42P01")
	for i, n := range []string{"a", "b"} {
		query(false, "SELECT * FROM "+n, []string{"id"}, []string{"INT4"}, []bool{true}, [][]any{{int64(11 + 11*i)}})
	}
	notices = nil
	exec(true, "DROP TABLE IF EXISTS a,missing,public.a,b CASCADE", 0)
	wantNotice := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `table "missing" does not exist, skipping`}}
	if !reflect.DeepEqual(notices, wantNotice) {
		t.Fatal(notices)
	}
	for _, n := range []string{"a", "b"} {
		rows, e := db.QueryContext(ctx, "SELECT * FROM "+n)
		if rows != nil {
			rows.Close()
		}
		diagnostic(e, "42P01")
	}
	exec(false, "CREATE TABLE held(id INT)", 0)
	exec(false, "INSERT INTO held VALUES(11),(22)", 2)
	stmt, err := db.PrepareContext(ctx, "SELECT id FROM held")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id FROM held ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	exec(false, "DROP TABLE held", 0)
	rows, err := stmt.QueryContext(ctx)
	if rows != nil {
		rows.Close()
	}
	diagnostic(err, "42P01")
	exec(false, "CREATE TABLE held(other TEXT)", 0)
	exec(false, "INSERT INTO held VALUES('fresh')", 1)
	rows, err = stmt.QueryContext(ctx)
	if rows != nil {
		rows.Close()
	}
	diagnostic(err, "42703")
	if !reflect.DeepEqual(held.Columns(), []string{"id"}) || held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
		t.Fatal("held metadata changed")
	}
	values := make([]driver.Value, 1)
	for _, n := range []int64{11, 22} {
		if e := held.Next(values); e != nil || values[0] != n {
			t.Fatal(values, e)
		}
	}
	if err = held.Next(values); err != io.EOF {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`CREATE TABLE "public"."clone" ("lead" BIGINT, "i" INTEGER NOT NULL, "s" TEXT, "f" BOOLEAN, "tail" TEXT);`, `CREATE TABLE "public"."z" ();`, `CREATE TABLE "public"."zl" ();`} {
		if !strings.Contains(dump.String(), part) {
			t.Fatal("missing expanded shape", part, dump.String())
		}
	}
	for _, part := range []string{"LIKE", `"public"."source"`, `"public"."a"`, `"public"."b"`} {
		if strings.Contains(dump.String(), part) {
			t.Fatal("dropped/source definition serialized", part)
		}
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT * FROM clone")
	if rows != nil {
		rows.Close()
	}
	diagnostic(err, "42P01")
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	query(true, "SELECT * FROM clone", names, types, nullable, cloneRows)
	query(false, "SELECT * FROM typed", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, typedRows)
	query(true, "SELECT * FROM quoted_types", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, typedRows)
	for _, z := range []string{"z", "zl"} {
		query(true, "SELECT * FROM "+z, []string{}, []string{}, []bool{}, nil)
	}
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("redump mismatch", err, redump.String())
	}
	for _, script := range []string{"CREATE TABLE replacement(LIKE missing)", "CREATE TABLE replacement(id INT,id TEXT)", "CREATE TABLE replacement(id no_such_type)"} {
		if err = stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted")
		}
		query(false, "SELECT * FROM clone", names, types, nullable, cloneRows)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = db.ExecContext(canceled, "DROP TABLE clone")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	query(false, "SELECT * FROM clone", names, types, nullable, cloneRows)
}
