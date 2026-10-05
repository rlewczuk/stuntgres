package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

const roleDSN = "roles / exact? user=x"

func TestRoleSmoke(t *testing.T) {
	if err := roleRun(); err != nil {
		t.Fatal(err)
	}
}
func roleCommand(ctx context.Context, c *sql.Conn, query string, count int64, prepared bool) error {
	var result sql.Result
	var err error
	if prepared {
		stmt, prepareErr := c.PrepareContext(ctx, query)
		if prepareErr != nil {
			return prepareErr
		}
		defer stmt.Close()
		result, err = stmt.ExecContext(ctx)
	} else {
		result, err = c.ExecContext(ctx, query)
	}
	if err != nil {
		return err
	}
	got, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if got != count {
		return fmt.Errorf("%s affected %d want %d", query, got, count)
	}
	return nil
}
func roleDiagnostic(err error, code, message, detail string) error {
	var d *stuntgres.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Detail != detail ||
		d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" || d.Hint != "" ||
		d.Schema != "" || d.Table != "" || d.Column != "" || d.File != "" || d.Routine != "" {
		return fmt.Errorf("diagnostic got %v want %s %s / %s", err, code, message, detail)
	}
	return nil
}
func roleDenied(ctx context.Context, c *sql.Conn, query, code, message, detail string, prepared bool) error {
	return roleDiagnostic(roleCommand(ctx, c, query, 0, prepared), code, message, detail)
}
func roleIdentities(ctx context.Context, c *sql.Conn, want string, held *sql.Stmt) error {
	var rows *sql.Rows
	var err error
	if held != nil {
		rows, err = held.QueryContext(ctx)
	} else {
		rows, err = c.QueryContext(ctx, "SELECT session_user,current_user")
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(columns, []string{"session_user", "current_user"}) || len(types) != 2 {
		return fmt.Errorf("identity labels %v", columns)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != reflect.TypeFor[string]() {
			return fmt.Errorf("identity metadata %s/%v", typ.DatabaseTypeName(), typ.ScanType())
		}
	}
	if !rows.Next() {
		return fmt.Errorf("identity missing: %v", rows.Err())
	}
	var session, current string
	if err := rows.Scan(&session, &current); err != nil {
		return err
	}
	if session != want || current != want {
		return fmt.Errorf("identity %q/%q want %q", session, current, want)
	}
	if rows.Next() {
		return errors.New("identity extra row")
	}
	return rows.Err()
}
func roleSecret(ctx context.Context, c *sql.Conn, held *sql.Stmt) error {
	var rows *sql.Rows
	var err error
	if held != nil {
		rows, err = held.QueryContext(ctx)
	} else {
		rows, err = c.QueryContext(ctx, "SELECT id FROM secret ORDER BY id")
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		return fmt.Errorf("secret metadata %v", types)
	}
	if !rows.Next() {
		return fmt.Errorf("secret missing %v", rows.Err())
	}
	var value int64
	if err := rows.Scan(&value); err != nil {
		return err
	}
	if value != 7 || rows.Next() {
		return fmt.Errorf("secret sentinel %d", value)
	}
	return rows.Err()
}
func roleCreation(ctx context.Context, d *stuntgres.Driver, admin *sql.Conn) error {
	for _, prepared := range []bool{false, true} {
		suffix := "direct"
		if prepared {
			suffix = "prepared"
		}
		for _, query := range []string{
			"CREATE ROLE role_" + suffix, "CREATE USER user_" + suffix,
			"CREATE GROUP group_" + suffix, "CREATE ROLE login_" + suffix + " WITH LOGIN",
			"CREATE USER nolog_" + suffix + " NOLOGIN",
		} {
			if err := roleCommand(ctx, admin, query, 0, prepared); err != nil {
				return err
			}
		}
		for name, login := range map[string]bool{
			"role_" + suffix: false, "user_" + suffix: true, "group_" + suffix: false,
			"login_" + suffix: true, "nolog_" + suffix: false,
		} {
			connector, err := d.OpenConnectorAs(roleDSN, name)
			if err != nil {
				return err
			}
			physical, err := connector.Connect(ctx)
			if login {
				if err != nil {
					return err
				}
				if err := physical.Close(); err != nil {
					return err
				}
			} else if err == nil {
				physical.Close()
				return fmt.Errorf("NOLOGIN %s admitted", name)
			}
		}
		if err := roleDenied(ctx, admin, "CREATE ROLE role_"+suffix, "42710", "role \"role_"+suffix+"\" already exists", "", prepared); err != nil {
			return err
		}
		for _, query := range []string{"DROP ROLE role_" + suffix, "DROP USER user_" + suffix, "DROP GROUP group_" + suffix} {
			if err := roleCommand(ctx, admin, query, 0, prepared); err != nil {
				return err
			}
		}
		if err := roleDenied(ctx, admin, "DROP ROLE role_"+suffix, "42704", "role \"role_"+suffix+"\" does not exist", "", prepared); err != nil {
			return err
		}
	}
	for _, query := range []string{"CREATE USER role_owner", "CREATE USER role_reader", "CREATE ROLE no_login"} {
		if err := roleCommand(ctx, admin, query, 0, false); err != nil {
			return err
		}
	}
	if err := roleDenied(ctx, admin, "CREATE ROLE unsupported WITH SUPERUSER", "0A000", "unsupported role option", "", false); err != nil {
		return err
	}
	return roleDenied(ctx, admin, "DROP ROLE unsupported", "42704", `role "unsupported" does not exist`, "", false)
}
func roleTrusted(ctx context.Context, d *stuntgres.Driver) error {
	ownerConnector, err := d.OpenConnectorAs(roleDSN, "role_owner")
	if err != nil {
		return err
	}
	readerConnector, err := d.OpenConnectorAs(roleDSN, "role_reader")
	if err != nil {
		return err
	}
	ownerPool, readerPool := sql.OpenDB(ownerConnector), sql.OpenDB(readerConnector)
	defer ownerPool.Close()
	defer readerPool.Close()
	ownerPool.SetMaxOpenConns(1)
	owner, err := ownerPool.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { owner.Close() }()
	reader, err := readerPool.Conn(ctx)
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := roleIdentities(ctx, owner, "role_owner", nil); err != nil {
		return err
	}
	if err := roleIdentities(ctx, reader, "role_reader", nil); err != nil {
		return err
	}
	if err := roleDenied(ctx, reader, "SET SESSION AUTHORIZATION role_owner", "42501", `permission denied to set session authorization "role_owner"`, "", true); err != nil {
		return err
	}
	if err := roleDenied(ctx, owner, "SET SESSION AUTHORIZATION role_reader", "42501", `permission denied to set session authorization "role_reader"`, "", false); err != nil {
		return err
	}
	for _, query := range []string{"SET SESSION AUTHORIZATION role_owner", "SET SESSION AUTHORIZATION DEFAULT", "RESET SESSION AUTHORIZATION"} {
		if err := roleCommand(ctx, owner, query, 0, true); err != nil {
			return err
		}
		if err := roleIdentities(ctx, owner, "role_owner", nil); err != nil {
			return err
		}
		if err := roleIdentities(ctx, reader, "role_reader", nil); err != nil {
			return err
		}
	}
	if err := roleSecret(ctx, owner, nil); err != nil {
		return err
	}
	if err := roleDiagnostic(roleSecret(ctx, reader, nil), "42501", "permission denied for table secret", ""); err != nil {
		return err
	}
	if err := owner.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		return fmt.Errorf("replacement signal: %v", err)
	}
	owner.Close()
	owner, err = ownerPool.Conn(ctx)
	if err != nil {
		return err
	}
	if err := roleIdentities(ctx, owner, "role_owner", nil); err != nil {
		return err
	}
	return roleIdentities(ctx, reader, "role_reader", nil)
}
func roleReuse(ctx context.Context, d *stuntgres.Driver) error {
	connector, err := d.OpenConnector(roleDSN)
	if err != nil {
		return err
	}
	pool := sql.OpenDB(connector)
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	c, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { c.Close() }()
	if err := roleCommand(ctx, c, "SET SESSION AUTHORIZATION role_owner", 0, false); err != nil {
		return err
	}
	c.Close()
	c, err = pool.Conn(ctx)
	if err != nil {
		return err
	}
	if err := roleIdentities(ctx, c, "role_owner", nil); err != nil {
		return err
	}
	if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		return err
	}
	c.Close()
	c, err = pool.Conn(ctx)
	if err != nil {
		return err
	}
	return roleIdentities(ctx, c, "postgres", nil)
}
func roleRun() error {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(roleDSN)
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	a, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer b.Close()
	if err := roleCreation(ctx, d, a); err != nil {
		return err
	}
	heldIdentity, err := a.PrepareContext(ctx, "SELECT session_user,current_user")
	if err != nil {
		return err
	}
	defer heldIdentity.Close()
	for _, query := range []string{"SET SESSION AUTHORIZATION no_login", "SET SESSION AUTHORIZATION DEFAULT", "RESET SESSION AUTHORIZATION"} {
		if err := roleCommand(ctx, a, query, 0, false); err != nil {
			return err
		}
		want := "postgres"
		if strings.HasSuffix(query, "no_login") {
			want = "no_login"
		}
		if err := roleIdentities(ctx, a, want, heldIdentity); err != nil {
			return err
		}
		if err := roleIdentities(ctx, b, "postgres", nil); err != nil {
			return err
		}
	}
	for _, prepared := range []bool{false, true} {
		if err := roleCommand(ctx, a, "SELECT current_user", 1, prepared); err != nil {
			return err
		}
	}
	if err := roleCommand(ctx, a, "SET SESSION AUTHORIZATION role_owner", 0, false); err != nil {
		return err
	}
	if err := roleCommand(ctx, b, "SET SESSION AUTHORIZATION role_reader", 0, true); err != nil {
		return err
	}
	if err := roleIdentities(ctx, a, "role_owner", nil); err != nil {
		return err
	}
	if err := roleIdentities(ctx, b, "role_reader", nil); err != nil {
		return err
	}
	if err := roleCommand(ctx, a, "CREATE TABLE secret(id INT)", 0, false); err != nil {
		return err
	}
	if err := roleCommand(ctx, a, "INSERT INTO secret VALUES(7)", 1, true); err != nil {
		return err
	}
	if err := roleSecret(ctx, a, nil); err != nil {
		return err
	}
	for _, query := range []string{"SELECT id FROM secret", "INSERT INTO secret VALUES(9)"} {
		if err := roleDenied(ctx, b, query, "42501", "permission denied for table secret", "", true); err != nil {
			return err
		}
	}
	heldSecret, err := a.PrepareContext(ctx, "SELECT id FROM secret ORDER BY id")
	if err != nil {
		return err
	}
	defer heldSecret.Close()
	if err := roleCommand(ctx, a, "SET SESSION AUTHORIZATION role_reader", 0, false); err != nil {
		return err
	}
	if err := roleDiagnostic(roleSecret(ctx, a, heldSecret), "42501", "permission denied for table secret", ""); err != nil {
		return err
	}
	if err := roleCommand(ctx, a, "SET SESSION AUTHORIZATION role_owner", 0, false); err != nil {
		return err
	}
	if err := roleSecret(ctx, a, heldSecret); err != nil {
		return err
	}
	if err := roleTrusted(ctx, d); err != nil {
		return err
	}
	if err := roleReuse(ctx, d); err != nil {
		return err
	}
	if err := roleCommand(ctx, a, "RESET SESSION AUTHORIZATION", 0, false); err != nil {
		return err
	}
	if err := roleDenied(ctx, a, "DROP ROLE role_owner", "2BP01", `role "role_owner" cannot be dropped because some objects depend on it`, "owner of table secret", false); err != nil {
		return err
	}
	if err := roleCommand(ctx, a, "SET application_name TO retained", 0, false); err != nil {
		return err
	}
	// These controls borrow an ordinary reader session, not an SQL superuser.
	controlConnector, err := d.OpenConnectorAs(roleDSN, "role_reader")
	if err != nil {
		return err
	}
	controls := sql.OpenDB(controlConnector)
	defer controls.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, controls, &dump); err != nil {
		return err
	}
	if !strings.HasPrefix(dump.String(), "SET SESSION AUTHORIZATION 'role_owner';\n") {
		return fmt.Errorf("owner dump %q", dump.String())
	}
	if err := stuntgres.Reset(ctx, controls); err != nil {
		return err
	}
	if err := roleIdentities(ctx, b, "role_reader", nil); err != nil {
		return err
	}
	if err := stuntgres.Restore(ctx, controls, strings.NewReader(dump.String())); err != nil {
		return err
	}
	if err := roleDenied(ctx, a, "DROP ROLE role_owner", "2BP01", `role "role_owner" cannot be dropped because some objects depend on it`, "owner of table secret", true); err != nil {
		return err
	}
	if err := roleCommand(ctx, a, "SET SESSION AUTHORIZATION role_owner", 0, false); err != nil {
		return err
	}
	if err := roleSecret(ctx, a, nil); err != nil {
		return err
	}
	var setting string
	if err := a.QueryRowContext(ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "retained" {
		return fmt.Errorf("control setting %q %v", setting, err)
	}
	if err := roleDiagnostic(stuntgres.Restore(ctx, controls, strings.NewReader("SET SESSION AUTHORIZATION 'absent'; CREATE TABLE bad(id INT);")), "22023", `role "absent" does not exist`, ""); err != nil {
		return err
	}
	if err := roleSecret(ctx, a, nil); err != nil {
		return err
	}
	if err := roleCommand(ctx, a, "RESET SESSION AUTHORIZATION", 0, false); err != nil {
		return err
	}
	if err := stuntgres.Restore(ctx, controls, strings.NewReader("CREATE TABLE legacy(id INT); INSERT INTO legacy VALUES(8);")); err != nil {
		return err
	}
	var value int64
	if err := a.QueryRowContext(ctx, "SELECT id FROM legacy").Scan(&value); err != nil || value != 8 {
		return fmt.Errorf("legacy %d %v", value, err)
	}
	if err := stuntgres.Reset(ctx, controls); err != nil {
		return err
	}
	if err := roleCommand(ctx, b, "RESET SESSION AUTHORIZATION", 0, false); err != nil {
		return err
	}
	if err := roleCommand(ctx, a, "DROP ROLE role_owner", 0, true); err != nil {
		return err
	}
	if err := roleDenied(ctx, a, "DROP ROLE role_owner", "42704", `role "role_owner" does not exist`, "", false); err != nil {
		return err
	}
	isolatedConnector, err := stuntgres.NewDriver().OpenConnector(roleDSN)
	if err != nil {
		return err
	}
	isolated := sql.OpenDB(isolatedConnector)
	defer isolated.Close()
	if err := roleDiagnostic(func() error { _, err := isolated.ExecContext(ctx, "SET SESSION AUTHORIZATION role_reader"); return err }(), "22023", `role "role_reader" does not exist`, ""); err != nil {
		return err
	}
	otherConnector, err := d.OpenConnector(roleDSN + " ")
	if err != nil {
		return err
	}
	other := sql.OpenDB(otherConnector)
	defer other.Close()
	if _, err := other.ExecContext(ctx, "SET SESSION AUTHORIZATION role_reader"); err == nil {
		return errors.New("opaque DSN identity isolation failed")
	}
	return nil
}
