package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/lib/pq"
	"net/url"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// The shared body runs identically on both backends and through both routes.
func roleSwitchCases(t *testing.T, body func(*fixture.RoleMembershipScenario)) {
	fixture.RoleMembershipCases(t, []string{"switch_s", "switch_a", "switch_b", "switch_c", "switch_d"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("ALTER ROLE switch_s NOINHERIT")
		s.Exec("ALTER ROLE switch_a NOLOGIN")
		s.Exec("GRANT switch_a,switch_b,switch_c TO switch_s WITH INHERIT FALSE, SET TRUE")
		s.Actor("switch_s")
		defer s.C.ExecContext(s.P.Ctx, "ROLLBACK")
		body(s)
	})
}
func switchIdentity(s *fixture.RoleMembershipScenario, current string) {
	s.T.Helper()
	s.Run(fixture.Select("SELECT current_user,current_role,user,session_user", []string{"current_user", "current_role", "user", "session_user"}, []string{"NAME", "NAME", "NAME", "NAME"}, [][]any{{current, current, current, "switch_s"}}))
}
func switchDenied(s *fixture.RoleMembershipScenario, role string) {
	s.Fail("SET ROLE "+role, "42501", "permission denied to set role \""+role+"\"", "")
}
func switchAdmin(s *fixture.RoleMembershipScenario, q string) {
	s.T.Helper()
	fixture.RunStep(s.T, s.P.Ctx, s.Backend.DB, s.Record, fixture.Exec(q, 0))
}

func TestReferenceRoleSwitchIdentityAuthority(t *testing.T) {
	roleSwitchCases(t, func(s *fixture.RoleMembershipScenario) {
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "switch_s", func() { s.Exec("CREATE TABLE switch_login_owned(id INT)") })
		s.Run(fixture.Exec("INSERT INTO switch_login_owned VALUES(7)", 1))
		s.Exec("SET ROLE switch_b")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "switch_b", func() { s.Exec("CREATE TABLE switch_target_owned(id INT)") })
		s.Run(fixture.Exec("INSERT INTO switch_target_owned VALUES(7)", 1))
		switchIdentity(s, "switch_b")
		s.Run(fixture.Select("SELECT id FROM switch_target_owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		denied := fixture.RoleReferenceError("SELECT id FROM switch_login_owned", "42501", "permission denied for table switch_login_owned", "", s.Prepared, "execute")
		denied.SelectRows = true
		s.Run(denied)
		s.Exec("SET ROLE switch_a")
		s.Exec("SET ROLE switch_b") // Eligibility comes from session_user, not switch_a.
		switchAdmin(s, "GRANT switch_d TO switch_a WITH INHERIT FALSE, SET TRUE")
		s.Exec("SET ROLE switch_a")
		switchAdmin(s, "GRANT switch_a TO switch_s WITH SET FALSE")
		switchDenied(s, "switch_d")
		switchAdmin(s, "GRANT switch_b TO switch_a WITH INHERIT TRUE, SET FALSE")
		s.Run(fixture.Select("SELECT id FROM switch_target_owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		peer, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{s.Bootstrap}}))
		s.Exec("SET ROLE NONE")
		switchIdentity(s, "switch_s")
		s.Run(fixture.Select("SELECT id FROM switch_login_owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func TestReferenceRoleSwitchEligibility(t *testing.T) {
	roleSwitchCases(t, func(s *fixture.RoleMembershipScenario) {
		switchAdmin(s, "REVOKE switch_b FROM switch_s")
		switchAdmin(s, "GRANT switch_b TO switch_a WITH ADMIN FALSE, INHERIT FALSE, SET TRUE")
		s.Exec("SET ROLE switch_b")
		switchIdentity(s, "switch_b")
		switchAdmin(s, "GRANT switch_b TO switch_a WITH ADMIN TRUE, INHERIT TRUE, SET FALSE")
		switchDenied(s, "switch_b")
		switchAdmin(s, "GRANT switch_b TO switch_a WITH SET TRUE")
		switchAdmin(s, "GRANT switch_a TO switch_s WITH ADMIN TRUE, INHERIT TRUE, SET FALSE")
		switchDenied(s, "switch_b")
		switchAdmin(s, "GRANT switch_b TO switch_c WITH INHERIT FALSE, SET TRUE")
		s.Exec("SET ROLE switch_b")
		s.Exec("SET ROLE switch_s")
		switchIdentity(s, "switch_s")
		stmt, err := s.C.PrepareContext(s.P.Ctx, "SET ROLE switch_d")
		if err != nil {
			s.T.Fatal(err)
		}
		defer stmt.Close()
		switchIdentity(s, "switch_s")
		switchAdmin(s, "GRANT switch_d TO switch_s WITH SET TRUE")
		fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: "SET ROLE switch_d"}, s.Record, fixture.Exec("SET ROLE switch_d", 0))
		switchAdmin(s, "GRANT switch_d TO switch_s WITH SET FALSE")
		_, err = stmt.ExecContext(s.P.Ctx)
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied to set role \"switch_d\"")
		fixture.AssertDiagnostic(s.T, err, &want)
		s.Actor("")
		s.Exec("SET ROLE switch_a") // Session superuser, NOLOGIN target.
		s.Actor("switch_s")
		switchIdentity(s, "switch_s")
	})
}

func TestReferenceRoleSwitchLifetime(t *testing.T) {
	roleSwitchCases(t, func(s *fixture.RoleMembershipScenario) {
		cases := []struct {
			commands       []string
			inside, commit string
		}{
			{[]string{"SET SESSION ROLE switch_a"}, "switch_a", "switch_a"},
			{[]string{"SET LOCAL ROLE switch_a"}, "switch_a", "switch_s"},
			{[]string{"SET SESSION ROLE switch_a", "SET LOCAL ROLE switch_b"}, "switch_b", "switch_a"},
			{[]string{"SET LOCAL ROLE switch_a", "SET SESSION ROLE switch_b"}, "switch_b", "switch_b"},
			{[]string{"SET ROLE switch_a", "SET LOCAL ROLE switch_b", "SET LOCAL ROLE switch_c"}, "switch_c", "switch_a"},
			{[]string{"SET LOCAL ROLE switch_a", "SET ROLE switch_b", "SET LOCAL ROLE switch_c", "SET ROLE switch_a"}, "switch_a", "switch_a"},
			{[]string{"SET ROLE switch_a", "SET LOCAL ROLE TO DEFAULT"}, "switch_s", "switch_a"},
			{[]string{"SET ROLE switch_a", "SET LOCAL ROLE = DEFAULT"}, "switch_s", "switch_a"},
			{[]string{"SET LOCAL ROLE switch_a", "SET ROLE NONE"}, "switch_s", "switch_s"},
			{[]string{"SET LOCAL ROLE switch_a", "RESET ROLE"}, "switch_s", "switch_s"},
		}
		for _, tc := range cases {
			for _, end := range []string{"COMMIT", "ROLLBACK"} {
				s.Exec("SET ROLE NONE")
				s.Exec("BEGIN")
				for _, q := range tc.commands {
					s.Exec(q)
				}
				switchIdentity(s, tc.inside)
				s.Exec(end)
				want := "switch_s"
				if end == "COMMIT" {
					want = tc.commit
				}
				switchIdentity(s, want)
			}
		}
		s.Exec("SET ROLE NONE")
		s.Exec("BEGIN READ ONLY")
		s.Exec("SET LOCAL ROLE switch_a")
		s.Exec("RESET ROLE")
		s.Exec("COMMIT")
		switchIdentity(s, "switch_s")
		tx, err := s.C.BeginTx(s.P.Ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			s.T.Fatal(err)
		}
		defer tx.Rollback()
		step := fixture.Exec("SET LOCAL ROLE switch_b", 0)
		step.Prepared = s.Prepared
		fixture.RunStep(s.T, s.P.Ctx, tx, s.Record, step)
		fixture.RunStep(s.T, s.P.Ctx, tx, s.Record, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"switch_b"}}))
		if err := tx.Commit(); err != nil {
			s.T.Fatal(err)
		}
		switchIdentity(s, "switch_s")
		s.Exec("BEGIN")
		s.Exec("SET ROLE switch_a")
		s.Exec("COMMIT AND CHAIN")
		switchIdentity(s, "switch_a")
		s.Exec("SET LOCAL ROLE switch_b")
		s.Exec("ROLLBACK AND CHAIN")
		switchIdentity(s, "switch_a")
		s.Exec("ROLLBACK")
	})
}

func TestReferenceRoleSwitchSavepoints(t *testing.T) {
	roleSwitchCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("SET application_name=entry")
		s.Exec("BEGIN")
		s.Exec("SET ROLE switch_a")
		s.Exec("SET LOCAL ROLE switch_b")
		s.Exec("SET application_name=session_value")
		s.Exec("SET LOCAL application_name=local_value")
		s.Exec("SAVEPOINT x")
		s.Exec("SET ROLE switch_c")
		s.Exec("SET application_name=discarded")
		s.Exec("SAVEPOINT y")
		s.Exec("SET LOCAL ROLE switch_a")
		s.Exec("ROLLBACK TO x")
		switchIdentity(s, "switch_b")
		s.Run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"local_value"}}))
		s.Exec("SET ROLE switch_c")
		s.Exec("ROLLBACK TO x")
		switchIdentity(s, "switch_b")
		s.Fail("SET ROLE switch_missing", "22023", "role \"switch_missing\" does not exist", "")
		aborted := fixture.RoleReferenceError("SET ROLE switch_a", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", s.Prepared, "execute")
		if s.Prepared {
			aborted.ErrorStage = "prepare"
		}
		s.Run(aborted)
		s.Exec("ROLLBACK TO x")
		switchIdentity(s, "switch_b")
		s.Exec("RELEASE x")
		switchIdentity(s, "switch_b")
		s.Exec("COMMIT")
		switchIdentity(s, "switch_a")
		s.Run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"session_value"}}))
		s.Exec("BEGIN")
		s.Exec("SET ROLE switch_c")
		s.Fail("SET ROLE switch_missing", "22023", "role \"switch_missing\" does not exist", "")
		s.Exec("ROLLBACK")
		switchIdentity(s, "switch_a")
	})
}

func TestReferenceRoleSwitchDiagnosticsAndNotices(t *testing.T) {
	roleSwitchCases(t, func(s *fixture.RoleMembershipScenario) {
		for _, q := range []string{"SET LOCAL ROLE switch_a", "SET LOCAL ROLE switch_missing", "SET LOCAL ROLE switch_d"} {
			stmt, err := s.C.PrepareContext(s.P.Ctx, q)
			if err != nil {
				s.T.Fatal(err)
			}
			stmt.Close()
			if len(*s.Notices) != 0 {
				s.T.Fatal("Prepare emitted notices", *s.Notices)
			}
			switchIdentity(s, "switch_s")
			switch q {
			case "SET LOCAL ROLE switch_a":
				s.Exec(q)
			case "SET LOCAL ROLE switch_missing":
				s.Fail(q, "22023", "role \"switch_missing\" does not exist", "")
			default:
				s.Fail(q, "42501", "permission denied to set role \"switch_d\"", "")
			}
			s.Notice("WARNING", "25P01", "SET LOCAL can only be used in transaction blocks")
			switchIdentity(s, "switch_s")
		}
		s.Exec("SET client_min_messages=error")
		s.Exec("SET LOCAL ROLE switch_a")
		if len(*s.Notices) != 0 {
			s.T.Fatal(*s.Notices)
		}
		s.Exec("RESET client_min_messages")
		for _, q := range []string{"SET ROLE none", "SET ROLE 'none'", "SET ROLE \"none\"", "SET ROLE TO DEFAULT", "SET ROLE = DEFAULT", "SET SESSION ROLE TO DEFAULT"} {
			s.Exec("SET ROLE switch_a")
			s.Exec(q)
			switchIdentity(s, "switch_s")
		}
		switchAdmin(s, `CREATE ROLE "NONE"`)
		switchAdmin(s, `CREATE ROLE "default"`)
		defer switchAdmin(s, `DROP ROLE "NONE","default"`)
		switchAdmin(s, `GRANT "NONE","default" TO switch_s WITH INHERIT FALSE, SET TRUE`)
		for _, q := range []string{`SET ROLE "NONE"`, `SET ROLE 'NONE'`} {
			s.Exec(q)
			switchIdentity(s, "NONE")
		}
		for _, q := range []string{`SET ROLE "default"`, `SET ROLE 'default'`} {
			s.Exec(q)
			switchIdentity(s, "default")
		}
		s.Exec("SET ROLE NONE")
		for _, tc := range []struct{ q, pos string }{{"SET ROLE DEFAULT", "10"}, {"SET SESSION ROLE DEFAULT", "18"}, {"SET LOCAL ROLE DEFAULT", "16"}, {"SET LOCAL ROLE switch_a trailing", "25"}} {
			want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+map[bool]string{true: "trailing", false: "DEFAULT"}[strings.Contains(tc.q, "trailing")]+`"`)
			want.Position = tc.pos
			step := fixture.ErrorDiagnostic(tc.q, want)
			step.Prepared = s.Prepared
			if s.Prepared {
				step.ErrorStage = "prepare"
			}
			s.Run(step)
			switchIdentity(s, "switch_s")
		}
		q := "SET ROLE switch_a; SET LOCAL ROLE switch_b trailing"
		want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "trailing"`)
		want.Position = "44"
		step := fixture.ErrorDiagnostic(q, want)
		if s.Prepared {
			step.ErrorStage = "prepare"
		}
		s.Run(step)
		switchIdentity(s, "switch_s")
	})
}

func TestReferenceRoleSwitchCachedRestoration(t *testing.T) {
	roleSwitchCases(t, func(s *fixture.RoleMembershipScenario) {
		for _, kind := range []string{"commit", "savepoint", "rollback"} {
			switchAdmin(s, "GRANT switch_a TO switch_s WITH SET TRUE")
			s.Exec("SET ROLE switch_a")
			s.Exec("BEGIN")
			s.Exec("SAVEPOINT x")
			s.Exec("SET LOCAL ROLE switch_b")
			switchAdmin(s, "GRANT switch_a TO switch_s WITH SET FALSE")
			if kind == "savepoint" {
				s.Exec("ROLLBACK TO x")
				switchIdentity(s, "switch_a")
				s.Exec("COMMIT")
			} else if kind == "commit" {
				s.Exec("COMMIT")
			} else {
				s.Exec("ROLLBACK")
			}
			switchIdentity(s, "switch_a")
			switchDenied(s, "switch_a")
			switchIdentity(s, "switch_a")
			s.Exec("SET ROLE NONE")
		}
	})
}

func TestReferenceRoleSwitchConfiguredReset(t *testing.T) {
	roleSwitchCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("SET ROLE switch_b")
		s.Exec("SET ROLE NONE")
		switchIdentity(s, "switch_s")
		s.Exec("SET ROLE switch_b")
		s.Exec("RESET ROLE")
		switchIdentity(s, "switch_s")
		if !s.Backend.PG {
			return
		} // Native public connectors have no configured-role option.
		switchAdmin(s, "ALTER ROLE switch_s PASSWORD 'role-core-fixture'")
		u, err := url.Parse(fixture.DSN)
		if err != nil {
			s.T.Fatal(err)
		}
		u.User = url.UserPassword("switch_s", "role-core-fixture")
		values := u.Query()
		values.Set("options", "-c role=switch_a")
		u.RawQuery = values.Encode()
		connector, err := pq.NewConnector(u.String())
		if err != nil {
			s.T.Fatal(err)
		}
		db, record := fixture.InsertSelectRecordedPool(s.T, connector)
		record.RawBackend = fixture.RawPostgres
		defer db.Close()
		c, err := db.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer c.Close()
		configured := *s
		configured.C = c
		configured.Record = record
		switchIdentity(&configured, "switch_a")
		configured.Exec("SET ROLE switch_b")
		configured.Exec("SET ROLE NONE")
		switchIdentity(&configured, "switch_s")
		configured.Exec("RESET ROLE")
		switchIdentity(&configured, "switch_a")
		switchAdmin(s, "GRANT switch_a TO switch_s WITH SET FALSE")
		configured.Exec("SET ROLE NONE")
		configured.Exec("RESET ROLE")
		switchIdentity(&configured, "switch_a")
		switchDenied(&configured, "switch_a")
	})
}

func TestReferenceRoleSwitchLifecycle(t *testing.T) {
	roleSwitchCases(t, func(s *fixture.RoleMembershipScenario) {
		fixture.PreparedReferenceCleanup(s.T, s.Backend, "role_switch_lifecycle")
		s.Exec("BEGIN")
		s.Exec("SET ROLE switch_a")
		s.Exec("SET LOCAL ROLE switch_b")
		s.Exec("PREPARE TRANSACTION 'role_switch_lifecycle'")
		switchIdentity(s, "switch_a")
		switchAdmin(s, "COMMIT PREPARED 'role_switch_lifecycle'")
		switchIdentity(s, "switch_a")
		s.Exec("RESET ROLE")
		stmt, err := s.C.PrepareContext(s.P.Ctx, "SELECT current_user,session_user")
		if err != nil {
			s.T.Fatal(err)
		}
		defer stmt.Close()
		rows, err := stmt.QueryContext(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		if !rows.Next() {
			s.T.Fatal("missing snapshot")
		}
		var old, session string
		if err := rows.Scan(&old, &session); err != nil {
			s.T.Fatal(err)
		}
		rows.Close()
		held := fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: "SELECT current_user,session_user"}
		for _, q := range []string{"SET ROLE switch_a", "BEGIN", "SET LOCAL ROLE switch_b", "ROLLBACK", "RESET ROLE"} {
			s.Exec(q)
			want := "switch_a"
			if q == "SET LOCAL ROLE switch_b" {
				want = "switch_b"
			}
			if q == "RESET ROLE" {
				want = "switch_s"
			}
			fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Select(held.SQL, []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{want, "switch_s"}}))
		}
		if old != "switch_s" || session != "switch_s" {
			s.T.Fatal(old, session)
		}
		if !s.Backend.PG {
			// Pre-canceled context preservation is a native adapter guarantee, not a
			// portable pq guarantee: pq may finish and asynchronously retire its owner.
			ctx, cancel := context.WithCancel(s.P.Ctx)
			cancel()
			for _, q := range []string{"SET ROLE switch_a", "SET LOCAL ROLE switch_b", "RESET ROLE"} {
				_, err := s.C.ExecContext(ctx, q)
				if !errors.Is(err, context.Canceled) {
					s.T.Fatal(err)
				}
				switchIdentity(s, "switch_s")
			}
		}
		if !s.Backend.PG {
			s.Exec("SET ROLE switch_a")
			var dump strings.Builder
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			switchIdentity(s, "switch_a")
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump.String())); err != nil {
				s.T.Fatal(err)
			}
			switchIdentity(s, "switch_a")
		}
		peer, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer peer.Close()
		if err := s.C.Close(); err != nil {
			s.T.Fatal(err)
		}
		fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{s.Bootstrap}}))
	})
}

func TestReferenceRoleSwitchFeatureSmoke(t *testing.T) {
	roleSwitchCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("SET ROLE switch_a")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "switch_a", func() { s.Exec("CREATE TABLE switch_smoke_owned(id INT)") })
		s.Run(fixture.Exec("INSERT INTO switch_smoke_owned VALUES(7)", 1))
		s.Run(fixture.Select("SELECT id FROM switch_smoke_owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		// Querying a utility independently checks its empty rows/columns/types.
		s.Run(fixture.Select("SET ROLE NONE", []string{}, []string{}, [][]any{}))
		s.Exec("SET SESSION ROLE switch_a")
		switchIdentity(s, "switch_a")
		s.Exec("BEGIN")
		s.Exec("SET LOCAL ROLE switch_b")
		s.Exec("SAVEPOINT x")
		s.Exec("SET ROLE switch_a")
		s.Exec("ROLLBACK TO x")
		switchIdentity(s, "switch_b")
		s.Exec("RELEASE x")
		s.Exec("COMMIT")
		switchIdentity(s, "switch_a")
		s.Exec("SET ROLE NONE")
		s.Exec("RESET ROLE")
		switchIdentity(s, "switch_s")
		switchDenied(s, "switch_d")
		s.Exec("SET LOCAL ROLE switch_a")
		s.Notice("WARNING", "25P01", "SET LOCAL can only be used in transaction blocks")
		switchIdentity(s, "switch_s")
	})
}
