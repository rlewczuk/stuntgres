package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRolePreparedSetupSourceConsumers(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"owner", "reader"})
	p := fixture.NewScenario(t)
	if p.Postgres != nil {
		if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			for _, q := range []string{"CREATE USER owner", "CREATE ROLE reader", "SET SESSION AUTHORIZATION owner", "CREATE TABLE secret(id INT)", "INSERT INTO secret VALUES(7)"} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 1
				}
				roleScenarioCommand(t, a, q, count, true)
			}
			identity, err := a.PrepareContext(p.Ctx, "SELECT session_user,current_user,session_user")
			if err != nil {
				t.Fatal(err)
			}
			defer identity.Close()
			table, err := a.PrepareContext(p.Ctx, "SELECT id FROM secret")
			if err != nil {
				t.Fatal(err)
			}
			defer table.Close()
			roleScenarioCommand(t, b, "SET SESSION AUTHORIZATION reader", 0, false)
			roleScenarioIdentity(t, b, "reader", nil, backend.PG)
			roleScenarioCommand(t, a, "SET SESSION AUTHORIZATION reader", 0, false)
			roleScenarioIdentity(t, a, "reader", identity, backend.PG)
			var value int64
			err = table.QueryRowContext(p.Ctx).Scan(&value)
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table secret")
			fixture.AssertDiagnostic(t, err, &want)
			roleScenarioCommand(t, a, "SET SESSION AUTHORIZATION owner", 0, false)
			if err := table.QueryRowContext(p.Ctx).Scan(&value); err != nil || value != 7 {
				t.Fatal(value, err)
			}
			roleScenarioIdentity(t, b, "reader", nil, backend.PG)
			roleScenarioCommand(t, a, "RESET SESSION AUTHORIZATION", 0, false)
			bootstrap := "postgres"
			if backend.PG {
				bootstrap = "stuntgres"
			}
			roleScenarioIdentity(t, a, bootstrap, identity, backend.PG)
			roleScenarioCommand(t, a, "SELECT current_user", 1, true)
			roleScenarioCommand(t, b, "RESET SESSION AUTHORIZATION", 0, false)
		})
	}
}

func TestReferenceRoleInputPrecedenceSourceConsumers(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"reader"})
	p := fixture.NewScenario(t)
	// The fixture recreates public without its initdb PUBLIC USAGE grant.
	// This witness tests input versus table ACL, not namespace invisibility.
	if p.Postgres != nil {
		if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"CREATE ROLE reader", "CREATE TABLE secret(id INT)", "INSERT INTO secret VALUES(7)", "SET SESSION AUTHORIZATION reader"} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 1
				}
				roleScenarioCommand(t, c, q, count, false)
			}
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					q, code, message string
					args             []any
				}{
					{"INSERT INTO secret VALUES($1)", "22P02", `invalid input syntax for type integer: "bad"`, []any{"bad"}},
					{"INSERT INTO secret VALUES($1)", "42501", "permission denied for table secret", []any{"9"}},
					{"INSERT INTO secret VALUES(2147483648)", "22003", "integer out of range", nil},
				} {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					if tc.code == "22P02" {
						want.Context = "unnamed portal parameter $1 = '...'"
					}
					s := fixture.ErrorDiagnostic(tc.q, want, tc.args...)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
				}
			}
			roleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, false)
			var value int64
			if err := c.QueryRowContext(p.Ctx, "SELECT id FROM secret").Scan(&value); err != nil || value != 7 {
				t.Fatal("denied inputs changed own sentinel", value, err)
			}
		})
	}
}
