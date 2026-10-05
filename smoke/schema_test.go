package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestSchemaSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("schema-smoke")
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(connector.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
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
	command := func(c *sql.Conn, q string, n int64) {
		t.Helper()
		if err := roleCommand(ctx, c, q, n, false); err != nil {
			t.Fatal(q, err)
		}
	}
	state := func(err error, code string) {
		t.Helper()
		var e *stuntgres.Error
		if !errors.As(err, &e) || e.Code != code {
			t.Fatalf("got %v want %s", err, code)
		}
	}
	emptyCreation := func(q string, prepared bool) {
		t.Helper()
		var failure error
		if prepared {
			s, err := a.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal("empty-path CREATE must Prepare successfully", err)
			}
			_, failure = s.ExecContext(ctx)
			s.Close()
		} else {
			_, failure = a.ExecContext(ctx, q)
		}
		if err := settingsFullDiagnostic(failure, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: "no schema has been selected to create in", Position: "14"}); err != nil {
			t.Fatal(err)
		}
	}
	value := func(c *sql.Conn, q string, held *sql.Stmt, want int64) {
		t.Helper()
		var r *sql.Rows
		var err error
		if held != nil {
			r, err = held.QueryContext(ctx)
		} else {
			r, err = c.QueryContext(ctx, q)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		labels, err := r.Columns()
		if err != nil || !reflect.DeepEqual(labels, []string{"id"}) {
			t.Fatal(labels, err)
		}
		types, err := r.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
			t.Fatal(types, err)
		}
		if !r.Next() {
			t.Fatal("missing row", r.Err())
		}
		var got int64
		if err = r.Scan(&got); err != nil || got != want {
			t.Fatal(got, want, err)
		}
		if r.Next() || r.Err() != nil {
			t.Fatal("extra/error", r.Err())
		}
	}
	current := func(c *sql.Conn, want *string) {
		t.Helper()
		for _, q := range []string{"SELECT current_schema", "SELECT current_schema()"} {
			r, err := c.QueryContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			types, err := r.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "NAME" {
				t.Fatal(types, err)
			}
			if !r.Next() {
				t.Fatal(r.Err())
			}
			var got sql.NullString
			if err = r.Scan(&got); err != nil {
				t.Fatal(err)
			}
			if want == nil && got.Valid || want != nil && (!got.Valid || got.String != *want) {
				t.Fatalf("current schema %#v want %v", got, want)
			}
			if r.Next() || r.Err() != nil {
				t.Fatal(r.Err())
			}
			r.Close()
		}
	}
	for _, q := range []string{"CREATE SCHEMA s1", "CREATE SCHEMA s2", "CREATE SCHEMA empty_schema", `CREATE SCHEMA "dot.schema"`, "CREATE TABLE s1.t(id INT)", "CREATE TABLE s2.t(id INT)"} {
		command(a, q, 0)
	}
	command(a, "INSERT INTO s1.t VALUES(7)", 1)
	command(a, "INSERT INTO s2.t VALUES(9)", 1)
	value(a, "SELECT id FROM s1.t", nil, 7)
	value(a, "SELECT id FROM s2.t", nil, 9)
	qualified, err := a.PrepareContext(ctx, "SELECT id FROM s1.t")
	if err != nil {
		t.Fatal(err)
	}
	defer qualified.Close()
	value(a, "", qualified, 7)
	command(a, "SET search_path TO s1,s2", 0)
	command(b, "SET search_path TO s2,s1", 0)
	one, two := "s1", "s2"
	current(a, &one)
	current(b, &two)
	if err := roleCommand(ctx, a, "SELECT current_schema()", 1, true); err != nil {
		t.Fatal(err)
	}
	held, err := a.PrepareContext(ctx, "SELECT id FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	value(a, "", held, 7)
	value(b, "SELECT id FROM t", nil, 9)
	command(a, "SET search_path TO missing,s2,s1", 0)
	value(a, "", held, 9)
	value(b, "SELECT id FROM t", nil, 9)
	pathText := func(q, label, want string, prepared bool) {
		t.Helper()
		var r *sql.Rows
		var err error
		if prepared {
			s, er := a.PrepareContext(ctx, q)
			if er != nil {
				t.Fatal(er)
			}
			defer s.Close()
			r, err = s.QueryContext(ctx)
		} else {
			r, err = a.QueryContext(ctx, q)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		labels, err := r.Columns()
		if err != nil || !reflect.DeepEqual(labels, []string{label}) {
			t.Fatal(labels, err)
		}
		types, err := r.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
			t.Fatal(types, err)
		}
		if !r.Next() {
			t.Fatal(r.Err())
		}
		var got string
		if err = r.Scan(&got); err != nil || got != want {
			t.Fatal(got, want, err)
		}
		if r.Next() || r.Err() != nil {
			t.Fatal(r.Err())
		}
	}
	for _, prepared := range []bool{false, true} {
		if err = roleCommand(ctx, a, "SET search_path TO 's1,'", 0, prepared); err != nil {
			t.Fatal(err)
		}
		pathText("SHOW search_path", "search_path", `"s1,"`, prepared)
		current(a, nil)
	}
	command(a, `CREATE SCHEMA "s1," CREATE TABLE t(id INT)`, 0)
	command(a, `INSERT INTO "s1,".t VALUES(11)`, 1)
	for _, prepared := range []bool{false, true} {
		if err = roleCommand(ctx, a, "SET search_path TO 's1,'", 0, prepared); err != nil {
			t.Fatal(err)
		}
		pathText("SELECT current_setting('search_path')", "current_setting", `"s1,"`, prepared)
		literal := "s1,"
		current(a, &literal)
		if prepared {
			s, er := a.PrepareContext(ctx, "SELECT id FROM t")
			if er != nil {
				t.Fatal(er)
			}
			value(a, "", s, 11)
			s.Close()
		} else {
			value(a, "SELECT id FROM t", nil, 11)
		}
		value(a, "SELECT id FROM s1.t", nil, 7)
		if err = roleCommand(ctx, a, "SET search_path TO ''", 0, prepared); err != nil {
			t.Fatal(err)
		}
		pathText("SHOW search_path", "search_path", `""`, prepared)
		current(a, nil)
		emptyCreation("CREATE TABLE empty_path(id INT)", prepared)
		pathText("SELECT set_config('search_path','',false)", "set_config", "", prepared)
		pathText("SHOW search_path", "search_path", "", prepared)
		current(a, nil)
		emptyCreation("CREATE TABLE raw_empty_path(id INT)", prepared)
		if err = roleCommand(ctx, a, `SET search_path TO "lowercase","A.B",public`, 0, prepared); err != nil {
			t.Fatal(err)
		}
		pathText("SHOW search_path", "search_path", `lowercase, "A.B", public`, prepared)
		pub := "public"
		current(a, &pub)
		for _, raw := range []string{`""`, `"", s2`} {
			pathText("SELECT set_config('search_path','"+raw+"',false)", "set_config", raw, prepared)
			pathText("SHOW search_path", "search_path", raw, prepared)
			if raw == `""` {
				current(a, nil)
			} else {
				current(a, &two)
				value(a, "SELECT id FROM t", nil, 9)
			}
		}
		if err = roleCommand(ctx, a, "SET search_path TO '',s2", 0, prepared); err != nil {
			t.Fatal(err)
		}
		pathText("SHOW search_path", "search_path", `"", s2`, prepared)
		current(a, &two)
		value(a, "SELECT id FROM t", nil, 9)
		command(a, "SET search_path TO s2", 0)
		err = roleCommand(ctx, a, "SELECT set_config('search_path','s1,',false)", 0, prepared)
		state(err, "22023")
		pathText("SHOW search_path", "search_path", "s2", prepared)
		pathText("SELECT current_setting('search_path')", "current_setting", "s2", prepared)
		current(a, &two)
		value(a, "", held, 9)
		value(b, "SELECT id FROM t", nil, 9)
	}
	command(a, "SET search_path TO ''", 0)
	current(a, nil)
	emptyCreation("CREATE TABLE nowhere(id INT)", false)
	value(a, "SELECT id FROM s1.t", nil, 7)
	command(a, "RESET search_path", 0)
	pub := "public"
	current(a, &pub)
	command(a, `SET search_path TO "dot.schema"`, 0)
	dot := "dot.schema"
	current(a, &dot)
	command(a, "CREATE TABLE quoted(id INT)", 0)
	command(a, "CREATE USER schema_owner", 0)
	command(a, "CREATE USER table_owner", 0)
	command(a, "CREATE SCHEMA nested AUTHORIZATION schema_owner CREATE TABLE t(id INT) CREATE TABLE u(id INT)", 0)
	command(a, "SET SESSION AUTHORIZATION schema_owner", 0)
	command(a, "CREATE TABLE nested.extra(id INT)", 0)
	command(a, "INSERT INTO nested.t VALUES(11)", 1)
	value(a, "SELECT id FROM nested.t", nil, 11)
	_, err = a.ExecContext(ctx, "CREATE SCHEMA denied")
	state(err, "42501")
	command(a, "ALTER SCHEMA nested OWNER TO schema_owner", 0)
	command(a, "RESET SESSION AUTHORIZATION", 0)
	command(a, "CREATE SCHEMA mixed AUTHORIZATION schema_owner", 0)
	command(a, "CREATE TABLE mixed.t(id INT)", 0)
	command(a, "INSERT INTO mixed.t VALUES(13)", 1)
	command(a, "ALTER SCHEMA mixed OWNER TO table_owner", 0)
	command(b, "SET SESSION AUTHORIZATION table_owner", 0)
	_, err = b.PrepareContext(ctx, "SELECT id FROM nested.t")
	state(err, "42501")
	command(b, "RESET SESSION AUTHORIZATION", 0)
	_, err = a.ExecContext(ctx, "CREATE SCHEMA failed CREATE TABLE t(id INT) CREATE TABLE t(id INT)")
	state(err, "42P07")
	_, err = a.ExecContext(ctx, "CREATE TABLE failed.x(id INT)")
	state(err, "3F000")
	rows, err := a.QueryContext(ctx, "SELECT id FROM s1.t")
	if err != nil {
		t.Fatal(err)
	}
	command(a, "ALTER SCHEMA s1 RENAME TO renamed", 0)
	value(a, "SELECT id FROM renamed.t", nil, 7)
	_, err = a.QueryContext(ctx, "SELECT id FROM s1.t")
	state(err, "42P01")
	value(a, "SELECT id FROM s2.t", nil, 9)
	_, err = a.ExecContext(ctx, "DROP SCHEMA renamed")
	state(err, "2BP01")
	value(a, "SELECT id FROM renamed.t", nil, 7)
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `CREATE SCHEMA "empty_schema" AUTHORIZATION "postgres";`) {
		t.Fatal(dump.String())
	}
	privateScript := dump.String() + "SET SESSION AUTHORIZATION 'postgres'; CREATE SCHEMA private_mixed AUTHORIZATION schema_owner; SET SESSION AUTHORIZATION table_owner; CREATE TABLE private_mixed.t(id INT); INSERT INTO private_mixed.t VALUES(17);"
	if err = stuntgres.Restore(ctx, db, strings.NewReader(privateScript)); err != nil {
		t.Fatal(err)
	}
	dump.Reset()
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	value(a, "SELECT id FROM renamed.t", nil, 7)
	value(a, "SELECT id FROM s2.t", nil, 9)
	value(a, "SELECT id FROM mixed.t", nil, 13)
	value(a, "SELECT id FROM private_mixed.t", nil, 17)
	command(b, "SET SESSION AUTHORIZATION table_owner", 0)
	_, err = b.PrepareContext(ctx, "INSERT INTO private_mixed.t VALUES(19)")
	state(err, "42501")
	_, err = b.ExecContext(ctx, "INSERT INTO private_mixed.t VALUES(19)")
	state(err, "42501")
	command(b, "RESET SESSION AUTHORIZATION", 0)
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var old int64
	if err = rows.Scan(&old); err != nil || old != 7 {
		t.Fatal(old, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	_, err = a.ExecContext(ctx, "DROP ROLE table_owner")
	state(err, "2BP01")
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE SCHEMA bad; INSERT INTO missing VALUES(1);")); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	value(a, "SELECT id FROM renamed.t", nil, 7)
	command(a, "DROP SCHEMA renamed CASCADE", 0)
	value(a, "SELECT id FROM s2.t", nil, 9)
	for _, prepared := range []bool{false, true} {
		start := len(notices)
		if err = roleCommand(ctx, a, "CREATE SCHEMA IF NOT EXISTS s2", 0, prepared); err != nil {
			t.Fatal(err)
		}
		if err = roleCommand(ctx, a, "DROP SCHEMA IF EXISTS missing_smoke_schema", 0, prepared); err != nil {
			t.Fatal(err)
		}
		want := []stuntgres.Error{
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P06", Message: `schema "s2" already exists, skipping`},
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "missing_smoke_schema" does not exist, skipping`},
		}
		if !reflect.DeepEqual(notices[start:], want) {
			t.Fatalf("notices %#v want %#v", notices[start:], want)
		}
	}
	command(a, "CREATE SCHEMA shape CREATE TABLE t(id TEXT)", 0)
	command(a, "SET search_path TO shape", 0)
	_, err = held.QueryContext(ctx)
	state(err, "0A000")
	t.Run("public_lifecycle", schemaPublicLifecycleSmoke)
}
