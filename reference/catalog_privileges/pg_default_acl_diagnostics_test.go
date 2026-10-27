package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDefaultACLDiagnostics(t *testing.T) {
	pgDefaultACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT missing FROM pg_catalog.pg_default_acl", "42703", `column "missing" does not exist`, "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_default_acl", "42703", `column "missing" does not exist`, "14"},
			{"SELECT oid FROM pg_catalog.pg_default_acl_missing", "42P01", `relation "pg_catalog.pg_default_acl_missing" does not exist`, "17"},
			{"SELECT 'bad'::oid FROM pg_default_acl WHERE FALSE", "22P02", `invalid input syntax for type oid: "bad"`, "8"},
			{"SELECT '4294967296'::oid FROM pg_default_acl WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`, "8"},
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
		q := "SELECT * FROM pg_default_acl WHERE oid=$1"
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
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDefaultACLSelect(q, b.PG, nil, int64(0)))
		pgDefaultACLDenial(t, p, b)
		if b.PG {
			return
		}
		for _, tc := range []struct{ q, stage string }{
			{"INSERT INTO pg_default_acl(defaclrole) VALUES(10)", "execute"},
			{"UPDATE pg_default_acl SET defaclrole=10 WHERE FALSE", "prepare"},
			{"DELETE FROM pg_default_acl WHERE FALSE", "execute"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = tc.stage
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM pg_default_acl WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	})
}

func pgDefaultACLDenial(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgDefaultACLRoles(t, p, b, "dacl18_denied_observer")
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer func() {
		for _, q := range []string{"RESET ROLE", "REVOKE SELECT(oid) ON pg_catalog.pg_default_acl FROM PUBLIC", "GRANT SELECT ON pg_catalog.pg_default_acl TO PUBLIC"} {
			if _, err := conn.ExecContext(p.Ctx, q); err != nil {
				t.Error("restore catalog ACL", q, err)
			}
		}
	}()
	const q = "SELECT * FROM pg_catalog.pg_default_acl WHERE FALSE"
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
	acl := func(command string) {
		for _, q := range []string{"RESET ROLE", command, "SET ROLE dacl18_denied_observer"} {
			run(fixture.Exec(q, 0))
		}
	}
	deny := func(q string) fixture.Step {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_default_acl"})
		s.SelectRows, s.ErrorStage = true, "execute"
		return s
	}
	acl("REVOKE SELECT ON pg_catalog.pg_default_acl FROM PUBLIC")
	run(deny(q))
	run(fixture.Prepared(deny(q)))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, deny(q))
	acl("GRANT SELECT(oid) ON pg_catalog.pg_default_acl TO PUBLIC")
	run(fixture.Select("SELECT oid FROM pg_default_acl WHERE FALSE", []string{"oid"}, []string{"OID"}, nil))
	run(deny(q))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, deny(q))
	acl("GRANT SELECT ON pg_catalog.pg_default_acl TO PUBLIC")
	fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDefaultACLSelect(q, b.PG, nil))
}
