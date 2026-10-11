package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func roleScenarioNameScan(pg bool) reflect.Type {
	if pg {
		return reflect.TypeFor[any]()
	}
	return reflect.TypeFor[string]()
}
func roleScenarioCommand(t *testing.T, c *sql.Conn, q string, count int64, prepared bool) {
	t.Helper()
	var r sql.Result
	var err error
	if prepared {
		s, e := c.PrepareContext(context.Background(), q)
		if e != nil {
			t.Fatal(q, e)
		}
		defer s.Close()
		r, err = s.ExecContext(context.Background())
	} else {
		r, err = c.ExecContext(context.Background(), q)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	if n, e := r.RowsAffected(); e != nil || n != count {
		t.Fatal(q, n, e)
	}
	if _, e := r.LastInsertId(); e == nil {
		t.Fatal("LastInsertId")
	}
}
func roleScenarioIdentity(t *testing.T, c *sql.Conn, want string, held *sql.Stmt, pg bool) {
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
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"session_user", "current_user", "session_user"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != roleScenarioNameScan(pg) {
			t.Fatal(typ)
		}
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var a, b, cName string
	if err := rows.Scan(&a, &b, &cName); err != nil || a != want || b != want || cName != want {
		t.Fatal(a, b, cName, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceRoleRetainedIdentityRowsAcrossSwitchAndControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"original", "later"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			t.Run(backend.Name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				ctx := p.Ctx
				db := backend.DB
				bootstrap := "postgres"
				if backend.PG {
					bootstrap = "stuntgres"
					if _, err := fixture.Admin.ExecContext(ctx, "GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC"); err != nil {
						t.Fatal(err)
					}
				}
				c, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				for _, query := range []string{"CREATE USER original", "CREATE USER later", "SET SESSION AUTHORIZATION original", "CREATE TABLE sentinel(id INT)", "INSERT INTO sentinel VALUES(7)"} {
					count := int64(0)
					if query == "INSERT INTO sentinel VALUES(7)" {
						count = 1
					}
					roleScenarioCommand(t, c, query, count, prepared)
				}
				peer, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				roleScenarioCommand(t, peer, "SET application_name TO 'peer-retained'", 0, false)
				roleScenarioCommand(t, c, "SET application_name TO 'producer-retained'", 0, false)
				checkSessions := func() {
					t.Helper()
					roleScenarioIdentity(t, peer, bootstrap, nil, backend.PG)
					for _, tc := range []struct {
						conn *sql.Conn
						want string
					}{{peer, "peer-retained"}, {c, "producer-retained"}} {
						var value string
						if err := tc.conn.QueryRowContext(ctx, "SHOW application_name").Scan(&value); err != nil || value != tc.want {
							t.Fatal("settings changed", value, err)
						}
					}
				}
				var held *sql.Stmt
				if prepared {
					held, err = c.PrepareContext(ctx, "SELECT session_user,current_user")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				capture := func() *sql.Rows {
					t.Helper()
					var rows *sql.Rows
					var err error
					if held == nil {
						rows, err = c.QueryContext(ctx, "SELECT session_user,current_user")
					} else {
						rows, err = held.QueryContext(ctx)
					}
					if err != nil {
						t.Fatal(err)
					}
					return rows
				}
				check := func(rows *sql.Rows, want string) { roleScenarioIdentitySnapshot(t, rows, want, backend.PG) }
				// pq must drain each cursor before another command on its transport.
				if backend.PG {
					roleScenarioPostgresIdentityTransitions(t, c, prepared, bootstrap, capture, check, checkSessions)
					return
				}
				var dump bytes.Buffer
				if !backend.PG {
					if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
				}
				// Separate snapshots witness each boundary; the final one spans all
				// transitions before its first Next/Scan, not just a copied Go value.
				all := capture()
				defer all.Close()
				atSwitch := capture()
				defer atSwitch.Close()
				roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION later", 0, prepared)
				check(atSwitch, "original")
				check(capture(), "later")
				checkSessions()
				roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION original", 0, prepared)
				atReset := capture()
				defer atReset.Close()
				roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION later", 0, prepared)
				if !backend.PG {
					if err := stuntgres.Reset(ctx, p.Native); err != nil {
						t.Fatal(err)
					}
				}
				check(atReset, "original")
				check(capture(), "later")
				checkSessions()
				roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION original", 0, prepared)
				atRestore := capture()
				defer atRestore.Close()
				roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION later", 0, prepared)
				if !backend.PG {
					if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
				}
				check(atRestore, "original")
				check(all, "original")
				check(capture(), "later")
				checkSessions()
				roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION original", 0, prepared)
				var value int64
				if err := c.QueryRowContext(ctx, "SELECT id FROM sentinel").Scan(&value); err != nil || value != 7 {
					t.Fatal("restored sentinel", value, err)
				}
				roleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, prepared)
				check(capture(), bootstrap)
				checkSessions()
				roleScenarioCommand(t, c, "DROP TABLE sentinel", 0, false)
				roleScenarioCommand(t, c, "DROP ROLE original,later", 0, false)
			})
		}
	}
}

func roleScenarioPostgresIdentityTransitions(t *testing.T, c *sql.Conn, prepared bool, bootstrap string, capture func() *sql.Rows, check func(*sql.Rows, string), checkSessions func()) {
	t.Helper()
	check(capture(), "original")
	checkSessions()
	for _, name := range []string{"later", "original", "later", "original"} {
		roleScenarioCommand(t, c, "SET SESSION AUTHORIZATION "+name, 0, prepared)
		check(capture(), name)
		checkSessions()
	}
	var value int64
	if err := c.QueryRowContext(context.Background(), "SELECT id FROM sentinel").Scan(&value); err != nil || value != 7 {
		t.Fatal(value, err)
	}
	roleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, prepared)
	check(capture(), bootstrap)
	checkSessions()
	roleScenarioCommand(t, c, "DROP TABLE sentinel", 0, false)
	roleScenarioCommand(t, c, "DROP ROLE original,later", 0, false)
}

func roleScenarioIdentitySnapshot(t *testing.T, rows *sql.Rows, want string, pg bool) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"session_user", "current_user"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal(types, err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != roleScenarioNameScan(pg) {
			t.Fatal("NAME metadata", typ)
		}
	}
	if !rows.Next() {
		t.Fatal("identity missing", rows.Err())
	}
	var session, current string
	if err := rows.Scan(&session, &current); err != nil || session != want || current != want {
		t.Fatal(session, current, want, err)
	}
	// Preserve the original string consumer and separately inspect raw cells.
	var a, b any
	if err := rows.Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []any{a, b} {
		if pg {
			bytes, ok := raw.([]byte)
			if !ok || string(bytes) != want {
				t.Fatal("pq NAME export", raw)
			}
		} else {
			if raw != want {
				t.Fatal("native NAME export", raw)
			}
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("identity extra/error", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
