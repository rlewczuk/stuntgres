package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceRolePreparedSetupSourceConsumers(t *testing.T) {
	roleReferenceCleanup(t, []string{"owner", "reader"})
	p := newReferenceScenario(t)
	if p.postgres != nil {
		if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.db.Conn(p.ctx)
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
			identity, err := a.PrepareContext(p.ctx, "SELECT session_user,current_user,session_user")
			if err != nil {
				t.Fatal(err)
			}
			defer identity.Close()
			table, err := a.PrepareContext(p.ctx, "SELECT id FROM secret")
			if err != nil {
				t.Fatal(err)
			}
			defer table.Close()
			roleScenarioCommand(t, b, "SET SESSION AUTHORIZATION reader", 0, false)
			roleScenarioIdentity(t, b, "reader", nil, backend.pg)
			roleScenarioCommand(t, a, "SET SESSION AUTHORIZATION reader", 0, false)
			roleScenarioIdentity(t, a, "reader", identity, backend.pg)
			var value int64
			err = table.QueryRowContext(p.ctx).Scan(&value)
			want := controlsReferenceDiagnostic("42501", "permission denied for table secret")
			referenceAssertDiagnostic(t, err, &want)
			roleScenarioCommand(t, a, "SET SESSION AUTHORIZATION owner", 0, false)
			if err := table.QueryRowContext(p.ctx).Scan(&value); err != nil || value != 7 {
				t.Fatal(value, err)
			}
			roleScenarioIdentity(t, b, "reader", nil, backend.pg)
			roleScenarioCommand(t, a, "RESET SESSION AUTHORIZATION", 0, false)
			bootstrap := "postgres"
			if backend.pg {
				bootstrap = "stuntgres"
			}
			roleScenarioIdentity(t, a, bootstrap, identity, backend.pg)
			roleScenarioCommand(t, a, "SELECT current_user", 1, true)
			roleScenarioCommand(t, b, "RESET SESSION AUTHORIZATION", 0, false)
		})
	}
}

func TestReferenceRoleInputPrecedenceSourceConsumers(t *testing.T) {
	roleReferenceCleanup(t, []string{"reader"})
	p := newReferenceScenario(t)
	// The fixture recreates public without its initdb PUBLIC USAGE grant.
	// This witness tests input versus table ACL, not namespace invisibility.
	if p.postgres != nil {
		if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
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
					want := controlsReferenceDiagnostic(tc.code, tc.message)
					if tc.code == "22P02" {
						want.Context = "unnamed portal parameter $1 = '...'"
					}
					s := referenceErrorDiagnostic(tc.q, want, tc.args...)
					s.prepared = prepared
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, backend.recorder, s)
				}
			}
			roleScenarioCommand(t, c, "RESET SESSION AUTHORIZATION", 0, false)
			var value int64
			if err := c.QueryRowContext(p.ctx, "SELECT id FROM secret").Scan(&value); err != nil || value != 7 {
				t.Fatal("denied inputs changed own sentinel", value, err)
			}
		})
	}
}
