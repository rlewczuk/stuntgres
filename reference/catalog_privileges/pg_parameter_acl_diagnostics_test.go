package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGParameterACLVisibilityAndDiagnostics(t *testing.T) {
	pgParameterACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgParameterACLRoles(t, p, b, "pacl18_diagnostic_observer", "pacl18_diagnostic_reader")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("GRANT SET ON PARAMETER pacl18_diagnostic.value TO pacl18_diagnostic_reader", 0))
		oid := pgParameterACLOID(t, p, b, "pacl18_diagnostic.value")
		owner := pgParameterACLOwner(b)
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("SET ROLE pacl18_diagnostic_observer", 0))
		visible := pgParameterACLSelect("SELECT * FROM pg_parameter_acl WHERE parname='pacl18_diagnostic.value'", b.PG, [][]any{{oid, "pacl18_diagnostic.value", "{" + owner + "=sA/" + owner + ",pacl18_diagnostic_reader=s/" + owner + "}"}})
		run(visible)
		run(fixture.Prepared(visible))
		visibleStmt, err := conn.PrepareContext(p.Ctx, visible.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer visibleStmt.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: visibleStmt, SQL: visible.Query}, b.Recorder, visible)
		run(fixture.Exec("RESET ROLE", 0))
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT missing FROM pg_catalog.pg_parameter_acl", "42703", `column "missing" does not exist`, "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_parameter_acl", "42703", `column "missing" does not exist`, "14"},
			{"SELECT oid FROM pg_catalog.pg_parameter_acl_missing", "42P01", `relation "pg_catalog.pg_parameter_acl_missing" does not exist`, "17"},
			{"SELECT 'bad'::oid FROM pg_parameter_acl WHERE FALSE", "22P02", `invalid input syntax for type oid: "bad"`, "8"},
			{"SELECT '4294967296'::oid FROM pg_parameter_acl WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`, "8"},
		} {
			for _, plain := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows, s.NonContext, s.Prepared, s.ErrorStage = true, plain, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
		q := "SELECT * FROM pg_parameter_acl WHERE oid=$1"
		held := fixture.ViewHold(t, p, b, q)
		for _, tc := range []struct{ value, code, message string }{
			{"bad", "22P02", `invalid input syntax for type oid: "bad"`},
			{"", "22P02", `invalid input syntax for type oid: ""`},
			{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
		} {
			portal := "unnamed portal parameter $1 = '...'"
			if tc.value == "" {
				portal = "unnamed portal parameter $1 = ''"
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: portal}, tc.value)
			s.SelectRows, s.ErrorStage = true, "execute"
			for _, plain := range []bool{false, true} {
				s.NonContext = plain
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			}
			s.NonContext = false
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgParameterACLSelect(q, b.PG, nil, int64(0)))
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(canceled, q, int64(0))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query published Rows")
		}
		pgDBRoleSettingCanceled(t, err)
		stmt, err := b.DB.PrepareContext(canceled, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("canceled prepare published statement")
		}
		pgDBRoleSettingCanceled(t, err)
		rows, err = held.Statement.QueryContext(canceled, int64(0))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled held query published Rows")
		}
		pgDBRoleSettingCanceled(t, err)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgParameterACLSelect(q, b.PG, nil, int64(0)))
		pgParameterACLDenial(t, p, b)
		if b.PG {
			return
		}
		// Native unsupported writes/locks are boundaries, not PostgreSQL prohibitions.
		for _, tc := range []struct{ q, stage string }{
			{"INSERT INTO pg_parameter_acl(parname,paracl) VALUES('pacl18_forbidden.value',NULL)", "execute"},
			{"UPDATE pg_parameter_acl SET parname='pacl18_forbidden.value' WHERE FALSE", "prepare"},
			{"DELETE FROM pg_parameter_acl WHERE FALSE", "execute"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = tc.stage
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, pgParameterACLSelect(q, false, nil, int64(0)))
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM pg_parameter_acl WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	})
}

func pgParameterACLDenial(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer func() {
		for _, q := range []string{"RESET ROLE", "REVOKE SELECT(oid) ON pg_catalog.pg_parameter_acl FROM PUBLIC", "GRANT SELECT ON pg_catalog.pg_parameter_acl TO PUBLIC"} {
			if _, err := conn.ExecContext(p.Ctx, q); err != nil {
				t.Error("restore catalog ACL", q, err)
			}
		}
	}()
	const columnQuery = "SELECT oid FROM pg_catalog.pg_parameter_acl WHERE FALSE"
	const starQuery = "SELECT * FROM pg_catalog.pg_parameter_acl WHERE FALSE"
	columnStmt, err := conn.PrepareContext(p.Ctx, columnQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer columnStmt.Close()
	starStmt, err := conn.PrepareContext(p.Ctx, starQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer starStmt.Close()
	columnHeld := fixture.HeldStatement{Executor: conn, Statement: columnStmt, SQL: columnQuery}
	starHeld := fixture.HeldStatement{Executor: conn, Statement: starStmt, SQL: starQuery}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
	acl := func(command string) {
		for _, q := range []string{"RESET ROLE", command, "SET ROLE pacl18_diagnostic_observer"} {
			run(fixture.Exec(q, 0))
		}
	}
	deny := func(q string) fixture.Step {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_parameter_acl"})
		s.SelectRows, s.ErrorStage = true, "execute"
		return s
	}
	acl("REVOKE SELECT ON pg_catalog.pg_parameter_acl FROM PUBLIC")
	for _, q := range []string{starQuery, columnQuery, "SELECT parname FROM pg_parameter_acl", "SELECT paracl FROM pg_parameter_acl LIMIT 0", "SELECT * FROM pg_parameter_acl WHERE parname='pacl18_denied.absent'"} {
		run(deny(q))
	}
	fixture.RunStep(t, p.Ctx, starHeld, b.Recorder, deny(starQuery))
	fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, deny(columnQuery))
	acl("GRANT SELECT(oid) ON pg_catalog.pg_parameter_acl TO PUBLIC")
	empty := fixture.Select(columnQuery, []string{"oid"}, []string{"OID"}, nil)
	run(empty)
	run(fixture.Prepared(empty))
	fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, empty)
	run(deny(starQuery))
	run(deny("SELECT parname FROM pg_parameter_acl WHERE FALSE"))
	acl("REVOKE SELECT(oid) ON pg_catalog.pg_parameter_acl FROM PUBLIC")
	fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, deny(columnQuery))
	acl("GRANT SELECT(oid) ON pg_catalog.pg_parameter_acl TO PUBLIC")
	fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, empty)
	acl("GRANT SELECT ON pg_catalog.pg_parameter_acl TO PUBLIC")
	fixture.RunStep(t, p.Ctx, starHeld, b.Recorder, pgParameterACLSelect(starQuery, b.PG, nil))
}
