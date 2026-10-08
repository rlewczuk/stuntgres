package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestSessionIdentitySmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("identity smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	command := func(q string, n int64) {
		t.Helper()
		if err := roleCommand(ctx, conn, q, n, false); err != nil {
			t.Fatal(err)
		}
	}
	var a, b, c bool
	if err := conn.QueryRowContext(ctx, "SELECT current_catalog=current_database(),current_role=current_user,session_user=user").Scan(&a, &b, &c); err != nil || !a || !b || !c {
		t.Fatal(a, b, c, err)
	}
	command("SELECT current_user", 1)
	query := "SELECT current_catalog,current_database(),current_role,current_user,session_user,user,system_user"
	held, err := conn.PrepareContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	identity := func(want string) {
		t.Helper()
		rows, err := held.QueryContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(cols, []string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user", "system_user"}) {
			t.Fatal(cols, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range types {
			name := "NAME"
			if i == 6 {
				name = "TEXT"
			}
			if typ.DatabaseTypeName() != name || typ.ScanType() != reflect.TypeFor[string]() {
				t.Fatal(typ)
			}
		}
		var catalog, database, role, current, session, user string
		var system any
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		if err := rows.Scan(&catalog, &database, &role, &current, &session, &user, &system); err != nil || catalog != "identity smoke" || database != catalog || role != want || current != want || user != want || session != "postgres" || system != nil {
			t.Fatal(catalog, database, role, current, session, user, system, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	identity("postgres")
	command("CREATE ROLE identity_smoke_role", 0)
	for _, q := range []string{"SET ROLE identity_smoke_role", "SET SESSION ROLE 'identity_smoke_role'"} {
		command(q, 0)
		identity("identity_smoke_role")
		command("SET ROLE NONE", 0)
		identity("postgres")
	}
	command("SET ROLE identity_smoke_role", 0)
	command("RESET ROLE", 0)
	identity("postgres")
	command("SELECT current_catalog=current_database(),current_role=current_user,session_user=user", 1)
	for _, reset := range []string{"SET ROLE TO DEFAULT", "SET ROLE = DEFAULT", "SET SESSION ROLE TO DEFAULT"} {
		command("SET ROLE identity_smoke_role", 0)
		command(reset, 0)
		identity("postgres")
	}
	for _, tc := range []struct{ q, code, message, position string }{{"SET ROLE DEFAULT", "42601", `syntax error at or near "DEFAULT"`, "10"}, {"SELECT current_user()", "42601", `syntax error at or near "("`, "20"}, {"SET ROLE absent_identity_smoke", "22023", `role "absent_identity_smoke" does not exist`, ""}} {
		_, err := conn.ExecContext(ctx, tc.q)
		var d *stuntgres.Error
		want := &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if !errors.As(err, &d) || !reflect.DeepEqual(d, want) {
			t.Fatal(tc.q, err)
		}
	}
	for _, q := range []string{"SET LOCAL ROLE identity_smoke_role", "SET LOCAL ROLE TO DEFAULT"} {
		_, err := conn.ExecContext(ctx, q)
		var d *stuntgres.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "SET LOCAL ROLE is unsupported" || d.Position != "11" {
			t.Fatal(q, err)
		}
		identity("postgres")
	}
	command("BEGIN", 0)
	_, err = conn.ExecContext(ctx, "SET ROLE identity_smoke_role")
	var diag *stuntgres.Error
	if !errors.As(err, &diag) || diag.Code != "0A000" {
		t.Fatal(err)
	}
	command("ROLLBACK", 0)
	identity("postgres")
	_, err = conn.QueryContext(ctx, "SELECT current_database(1)")
	want := &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function current_database(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}
	if !errors.As(err, &diag) || !reflect.DeepEqual(diag, want) {
		t.Fatal(err)
	}
	command("SET SESSION AUTHORIZATION identity_smoke_role", 0)
	command("CREATE TABLE identity_smoke_t(id INT,who TEXT DEFAULT current_user)", 0)
	command("CREATE VIEW identity_smoke_v AS SELECT current_user AS who,system_user AS auth FROM identity_smoke_t", 0)
	command("RESET SESSION AUTHORIZATION", 0)
	command("SET ROLE identity_smoke_role", 0)
	command("INSERT INTO identity_smoke_t(id) VALUES(1)", 1)
	view, err := conn.PrepareContext(ctx, "SELECT who,auth FROM identity_smoke_v")
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	var who string
	var auth any
	if err := view.QueryRowContext(ctx).Scan(&who, &auth); err != nil || who != "identity_smoke_role" || auth != nil {
		t.Fatal(who, auth, err)
	}
	var retained driver.Rows
	if err := conn.Raw(func(raw any) error {
		var err error
		retained, err = raw.(driver.QueryerContext).QueryContext(ctx, "SELECT current_user,current_catalog,system_user", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	identity("identity_smoke_role")
	command("RESET ROLE", 0)
	if err := view.QueryRowContext(ctx).Scan(&who, &auth); err != nil || who != "postgres" || auth != nil {
		t.Fatal(who, auth, err)
	}
	old := make([]driver.Value, 3)
	if err := retained.Next(old); err != nil || !reflect.DeepEqual(old, []driver.Value{"identity_smoke_role", "identity smoke", nil}) {
		t.Fatal(old, err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT who FROM identity_smoke_t").Scan(&who); err != nil || who != "identity_smoke_role" {
		t.Fatal(who, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := conn.ExecContext(canceled, "SET ROLE identity_smoke_role"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	identity("postgres")
}
