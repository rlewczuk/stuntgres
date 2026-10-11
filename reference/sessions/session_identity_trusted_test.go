package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionIdentityTrustedAuthorityCancellationAndReplacement(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"identity_a", "identity_b"})
	p := fixture.NewScenario(t)
	dsn := "reference/" + t.Name()
	for _, q := range []string{"CREATE USER identity_a", "CREATE USER identity_b"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "ALTER ROLE identity_a PASSWORD 'role-core-fixture'"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			bootstrap, database := "postgres", dsn
			database = database[:min(len(database), 63)]
			var auth any
			if backend.PG {
				bootstrap, database, auth = "stuntgres", "stuntgres_reference", "scram-sha-256:identity_a"
			}
			aDB := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), backend.PG, dsn, "identity_a")
			aDB.SetMaxOpenConns(1)
			a, err := aDB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { a.Close() }()
			identity := func(c *sql.Conn, current, session string, system any) {
				sessionIdentityAssertLogin(t, p.Ctx, c, backend.PG, current, session, system)
			}
			stmt, err := a.PrepareContext(p.Ctx, "SET ROLE identity_a")
			if err != nil {
				t.Fatal(err)
			}
			r, err := stmt.ExecContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 0 {
				t.Fatal(n, err)
			}
			stmt.Close()
			if _, err := r.LastInsertId(); err == nil {
				t.Fatal("LastInsertId unexpectedly supported")
			}
			_, err = a.ExecContext(p.Ctx, "SET ROLE identity_b")
			want := fixture.ControlsReferenceDiagnostic("42501", `permission denied to set role "identity_b"`)
			fixture.AssertDiagnostic(t, err, &want)
			identity(a, "identity_a", "identity_a", auth)
			admin, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			defer admin.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, backend.Recorder, s) }
			run(fixture.Exec("SET ROLE identity_a", 0))
			run(fixture.Exec("SET ROLE identity_b", 0))
			heldIdentity, err := admin.PrepareContext(p.Ctx, "SELECT current_user,session_user")
			if err != nil {
				t.Fatal(err)
			}
			defer heldIdentity.Close()
			var current, session string
			assert := func() {
				if err := admin.QueryRowContext(p.Ctx, "SELECT current_user,session_user").Scan(&current, &session); err != nil || current != "identity_b" || session != bootstrap {
					t.Fatal(current, session, err)
				}
				if err := heldIdentity.QueryRowContext(p.Ctx).Scan(&current, &session); err != nil || current != "identity_b" || session != bootstrap {
					t.Fatal("held identity after cancellation", current, session, err)
				}
			}
			assert()
			if !backend.PG {
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, q := range []string{"SET ROLE identity_a", "RESET ROLE"} {
					if result, err := admin.ExecContext(canceled, q); result != nil || !errors.Is(err, context.Canceled) {
						t.Fatal(q, result, err)
					}
					assert()
				}
			} else {
				for _, prepared := range []bool{false, true} {
					fixture.SessionCancel(t, p.Ctx, admin, prepared)
					assert()
				}
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION identity_a", 0))
			_, err = admin.ExecContext(p.Ctx, "SET ROLE identity_b")
			fixture.AssertDiagnostic(t, err, &want)
			adminAuth := any(nil)
			if backend.PG {
				adminAuth = "scram-sha-256:stuntgres"
			}
			identity(admin, "identity_a", "identity_a", adminAuth)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			identity(admin, bootstrap, bootstrap, adminAuth)
			if err := a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			a.Close()
			a, err = aDB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			identity(a, "identity_a", "identity_a", auth)
			var name string
			if err := a.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&name); err != nil || name != database {
				t.Fatal(name, err)
			}
		})
	}
}

// sessionIdentityAssertLogin preserves the original repeated NAME columns,
// raw codec representations, typed scalar consumers and authenticated owner.
func sessionIdentityAssertLogin(t *testing.T, ctx context.Context, c *sql.Conn, postgres bool, current, session string, system any) {
	t.Helper()
	rows, err := c.QueryContext(ctx, "SELECT session_user,current_user,session_user")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{"session_user", "current_user", "session_user"}) {
		t.Fatal(cols, err)
	}
	fixture.SessionIdentityMetadata(postgres)(t, rows)
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "NAME" {
			t.Fatal(typ)
		}
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var rawA, rawB, rawC any
	if err := rows.Scan(&rawA, &rawB, &rawC); err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{rawA, rawB, rawC} {
		fixture.AssertRawValue(t, postgres, "NAME", v)
	}
	var x, y, z string
	if err := rows.Scan(&x, &y, &z); err != nil || x != session || y != current || z != session {
		t.Fatal(x, y, z, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var authentication any
	if err := c.QueryRowContext(ctx, "SELECT system_user").Scan(&authentication); err != nil || fixture.Normalize(authentication) != system {
		t.Fatal(authentication, err)
	}
}
