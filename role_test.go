package stuntgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func rolePool(t *testing.T, d *Driver, dsn, name string) *sql.DB {
	t.Helper()
	c, err := d.OpenConnectorAs(dsn, name)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
func rolePublicCommand(t *testing.T, c *sql.Conn, query string, count int64, prepared bool) {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		s, prepareErr := c.PrepareContext(context.Background(), query)
		if prepareErr != nil {
			t.Fatal(query, prepareErr)
		}
		defer s.Close()
		result, err = s.ExecContext(context.Background())
	} else {
		result, err = c.ExecContext(context.Background(), query)
	}
	if err != nil {
		t.Fatal(query, err)
	}
	got, err := result.RowsAffected()
	if err != nil || got != count {
		t.Fatal(query, got, err)
	}
}
func rolePublicIdentity(t *testing.T, c *sql.Conn, want string, held *sql.Stmt) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if held == nil {
		rows, err = c.QueryContext(context.Background(), "SELECT session_user,current_user,session_user")
	} else {
		rows, err = held.QueryContext(context.Background())
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{"session_user", "current_user", "session_user"}) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != reflect.TypeFor[string]() {
			t.Fatal(typ)
		}
	}
	if !rows.Next() {
		t.Fatal("missing identity", rows.Err())
	}
	var a, b, cName string
	if err := rows.Scan(&a, &b, &cName); err != nil || a != want || b != want || cName != want {
		t.Fatal(a, b, cName, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra/error identity", rows.Err())
	}
}
func rolePublicDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" || d.Schema != "" || d.Table != "" || d.Column != "" || d.File != "" || d.Routine != "" {
		t.Fatalf("diagnostic %T %#v want %s %s", err, err, code, message)
	}
}

func TestRoleTrustedConnectorIsolationAndReplacement(t *testing.T) {
	ctx := context.Background()
	d := NewDriver()
	dsn := " exact / roles ? user=no&password=opaque "
	db := rolePool(t, d, dsn, "postgres")
	admin, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, sql := range []string{"CREATE USER a", "CREATE USER b", "CREATE ROLE nolog"} {
		rolePublicCommand(t, admin, sql, 0, false)
	}
	aPool := rolePool(t, d, dsn, "a")
	aPool.SetMaxOpenConns(1)
	bPool := rolePool(t, d, dsn, "b")
	a, err := aPool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	b, err := bPool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	rolePublicIdentity(t, a, "a", nil)
	rolePublicIdentity(t, b, "b", nil)
	stmt, err := a.PrepareContext(ctx, "SET SESSION AUTHORIZATION b")
	if err != nil {
		t.Fatal("eager authorization check", err)
	}
	_, err = stmt.ExecContext(ctx)
	stmt.Close()
	rolePublicDiagnostic(t, err, "42501", `permission denied to set session authorization "b"`)
	rolePublicIdentity(t, a, "a", nil)
	rolePublicIdentity(t, b, "b", nil)
	for _, name := range []string{"missing", "nolog", "A"} {
		pool := rolePool(t, d, dsn, name)
		err := pool.PingContext(ctx)
		if err == nil {
			t.Fatal("invalid authenticator admitted", name)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("host admission fabricated SQLSTATE", err)
		}
	}
	if _, err := d.OpenConnectorAs(dsn, ""); err == nil {
		t.Fatal("empty assertion")
	}
	rolePublicCommand(t, a, "SET application_name TO retained", 0, false)
	a.Close()
	a, err = aPool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var setting string
	if err := a.QueryRowContext(ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "retained" {
		t.Fatal(setting, err)
	}
	rolePublicIdentity(t, a, "a", nil)
	if err := a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	a.Close()
	a, err = aPool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolePublicIdentity(t, a, "a", nil)
	if err := a.QueryRowContext(ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "" {
		t.Fatal("replacement settings", setting, err)
	}
	// LOGIN is resolved at each physical connection, never cached in connector.
	rolePublicCommand(t, admin, "DROP ROLE a", 0, false)
	_ = a.Raw(func(any) error { return driver.ErrBadConn })
	a.Close()
	if err := aPool.PingContext(ctx); err == nil {
		t.Fatal("connector cached deleted identity")
	}
	rolePublicCommand(t, admin, "CREATE USER a", 0, false)
	if err := aPool.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	isolated := rolePool(t, NewDriver(), dsn, "a")
	if err := isolated.PingContext(ctx); err == nil {
		t.Fatal("Driver identity leak")
	}
	different := rolePool(t, d, strings.TrimSpace(dsn), "a")
	if err := different.PingContext(ctx); err == nil {
		t.Fatal("opaque DSN parsed or trimmed")
	}
}

func TestRolePreparedIdentityAndExecutionAuthorization(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "prepared-roles", "postgres")
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
	for _, sql := range []string{"CREATE USER owner", "CREATE ROLE reader", "SET SESSION AUTHORIZATION owner", "CREATE TABLE secret(id INT)", "INSERT INTO secret VALUES(7)"} {
		count := int64(0)
		if strings.HasPrefix(sql, "INSERT") {
			count = 1
		}
		rolePublicCommand(t, a, sql, count, true)
	}
	heldIdentity, err := a.PrepareContext(ctx, "SELECT session_user,current_user,session_user")
	if err != nil {
		t.Fatal(err)
	}
	defer heldIdentity.Close()
	heldTable, err := a.PrepareContext(ctx, "SELECT id FROM secret")
	if err != nil {
		t.Fatal(err)
	}
	defer heldTable.Close()
	rolePublicCommand(t, b, "SET SESSION AUTHORIZATION reader", 0, false)
	rolePublicIdentity(t, b, "reader", nil)
	rolePublicCommand(t, a, "SET SESSION AUTHORIZATION reader", 0, false)
	rolePublicIdentity(t, a, "reader", heldIdentity)
	var value int64
	err = heldTable.QueryRowContext(ctx).Scan(&value)
	rolePublicDiagnostic(t, err, "42501", "permission denied for table secret")
	rolePublicCommand(t, a, "SET SESSION AUTHORIZATION owner", 0, false)
	if err := heldTable.QueryRowContext(ctx).Scan(&value); err != nil || value != 7 {
		t.Fatal(value, err)
	}
	rolePublicIdentity(t, b, "reader", nil)
	rolePublicCommand(t, a, "RESET SESSION AUTHORIZATION", 0, false)
	rolePublicIdentity(t, a, "postgres", heldIdentity)
	rolePublicCommand(t, a, "SELECT current_user", 1, true)
}

func TestRoleInputAndConstantPlanningPermissionPrecedence(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "role-input-precedence", "postgres")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, query := range []string{"CREATE ROLE reader", "CREATE TABLE secret(id INT)", "INSERT INTO secret VALUES(7)", "SET SESSION AUTHORIZATION reader"} {
		count := int64(0)
		if strings.HasPrefix(query, "INSERT") {
			count = 1
		}
		rolePublicCommand(t, c, query, count, false)
	}
	for _, prepared := range []bool{false, true} {
		exec := func(query string, args ...any) error {
			if !prepared {
				_, err := c.ExecContext(ctx, query, args...)
				return err
			}
			stmt, err := c.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal("unexpected preparation failure", query, err)
			}
			defer stmt.Close()
			_, err = stmt.ExecContext(ctx, args...)
			return err
		}
		rolePublicDiagnostic(t, exec("INSERT INTO secret VALUES($1)", "bad"), "22P02", `invalid input syntax for type integer: "bad"`)
		rolePublicDiagnostic(t, exec("INSERT INTO secret VALUES($1)", "9"), "42501", "permission denied for table secret")
		rolePublicDiagnostic(t, exec("INSERT INTO secret VALUES(2147483648)"), "22003", "integer out of range")
	}
	rolePublicCommand(t, c, "RESET SESSION AUTHORIZATION", 0, false)
	var value int64
	if err := c.QueryRowContext(ctx, "SELECT id FROM secret").Scan(&value); err != nil || value != 7 {
		t.Fatal("denied inputs changed sentinel", value, err)
	}
}

// SET ROLE is supported by PostgreSQL but deliberately excluded here. Assert
// the native parser boundary at both public entry points, not oracle parity.
func TestRoleExcludedCommands(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "role-excluded-commands", "postgres")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	peer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	for _, query := range []string{
		"CREATE USER owner", "CREATE ROLE reader", "SET SESSION AUTHORIZATION owner",
		"CREATE TABLE sentinel(id INT)", "CREATE TABLE role(role INT)",
		"SET application_name TO 'SET ROLE reader'",
	} {
		rolePublicCommand(t, c, query, 0, false)
	}
	rolePublicCommand(t, c, "INSERT INTO sentinel VALUES(7)", 1, false)
	rolePublicCommand(t, c, "INSERT INTO role(role) VALUES(8)", 1, true)
	rolePublicCommand(t, peer, "SET SESSION AUTHORIZATION reader", 0, false)
	rolePublicCommand(t, peer, "SET application_name TO peer", 0, true)
	assertUnchanged := func() {
		t.Helper()
		rolePublicIdentity(t, c, "owner", nil)
		rolePublicIdentity(t, peer, "reader", nil)
		for _, check := range []struct {
			conn       *sql.Conn
			name, want string
		}{
			{c, "application_name", "SET ROLE reader"},
			{peer, "application_name", "peer"},
		} {
			var got string
			if err := check.conn.QueryRowContext(ctx, "SHOW "+check.name).Scan(&got); err != nil || got != check.want {
				t.Fatalf("setting %s=%q want %q: %v", check.name, got, check.want, err)
			}
		}
		for _, check := range []struct {
			query string
			want  int64
		}{
			{"SELECT id FROM sentinel", 7},
			{"SELECT role FROM role WHERE role=8 ORDER BY role", 8},
		} {
			rows, err := c.QueryContext(ctx, check.query)
			if err != nil {
				t.Fatal(err)
			}
			var got int64
			if !rows.Next() {
				rows.Close()
				t.Fatal("missing sentinel", rows.Err())
			}
			if err := rows.Scan(&got); err != nil || got != check.want {
				rows.Close()
				t.Fatalf("sentinel=%d want %d: %v", got, check.want, err)
			}
			if rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatal("extra/error sentinel", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertUnchanged()
	for _, tc := range []struct{ query, message, position string }{
		{"SET ROLE reader", "SET ROLE is unsupported", "5"},
		{`SET ROLE "Reader"`, "SET ROLE is unsupported", "5"},
		{"SET ROLE 'reader'", "SET ROLE is unsupported", "5"},
		{"SET SESSION ROLE reader", "SET ROLE is unsupported", "13"},
		{"SET SESSION ROLE 'reader'", "SET ROLE is unsupported", "13"},
		{"SET LOCAL ROLE reader", "SET ROLE is unsupported", "11"},
		{"SET LOCAL ROLE 'reader'", "SET ROLE is unsupported", "11"},
		{"SET ROLE DEFAULT", "SET ROLE is unsupported", "5"},
		{"SET SESSION ROLE DEFAULT", "SET ROLE is unsupported", "13"},
		{"SET LOCAL ROLE DEFAULT", "SET ROLE is unsupported", "11"},
		{"SET ROLE NONE", "SET ROLE is unsupported", "5"},
		{"SET ROLE TO reader", "SET ROLE is unsupported", "5"},
		{"SET ROLE = 'reader'", "SET ROLE is unsupported", "5"},
		{"SET SESSION ROLE TO DEFAULT", "SET ROLE is unsupported", "13"},
		{"SET LOCAL ROLE = NONE", "SET ROLE is unsupported", "11"},
		{"RESET ROLE", "RESET ROLE is unsupported", "7"},
		{"/* é */ SET ROLE reader", "SET ROLE is unsupported", "13"},
		{"INSERT INTO sentinel VALUES(9); SET ROLE reader", "SET ROLE is unsupported", "37"},
	} {
		for _, prepared := range []bool{false, true} {
			mode := "direct"
			if prepared {
				mode = "prepare"
			}
			t.Run(mode+"/"+tc.query, func(t *testing.T) {
				var err error
				if prepared {
					stmt, prepareErr := c.PrepareContext(ctx, tc.query)
					if stmt != nil {
						stmt.Close()
						t.Fatal("excluded command produced an executable statement")
					}
					err = prepareErr
				} else {
					result, execErr := c.ExecContext(ctx, tc.query)
					if result != nil {
						t.Fatal("excluded command produced an execution result")
					}
					err = execErr
				}
				var got *Error
				want := &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message, Position: tc.position}
				if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
					t.Fatalf("diagnostic=%#v want=%#v", err, want)
				}
				assertUnchanged()
			})
		}
	}
	rolePublicCommand(t, c, "RESET SESSION AUTHORIZATION", 0, true)
	rolePublicIdentity(t, c, "postgres", nil)
	rolePublicIdentity(t, peer, "reader", nil)
}

func TestRoleConnectorNoticeConfigurationPreservesAuthentication(t *testing.T) {
	ctx := context.Background()
	d := NewDriver()
	admin := rolePool(t, d, "role-notice-connector", "postgres")
	if _, err := admin.ExecContext(ctx, "CREATE USER notice_login"); err != nil {
		t.Fatal(err)
	}
	base, err := d.OpenConnectorAs("role-notice-connector", "notice_login")
	if err != nil {
		t.Fatal(err)
	}
	var notices []Error
	configured := base.(*Connector).WithNoticeHandler(func(notice Error) {
		notices = append(notices, notice)
	})
	db := sql.OpenDB(configured)
	defer db.Close()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rolePublicIdentity(t, c, "notice_login", nil)
	rolePublicCommand(t, c, "CREATE TABLE notice_login_rows(id INT)", 0, false)
	rolePublicCommand(t, c, "CREATE TABLE IF NOT EXISTS notice_login_rows(id INT,id TEXT)", 0, true)
	want := []Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_login_rows" already exists, skipping`}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatal(notices)
	}
	plain := sql.OpenDB(base)
	defer plain.Close()
	peer, err := plain.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	rolePublicIdentity(t, peer, "notice_login", nil)
	rolePublicCommand(t, peer, "CREATE TABLE IF NOT EXISTS notice_login_rows(id INT)", 0, false)
	if !reflect.DeepEqual(notices, want) {
		t.Fatal("handler configuration leaked to base connector", notices)
	}
}
