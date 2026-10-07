package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestSearchPathSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	exec := func(c *sql.Conn, q string) {
		t.Helper()
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	value := func(c *sql.Conn, q string, want any) {
		t.Helper()
		var got any
		if err := c.QueryRowContext(ctx, q).Scan(&got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %#v want %#v: %v", q, got, want, err)
		}
	}
	for _, q := range []string{"CREATE SCHEMA sp1", "CREATE SCHEMA sp2", "CREATE TABLE sp1.t(id pg_catalog.int4)", "CREATE TABLE sp2.t(id INT)", "INSERT INTO sp1.t VALUES(7)", "INSERT INTO sp2.t VALUES(9)", "SET search_path TO missing,sp1,sp1,sp2"} {
		exec(a, q)
	}
	exec(b, "SET search_path TO sp1,sp2")
	table, err := a.PrepareContext(ctx, "SELECT id FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	schemas, err := a.PrepareContext(ctx, "SELECT current_schemas($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer schemas.Close()
	read := func(flag any, want any) {
		t.Helper()
		rows, err := schemas.QueryContext(ctx, flag)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 {
			t.Fatal(types, err)
		}
		if types[0].Name() != "current_schemas" || types[0].DatabaseTypeName() != "_NAME" || types[0].ScanType() != reflect.TypeOf("") {
			t.Fatal(types)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var got any
		if err := rows.Scan(&got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	read(false, "{sp1,sp2}")
	read(true, "{pg_catalog,sp1,sp2}")
	read(nil, nil)
	var id int64
	if err := table.QueryRowContext(ctx).Scan(&id); err != nil || id != 7 {
		t.Fatal(id, err)
	}
	retained, err := schemas.QueryContext(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	exec(a, "SET search_path TO sp2,sp1")
	if !retained.Next() {
		t.Fatal(retained.Err())
	}
	var old string
	if err := retained.Scan(&old); err != nil || old != "{sp1,sp2}" {
		t.Fatal(old, err)
	}
	retained.Close()
	read(false, "{sp2,sp1}")
	if err := table.QueryRowContext(ctx).Scan(&id); err != nil || id != 9 {
		t.Fatal(id, err)
	}
	value(a, "SELECT id FROM sp1.t", int64(7))
	value(b, "SELECT id FROM t", int64(7))
	value(b, "SELECT current_schemas(false)", "{sp1,sp2}")
	for _, tc := range []struct {
		q    string
		want any
	}{
		{"SELECT pg_catalog.num_nulls(NULL,7)", int64(1)},
		{"SELECT pg_catalog.current_schemas(false)", "{sp2,sp1}"},
		{"SELECT pg_catalog.current_setting('search_path')", "sp2, sp1"},
		{"SELECT CAST(id AS pg_catalog.int4) FROM sp1.t", int64(7)},
		{"SELECT pg_catalog.int4(id) FROM sp1.t", int64(7)},
		{"SELECT pg_catalog.count(*) FROM sp1.t", int64(1)},
		{"SELECT id OPERATOR(pg_catalog.-) 2 + 3 FROM sp1.t", int64(2)},
		{"SELECT id OPERATOR(pg_catalog.=) 7 FROM sp1.t", true},
	} {
		value(a, tc.q, tc.want)
	}
	exec(a, "CREATE SEQUENCE sp1.seq")
	value(a, "SELECT pg_catalog.nextval('sp1.seq')", int64(1))
	value(a, "SELECT pg_catalog.set_config('search_path','sp1',false)", "sp1")
	exec(a, "SET search_path TO pg_temp,sp1")
	read(false, "{sp1}")
	read(true, "{pg_catalog,sp1}")
	exec(a, "CREATE TEMP SEQUENCE temp_seq")
	var temp string
	if err := a.QueryRowContext(ctx, "SELECT current_schema()").Scan(&temp); err != nil || !strings.HasPrefix(temp, "pg_temp_") {
		t.Fatal(temp, err)
	}
	exec(a, "SET search_path TO sp1,pg_temp,pg_temp")
	read(false, "{sp1,"+temp+"}")
	read(true, "{pg_catalog,sp1,"+temp+"}")
	value(a, "SELECT pg_catalog.num_nulls(NULL)", int64(1))
	exec(a, `CREATE SCHEMA "path,comma"`)
	exec(a, `SET search_path TO "path,comma"`)
	read(false, `{"path,comma"}`)
	exec(a, "SET search_path TO sp2")
	_, err = a.ExecContext(ctx, "SELECT set_config('search_path','sp1,',false)")
	if err := settingsFullDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `invalid value for parameter "search_path": "sp1,"`, Detail: "List syntax is invalid."}); err != nil {
		t.Fatal(err)
	}
	read(false, "{sp2}")
	exec(a, "SET search_path TO missing,sp2,sp1")
	exec(a, "CREATE TABLE created(id INT)")
	exec(a, "CREATE TABLE sp1.defaults(id INT DEFAULT (7 OPERATOR(pg_catalog.+) 1))")
	exec(a, "INSERT INTO sp1.defaults DEFAULT VALUES")
	exec(a, "CREATE VIEW sp1.v AS SELECT id OPERATOR(pg_catalog.+) 1 AS n FROM sp1.t")
	value(a, "SELECT n FROM sp1.v", int64(8))
	exec(a, "CREATE VIEW sp1.math_path AS SELECT pg_catalog.round(1.25,1) AS n FROM sp1.t")
	checkMath := func(rows *sql.Rows) {
		t.Helper()
		defer rows.Close()
		metadata, err := rows.ColumnTypes()
		if err != nil || len(metadata) != 1 || metadata[0].Name() != "n" || metadata[0].DatabaseTypeName() != "NUMERIC" || metadata[0].ScanType() != reflect.TypeFor[string]() {
			t.Fatal("persisted math metadata", metadata, err)
		}
		if !rows.Next() {
			t.Fatal("missing persisted math row", rows.Err())
		}
		var raw any
		if err := rows.Scan(&raw); err != nil || raw != "1.3" {
			t.Fatal("persisted math value", raw, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra persisted math row", rows.Err())
		}
	}
	const mathQuery = "SELECT n FROM sp1.math_path"
	mathRows, err := a.QueryContext(ctx, mathQuery)
	if err != nil {
		t.Fatal(err)
	}
	checkMath(mathRows)
	mathHeld, err := a.PrepareContext(ctx, mathQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer mathHeld.Close()
	mathRows, err = mathHeld.QueryContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkMath(mathRows)
	exec(a, "CREATE VIEW sp1.paths AS SELECT pg_catalog.current_schemas(false) AS paths FROM sp1.t")
	value(a, "SELECT paths FROM sp1.paths", "{sp2,sp1}")
	exec(a, "SET search_path TO sp1")
	value(a, "SELECT paths FROM sp1.paths", "{sp1}")
	exec(a, "CREATE TABLE sp1.temporal(t pg_catalog.time(3),ts pg_catalog.timestamp(3))")
	exec(a, "INSERT INTO sp1.temporal VALUES('12:34:56.123789','2000-01-01 12:34:56.123789')")
	checkTemporal := func(rows *sql.Rows) {
		t.Helper()
		defer rows.Close()
		metadata, err := rows.ColumnTypes()
		if err != nil || len(metadata) != 2 {
			t.Fatal(metadata, err)
		}
		for i, expected := range []struct {
			name, typ string
			scan      reflect.Type
		}{{"t", "TIME", reflect.TypeFor[string]()}, {"ts", "TIMESTAMP", reflect.TypeFor[time.Time]()}} {
			if metadata[i].Name() != expected.name || metadata[i].DatabaseTypeName() != expected.typ || metadata[i].ScanType() != expected.scan {
				t.Fatal("temporal metadata", metadata[i], expected)
			}
		}
		if !rows.Next() {
			t.Fatal("missing temporal row", rows.Err())
		}
		var clock, timestamp any
		if err := rows.Scan(&clock, &timestamp); err != nil {
			t.Fatal(err)
		}
		if clock != "12:34:56.124" || !reflect.DeepEqual(timestamp, time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC)) {
			t.Fatal("temporal values", clock, timestamp)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra temporal row", rows.Err())
		}
	}
	const temporalQuery = "SELECT t,ts FROM sp1.temporal"
	temporalRows, err := a.QueryContext(ctx, temporalQuery)
	if err != nil {
		t.Fatal(err)
	}
	checkTemporal(temporalRows)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "SET search_path") {
		t.Fatal("dump serialized session path")
	}
	if !strings.Contains(dump.String(), `CREATE TABLE "sp1"."temporal" ("t" TIME(3), "ts" TIMESTAMP(3))`) {
		t.Fatal("dump lost canonical temporal precision", dump.String())
	}
	dumpText := dump.String()
	viewStart := strings.Index(dumpText, `CREATE VIEW "sp1"."math_path"`)
	if viewStart < 0 {
		t.Fatal("dump lost qualified math view", dumpText)
	}
	viewLine, _, _ := strings.Cut(dumpText[viewStart:], "\n")
	if !strings.Contains(viewLine, "pg_catalog.round(") || !strings.Contains(viewLine, `FROM "sp1"."t"`) {
		t.Fatal("dump lost qualified math or source identity", viewLine)
	}
	exec(a, "SET search_path TO sp2,sp1")
	freshConnector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-fresh-temporal")
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(freshConnector)
	defer fresh.Close()
	freshSession, err := fresh.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer freshSession.Close()
	exec(freshSession, "SET search_path TO pg_catalog")
	if err := stuntgres.Restore(ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	value(freshSession, "SELECT current_schemas(false)", "{pg_catalog}")
	restoredTemporal, err := freshSession.PrepareContext(ctx, temporalQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredTemporal.Close()
	freshMath, err := freshSession.PrepareContext(ctx, mathQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer freshMath.Close()
	for _, path := range []string{"pg_catalog", "sp2"} {
		exec(freshSession, "SET search_path TO "+path)
		rows, err := freshSession.QueryContext(ctx, temporalQuery)
		if err != nil {
			t.Fatal(err)
		}
		checkTemporal(rows)
		rows, err = restoredTemporal.QueryContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		checkTemporal(rows)
		rows, err = freshSession.QueryContext(ctx, mathQuery)
		if err != nil {
			t.Fatal(err)
		}
		checkMath(rows)
		rows, err = freshMath.QueryContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		checkMath(rows)
	}
	read(false, "{sp2,sp1}")
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	value(a, "SELECT id FROM sp1.t", int64(7))
	value(a, "SELECT id FROM sp2.t", int64(9))
	value(a, "SELECT id FROM sp1.defaults", int64(8))
	value(a, "SELECT n FROM sp1.v", int64(8))
	mathRows, err = a.QueryContext(ctx, mathQuery)
	if err != nil {
		t.Fatal(err)
	}
	checkMath(mathRows)
	mathRows, err = mathHeld.QueryContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkMath(mathRows)
	read(false, "{sp2,sp1}")
	value(a, "SELECT paths FROM sp1.paths", "{sp2,sp1}")
	value(a, "SELECT t FROM sp1.temporal", "12:34:56.124")
	value(a, "SELECT ts FROM sp1.temporal", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC))
	exec(a, "INSERT INTO sp1.temporal VALUES(TIME '12:34:56.125789',TIMESTAMP '2000-01-01 12:34:56.125789')")
	value(a, "SELECT t FROM sp1.temporal WHERE t='12:34:56.126'", "12:34:56.126")
	value(a, "SELECT ts FROM sp1.temporal WHERE t='12:34:56.126'", time.Date(2000, 1, 1, 12, 34, 56, 126000000, time.UTC))
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE broken(")); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	value(a, "SELECT id FROM sp1.t", int64(7))
}
