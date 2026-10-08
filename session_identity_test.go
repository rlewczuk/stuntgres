package stuntgres

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSessionIdentityInitialEqualityAndRoleFreshness(t *testing.T) {
	ctx := context.Background()
	t.Run("initial equality", func(t *testing.T) {
		db := rolePool(t, NewDriver(), "identity equality", "postgres")
		var catalog, role, session bool
		if err := db.QueryRowContext(ctx, "SELECT current_catalog=current_database(), current_role=current_user, session_user=user").Scan(&catalog, &role, &session); err != nil {
			t.Fatal(err)
		}
		if !catalog || !role || !session {
			t.Fatalf("initial identity equalities = (%t,%t,%t), want (true,true,true)", catalog, role, session)
		}
	})
	t.Run("prepared role freshness", func(t *testing.T) {
		db := rolePool(t, NewDriver(), "identity freshness", "postgres")
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		stmt, err := conn.PrepareContext(ctx, "SELECT current_user,session_user")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		assertIdentity := func(want string) {
			t.Helper()
			var current, session string
			if err := stmt.QueryRowContext(ctx).Scan(&current, &session); err != nil {
				t.Fatal(err)
			}
			if current != want || session != "postgres" {
				t.Fatalf("identity = (%q,%q), want (%q,postgres)", current, session, want)
			}
		}
		assertIdentity("postgres")
		rolePublicCommand(t, conn, `CREATE ROLE "IdentityRole"`, 0, false)
		rolePublicCommand(t, conn, `SET ROLE "IdentityRole"`, 0, false)
		assertIdentity("IdentityRole")
		rolePublicCommand(t, conn, "RESET ROLE", 0, false)
		assertIdentity("postgres")
	})
}

func TestSessionIdentityFormsMetadataAndDiagnostics(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "identity forms", "postgres")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query := "SELECT current_catalog,current_database(),current_role,current_user,session_user,user,system_user"
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	wantNames := []string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user", "system_user"}
	if err != nil || !reflect.DeepEqual(cols, wantNames) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		want := "NAME"
		if i == 6 {
			want = "TEXT"
		}
		if typ.DatabaseTypeName() != want || typ.ScanType() != reflect.TypeFor[string]() {
			t.Fatal(i, typ)
		}
	}
	var a, b, c, d, e, f string
	var system any
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err := rows.Scan(&a, &b, &c, &d, &e, &f, &system); err != nil {
		t.Fatal(err)
	}
	if a != "identity forms" || b != a || c != "postgres" || d != c || e != c || f != c || system != nil {
		t.Fatal(a, b, c, d, e, f, system)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	for _, q := range []string{`SELECT pg_catalog.current_user()`, `SELECT "current_user"()`, `SELECT pg_catalog.current_database()`, `SELECT "current_database"()`} {
		var value string
		if err := conn.QueryRowContext(ctx, q).Scan(&value); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, tc := range []struct{ q, code, message, pos, hint string }{
		{"SELECT current_user()", "42601", `syntax error at or near "("`, "20", ""},
		{"SELECT current_database(1)", "42883", "function current_database(integer) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT current_database(NULL)", "42883", "function current_database(unknown) does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT pg_catalog.current_role()", "42883", "function pg_catalog.current_role() does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"SET ROLE DEFAULT", "42601", `syntax error at or near "DEFAULT"`, "10", ""},
	} {
		for _, prepare := range []bool{false, true} {
			var err error
			if prepare {
				s, e := conn.PrepareContext(ctx, tc.q)
				err = e
				if s != nil {
					s.Close()
					t.Fatal("unexpected handle", tc.q)
				}
			} else {
				r, e := conn.QueryContext(ctx, tc.q)
				err = e
				if r != nil {
					r.Close()
					t.Fatal("unexpected cursor", tc.q)
				}
			}
			want := &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint}
			var got *Error
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("%s got %#v want %#v", tc.q, err, want)
			}
		}
	}
	rolePublicCommand(t, conn, "CREATE TABLE identity_t(id INT,who TEXT)", 0, false)
	rolePublicCommand(t, conn, "INSERT INTO identity_t VALUES(1,'postgres'),(2,'other')", 2, false)
	var id int
	var who string
	if err := conn.QueryRowContext(ctx, "SELECT id,current_user FROM identity_t WHERE who=current_user ORDER BY id").Scan(&id, &who); err != nil || id != 1 || who != "postgres" {
		t.Fatal(id, who, err)
	}
	empty, err := conn.QueryContext(ctx, "SELECT current_user,system_user FROM identity_t WHERE FALSE")
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	metadata, err := empty.ColumnTypes()
	if err != nil || len(metadata) != 2 || metadata[0].DatabaseTypeName() != "NAME" || metadata[1].DatabaseTypeName() != "TEXT" || empty.Next() {
		t.Fatal(metadata, err)
	}
	empty.Close()
	rolePublicCommand(t, conn, `CREATE TABLE identity_quoted("current_user" TEXT,current_database TEXT)`, 0, false)
	rolePublicCommand(t, conn, "INSERT INTO identity_quoted VALUES('stored user','stored database')", 1, false)
	var quoted, keyword, column, database string
	if err := conn.QueryRowContext(ctx, `SELECT "current_user",CURRENT_USER,current_database,current_database() FROM identity_quoted`).Scan(&quoted, &keyword, &column, &database); err != nil || quoted != "stored user" || keyword != "postgres" || column != "stored database" || database != "identity forms" {
		t.Fatal(quoted, keyword, column, database, err)
	}
	rolePublicCommand(t, conn, "SELECT current_catalog=current_database(),current_role=current_user,session_user=user", 1, true)
}

func TestSessionIdentityDatabaseName(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ dsn, want string }{
		{"", ""}, {"  identity db  ", "  identity db  "}, {"postgres://host/db?user=x", "postgres://host/db?user=x"},
		{strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)},
	} {
		db := rolePool(t, NewDriver(), tc.dsn, "postgres")
		var name string
		if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != tc.want {
			t.Fatal(tc.dsn, name, err)
		}
	}
	driver := NewDriver()
	a := rolePool(t, driver, strings.Repeat("a", 63)+"x", "postgres")
	b := rolePool(t, driver, strings.Repeat("a", 63)+"y", "postgres")
	if _, err := a.ExecContext(ctx, "CREATE TABLE only_a(id INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.QueryContext(ctx, "SELECT id FROM only_a"); err == nil {
		t.Fatal("clipped names merged exact DSN engines")
	}
	bad := rolePool(t, NewDriver(), string([]byte{255}), "postgres")
	if err := bad.PingContext(ctx); err == nil {
		t.Fatal("invalid UTF8 NAME admitted")
	}
	connector, err := NewDriver().OpenConnector("notice identity")
	if err != nil {
		t.Fatal(err)
	}
	copied := connector.(*Connector).WithNoticeHandler(func(Error) {})
	db := sql.OpenDB(copied)
	defer db.Close()
	var name string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "notice identity" {
		t.Fatal(name, err)
	}
}

func TestSessionIdentityDefaultsViewsAndControls(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "identity controls", "postgres")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE ROLE identity_reader", "SET SESSION AUTHORIZATION identity_reader", "CREATE TABLE identity_default(id INT,who TEXT DEFAULT current_user,session_name TEXT DEFAULT session_user)", "CREATE VIEW identity_view AS SELECT current_user AS effective,system_user AS auth FROM identity_default", "RESET SESSION AUTHORIZATION"} {
		rolePublicCommand(t, conn, q, 0, false)
	}
	// Superuser session authority persists after effective selection.
	rolePublicCommand(t, conn, "SET ROLE identity_reader", 0, false)
	var current, session string
	if err := conn.QueryRowContext(ctx, "SELECT current_user,session_user").Scan(&current, &session); err != nil || current != "identity_reader" || session != "postgres" {
		t.Fatal(current, session, err)
	}
	rolePublicCommand(t, conn, "INSERT INTO identity_default(id) VALUES(1)", 1, false)
	held, err := conn.PrepareContext(ctx, "SELECT effective,auth FROM identity_view")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var system any
	if err := held.QueryRowContext(ctx).Scan(&current, &system); err != nil || current != "identity_reader" || system != nil {
		t.Fatal(current, system, err)
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
	if err := Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "SET ROLE") || strings.Contains(dump.String(), "identity controls") {
		t.Fatal("serialized session state", dump.String())
	}
	if err := Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, db, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT who,session_name FROM identity_default").Scan(&current, &session); err != nil || current != "identity_reader" || session != "postgres" {
		t.Fatal(current, session, err)
	}
	if err := held.QueryRowContext(ctx).Scan(&current, &system); err != nil || current != "identity_reader" || system != nil {
		t.Fatal(current, system, err)
	}
	rolePublicCommand(t, conn, "RESET ROLE", 0, false)
	if err := held.QueryRowContext(ctx).Scan(&current, &system); err != nil || current != "postgres" || system != nil {
		t.Fatal(current, system, err)
	}
	old := make([]driver.Value, 3)
	if err := retained.Next(old); err != nil || !reflect.DeepEqual(old, []driver.Value{"identity_reader", "identity controls", nil}) {
		t.Fatal(old, err)
	}
	rolePublicCommand(t, conn, "SET ROLE identity_reader", 0, false)
	rolePublicCommand(t, conn, "INSERT INTO identity_default(id) VALUES(2)", 1, false)
	rows, err := conn.QueryContext(ctx, "SELECT who,session_name FROM identity_default ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if err := rows.Scan(&current, &session); err != nil || current != "identity_reader" || session != "postgres" {
			t.Fatal(current, session, err)
		}
		count++
	}
	if rows.Err() != nil || count != 2 {
		t.Fatal(count, rows.Err())
	}
}

func TestSessionIdentityTrustedAuthorityCancellationAndReplacement(t *testing.T) {
	ctx := context.Background()
	d := NewDriver()
	adminDB := rolePool(t, d, "identity authority", "postgres")
	for _, q := range []string{"CREATE USER identity_a", "CREATE USER identity_b"} {
		if _, err := adminDB.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	aDB := rolePool(t, d, "identity authority", "identity_a")
	aDB.SetMaxOpenConns(1)
	a, err := aDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	rolePublicCommand(t, a, "SET ROLE identity_a", 0, true)
	_, err = a.ExecContext(ctx, "SET ROLE identity_b")
	rolePublicDiagnostic(t, err, "42501", `permission denied to set role "identity_b"`)
	rolePublicIdentity(t, a, "identity_a", nil)
	admin, err := adminDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	rolePublicCommand(t, admin, "SET ROLE identity_a", 0, false)
	rolePublicCommand(t, admin, "SET ROLE identity_b", 0, false)
	var current, session string
	if err := admin.QueryRowContext(ctx, "SELECT current_user,session_user").Scan(&current, &session); err != nil || current != "identity_b" || session != "postgres" {
		t.Fatal(current, session, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, q := range []string{"SET ROLE identity_a", "RESET ROLE"} {
		if _, err := admin.ExecContext(canceled, q); !errors.Is(err, context.Canceled) {
			t.Fatal(q, err)
		}
	}
	if err := admin.QueryRowContext(ctx, "SELECT current_user,session_user").Scan(&current, &session); err != nil || current != "identity_b" || session != "postgres" {
		t.Fatal(current, session, err)
	}
	rolePublicCommand(t, admin, "SET SESSION AUTHORIZATION identity_a", 0, false)
	_, err = admin.ExecContext(ctx, "SET ROLE identity_b")
	rolePublicDiagnostic(t, err, "42501", `permission denied to set role "identity_b"`)
	rolePublicIdentity(t, admin, "identity_a", nil)
	rolePublicCommand(t, admin, "RESET SESSION AUTHORIZATION", 0, false)
	rolePublicIdentity(t, admin, "postgres", nil)
	if err := a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	a.Close()
	a, err = aDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolePublicIdentity(t, a, "identity_a", nil)
	var database string
	if err := a.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil || database != "identity authority" {
		t.Fatal(database, err)
	}
}

func TestSessionIdentityCompleteDiagnosticsAndScalarConsumers(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "identity diagnostics", "postgres")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"CREATE TABLE identity_t(id INT,who TEXT)", "INSERT INTO identity_t VALUES(1,'postgres'),(2,'other')", "CREATE SCHEMA identity_empty"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	functionHint := "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []struct{ q, code, message, pos, hint string }{
		{"SELECT current_database('x')", "42883", "function current_database(unknown) does not exist", "8", functionHint},
		{"SELECT current_database(1::BIGINT)", "42883", "function current_database(bigint) does not exist", "8", functionHint},
		{"SELECT current_database(1,2)", "42883", "function current_database(integer, integer) does not exist", "8", functionHint},
		{"SELECT current_database(VARIADIC 1)", "42883", "function current_database(integer) does not exist", "8", functionHint},
		{"SELECT pg_catalog.current_catalog()", "42883", "function pg_catalog.current_catalog() does not exist", "8", functionHint},
		{"SELECT pg_catalog.user()", "42883", "function pg_catalog.user() does not exist", "8", functionHint},
		{`SELECT "CURRENT_DATABASE"()`, "42883", `function CURRENT_DATABASE() does not exist`, "8", functionHint},
		{"SELECT identity_empty.current_database()", "42883", "function identity_empty.current_database() does not exist", "8", functionHint},
		{`SELECT "current_user" FROM identity_t`, "42703", `column "current_user" does not exist`, "8", ""},
		{"SELECT current_user+1 FROM identity_t", "42883", "operator does not exist: name + integer", "20", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT id FROM identity_t WHERE current_user", "42804", "argument of WHERE must be type boolean, not type name", "33", ""},
		{"/* é */ SELECT current_database(NULL)", "42883", "function current_database(unknown) does not exist", "16", functionHint},
		{"SELECT CAST(current_user AS TEXT)", "0A000", "explicit casts are supported only for int4, int8, text and bool", "8", ""},
	}
	for _, name := range []string{"current_catalog", "current_role", "current_user", "session_user", "user", "system_user"} {
		cases = append(cases, struct{ q, code, message, pos, hint string }{"SELECT " + name + "()", "42601", `syntax error at or near "("`, strconv.Itoa(8 + len(name)), ""})
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				stmt, e := c.PrepareContext(ctx, tc.q)
				err = e
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed Prepare produced handle", tc.q)
				}
			} else {
				rows, e := c.QueryContext(ctx, tc.q)
				err = e
				if rows != nil {
					rows.Close()
					t.Fatal("failed Query produced cursor", tc.q)
				}
			}
			want := &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint}
			var got *Error
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("%s diagnostic %#v want %#v", tc.q, err, want)
			}
		}
	}
	var who string
	if err := c.QueryRowContext(ctx, "SELECT CAST(COALESCE(system_user,'trusted') AS TEXT) FROM identity_t WHERE id=1").Scan(&who); err != nil || who != "trusted" {
		t.Fatal(who, err)
	}
	rolePublicCommand(t, c, "UPDATE identity_t SET who=current_user WHERE id=2", 1, false)
	var id int
	if err := c.QueryRowContext(ctx, "DELETE FROM identity_t WHERE who=current_user RETURNING id,current_user").Scan(&id, &who); err != nil {
		t.Fatal(id, who, err)
	}
}

func TestSessionIdentityRoleSentinelsAndNoPrefixPublication(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "identity sentinels", "postgres")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{`CREATE ROLE "NONE"`, `CREATE ROLE "default"`, "CREATE TABLE identity_sentinel(id INT)"} {
		rolePublicCommand(t, c, q, 0, false)
	}
	for _, q := range []string{`SET ROLE "NONE"`, "SET ROLE 'NONE'"} {
		rolePublicCommand(t, c, q, 0, false)
		var current string
		if err := c.QueryRowContext(ctx, "SELECT current_user").Scan(&current); err != nil || current != "NONE" {
			t.Fatal(q, current, err)
		}
		rolePublicCommand(t, c, `SET ROLE "none"`, 0, false)
		rolePublicIdentity(t, c, "postgres", nil)
	}
	for _, q := range []string{`SET ROLE "default"`, "SET ROLE 'default'"} {
		rolePublicCommand(t, c, q, 0, false)
		var current string
		if err := c.QueryRowContext(ctx, "SELECT current_user").Scan(&current); err != nil || current != "default" {
			t.Fatal(q, current, err)
		}
		rolePublicCommand(t, c, "SET ROLE = DEFAULT", 0, false)
		rolePublicIdentity(t, c, "postgres", nil)
	}
	for _, q := range []string{"INSERT INTO identity_sentinel VALUES(7); SET ROLE NONE garbage", "SET ROLE NONE garbage", "RESET ROLE garbage"} {
		_, err := c.ExecContext(ctx, q)
		var d *Error
		if !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal(q, err)
		}
		rolePublicIdentity(t, c, "postgres", nil)
	}
	rows, err := c.QueryContext(ctx, "SELECT id FROM identity_sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("published prefix", rows.Err())
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if rows, err := c.QueryContext(canceled, "SELECT current_user"); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(rows, err)
	}
	if stmt, err := c.PrepareContext(canceled, "SELECT current_user"); stmt != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(stmt, err)
	}
}

func TestSessionIdentityQuotedViewRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "identity quoted view", "postgres")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{
		"CREATE ROLE identity_quoted_owner",
		"SET SESSION AUTHORIZATION identity_quoted_owner",
		"CREATE TABLE identity_quoted_source(id INT)",
		"INSERT INTO identity_quoted_source VALUES(1)",
		`CREATE VIEW identity_quoted_view AS SELECT "current_user"() AS effective,"session_user"() AS session_name,"system_user"() AS authentication FROM identity_quoted_source`,
		"RESET SESSION AUTHORIZATION",
		"SET ROLE identity_quoted_owner",
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	held, err := conn.PrepareContext(ctx, "SELECT effective,session_name,authentication FROM identity_quoted_view")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	assert := func(want string) {
		t.Helper()
		rows, err := held.QueryContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 3 {
			t.Fatal(types, err)
		}
		for i, typ := range types {
			wantType := "NAME"
			if i == 2 {
				wantType = "TEXT"
			}
			if typ.DatabaseTypeName() != wantType || typ.ScanType() != reflect.TypeFor[string]() {
				t.Fatal(i, typ)
			}
		}
		var effective, session string
		var authentication any
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		if err := rows.Scan(&effective, &session, &authentication); err != nil || effective != want || session != "postgres" || authentication != nil {
			t.Fatal(effective, session, authentication, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	assert("identity_quoted_owner")
	var dump bytes.Buffer
	if err := Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	assert("identity_quoted_owner")
	rolePublicCommand(t, conn, "RESET ROLE", 0, false)
	assert("postgres")
}

func TestSessionIdentityArgumentDiagnosticPrecedence(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "identity argument precedence", "postgres")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE ROLE identity_child_reader", "CREATE SCHEMA identity_hidden", "SET SESSION AUTHORIZATION identity_child_reader", "CREATE TABLE identity_child(id INT)"} {
		rolePublicCommand(t, conn, q, 0, false)
	}
	for _, tc := range []struct{ query, position string }{
		{"SELECT missing_identity_schema.current_database(missing_column) FROM identity_child", "49"},
		{"SELECT identity_hidden.current_database(missing_column) FROM identity_child", "41"},
	} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				stmt, e := conn.PrepareContext(ctx, tc.query)
				err = e
				if stmt != nil {
					stmt.Close()
					t.Fatal("unexpected handle", tc.query)
				}
			} else {
				rows, e := conn.QueryContext(ctx, tc.query)
				err = e
				if rows != nil {
					rows.Close()
					t.Fatal("unexpected cursor", tc.query)
				}
			}
			want := &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing_column" does not exist`, Position: tc.position}
			var got *Error
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("%s prepared=%t got %#v want %#v", tc.query, prepared, err, want)
			}
		}
	}
}

type identityCancelReader struct {
	reader   *strings.Reader
	cancel   context.CancelFunc
	observed bool
}

func (r *identityCancelReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.observed = true
	r.cancel()
	return n, err
}

func TestSessionIdentityFailedAndCanceledControlsPreserveState(t *testing.T) {
	ctx := context.Background()
	db := rolePool(t, NewDriver(), "identity control preservation", "postgres")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE ROLE identity_control_owner", "SET SESSION AUTHORIZATION identity_control_owner", "CREATE TABLE identity_sentinel(id INT,who TEXT DEFAULT current_user)", "INSERT INTO identity_sentinel(id) VALUES(77)", "RESET SESSION AUTHORIZATION", "SET ROLE identity_control_owner"} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	query := "SELECT current_user,current_catalog,session_user,system_user,id,who FROM identity_sentinel"
	var retained driver.Rows
	if err := conn.Raw(func(raw any) error {
		var err error
		retained, err = raw.(driver.QueryerContext).QueryContext(ctx, query, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	assert := func() {
		t.Helper()
		var current, database, session, who string
		var system any
		var id int
		if err := conn.QueryRowContext(ctx, query).Scan(&current, &database, &session, &system, &id, &who); err != nil || current != "identity_control_owner" || database != "identity control preservation" || session != "postgres" || system != nil || id != 77 || who != "identity_control_owner" {
			t.Fatal(current, database, session, system, id, who, err)
		}
	}
	// The first INSERT really stages an identity default before a later row
	// fails; the isolated stage must never replace the sentinel catalog.
	script := "CREATE TABLE identity_replacement(id INT,who TEXT DEFAULT current_user); INSERT INTO identity_replacement(id) VALUES(1); INSERT INTO identity_replacement(id) VALUES(1/0);"
	var diagnostic *Error
	if err := Restore(ctx, db, strings.NewReader(script)); !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatal(err)
	}
	assert()
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	reader := &identityCancelReader{reader: strings.NewReader("CREATE TABLE identity_replacement(id INT);"), cancel: cancel}
	if err := Restore(canceled, db, reader); !reader.observed || !errors.Is(err, context.Canceled) {
		t.Fatal(reader.observed, err)
	}
	assert()
	metadata := retained.(driver.RowsColumnTypeDatabaseTypeName)
	for i, want := range []string{"NAME", "NAME", "NAME", "TEXT", "INT4", "TEXT"} {
		if got := metadata.ColumnTypeDatabaseTypeName(i); got != want {
			t.Fatal(i, got, want)
		}
	}
	values := make([]driver.Value, 6)
	if err := retained.Next(values); err != nil || !reflect.DeepEqual(values, []driver.Value{"identity_control_owner", "identity control preservation", "postgres", nil, int64(77), "identity_control_owner"}) {
		t.Fatal(values, err)
	}
}
