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
func roleSwitchCases(t *testing.T, body func(*membershipScenario)) {
	membershipCases(t, []string{"switch_s", "switch_a", "switch_b", "switch_c", "switch_d"}, func(s *membershipScenario) {
		s.exec("ALTER ROLE switch_s NOINHERIT")
		s.exec("ALTER ROLE switch_a NOLOGIN")
		s.exec("GRANT switch_a,switch_b,switch_c TO switch_s WITH INHERIT FALSE, SET TRUE")
		s.actor("switch_s")
		defer s.c.ExecContext(s.p.Ctx, "ROLLBACK")
		body(s)
	})
}
func switchIdentity(s *membershipScenario, current string) {
	s.t.Helper()
	s.run(fixture.Select("SELECT current_user,current_role,user,session_user", []string{"current_user", "current_role", "user", "session_user"}, []string{"NAME", "NAME", "NAME", "NAME"}, [][]any{{current, current, current, "switch_s"}}))
}
func switchDenied(s *membershipScenario, role string) {
	s.fail("SET ROLE "+role, "42501", "permission denied to set role \""+role+"\"", "")
}
func switchAdmin(s *membershipScenario, q string) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, s.backend.DB, s.record, fixture.Exec(q, 0))
}

func TestReferenceRoleSwitchIdentityAuthority(t *testing.T) {
	roleSwitchCases(t, func(s *membershipScenario) {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "switch_s", func() { s.exec("CREATE TABLE switch_login_owned(id INT)") })
		s.run(fixture.Exec("INSERT INTO switch_login_owned VALUES(7)", 1))
		s.exec("SET ROLE switch_b")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "switch_b", func() { s.exec("CREATE TABLE switch_target_owned(id INT)") })
		s.run(fixture.Exec("INSERT INTO switch_target_owned VALUES(7)", 1))
		switchIdentity(s, "switch_b")
		s.run(fixture.Select("SELECT id FROM switch_target_owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		denied := fixture.RoleReferenceError("SELECT id FROM switch_login_owned", "42501", "permission denied for table switch_login_owned", "", s.prepared, "execute")
		denied.SelectRows = true
		s.run(denied)
		s.exec("SET ROLE switch_a")
		s.exec("SET ROLE switch_b") // Eligibility comes from session_user, not switch_a.
		switchAdmin(s, "GRANT switch_d TO switch_a WITH INHERIT FALSE, SET TRUE")
		s.exec("SET ROLE switch_a")
		switchAdmin(s, "GRANT switch_a TO switch_s WITH SET FALSE")
		switchDenied(s, "switch_d")
		switchAdmin(s, "GRANT switch_b TO switch_a WITH INHERIT TRUE, SET FALSE")
		s.run(fixture.Select("SELECT id FROM switch_target_owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		peer, err := s.backend.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{s.bootstrap}}))
		s.exec("SET ROLE NONE")
		switchIdentity(s, "switch_s")
		s.run(fixture.Select("SELECT id FROM switch_login_owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func TestReferenceRoleSwitchEligibility(t *testing.T) {
	roleSwitchCases(t, func(s *membershipScenario) {
		switchAdmin(s, "REVOKE switch_b FROM switch_s")
		switchAdmin(s, "GRANT switch_b TO switch_a WITH ADMIN FALSE, INHERIT FALSE, SET TRUE")
		s.exec("SET ROLE switch_b")
		switchIdentity(s, "switch_b")
		switchAdmin(s, "GRANT switch_b TO switch_a WITH ADMIN TRUE, INHERIT TRUE, SET FALSE")
		switchDenied(s, "switch_b")
		switchAdmin(s, "GRANT switch_b TO switch_a WITH SET TRUE")
		switchAdmin(s, "GRANT switch_a TO switch_s WITH ADMIN TRUE, INHERIT TRUE, SET FALSE")
		switchDenied(s, "switch_b")
		switchAdmin(s, "GRANT switch_b TO switch_c WITH INHERIT FALSE, SET TRUE")
		s.exec("SET ROLE switch_b")
		s.exec("SET ROLE switch_s")
		switchIdentity(s, "switch_s")
		stmt, err := s.c.PrepareContext(s.p.Ctx, "SET ROLE switch_d")
		if err != nil {
			s.t.Fatal(err)
		}
		defer stmt.Close()
		switchIdentity(s, "switch_s")
		switchAdmin(s, "GRANT switch_d TO switch_s WITH SET TRUE")
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: "SET ROLE switch_d"}, s.record, fixture.Exec("SET ROLE switch_d", 0))
		switchAdmin(s, "GRANT switch_d TO switch_s WITH SET FALSE")
		_, err = stmt.ExecContext(s.p.Ctx)
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied to set role \"switch_d\"")
		fixture.AssertDiagnostic(s.t, err, &want)
		s.actor("")
		s.exec("SET ROLE switch_a") // Session superuser, NOLOGIN target.
		s.actor("switch_s")
		switchIdentity(s, "switch_s")
	})
}

func TestReferenceRoleSwitchLifetime(t *testing.T) {
	roleSwitchCases(t, func(s *membershipScenario) {
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
				s.exec("SET ROLE NONE")
				s.exec("BEGIN")
				for _, q := range tc.commands {
					s.exec(q)
				}
				switchIdentity(s, tc.inside)
				s.exec(end)
				want := "switch_s"
				if end == "COMMIT" {
					want = tc.commit
				}
				switchIdentity(s, want)
			}
		}
		s.exec("SET ROLE NONE")
		s.exec("BEGIN READ ONLY")
		s.exec("SET LOCAL ROLE switch_a")
		s.exec("RESET ROLE")
		s.exec("COMMIT")
		switchIdentity(s, "switch_s")
		tx, err := s.c.BeginTx(s.p.Ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			s.t.Fatal(err)
		}
		defer tx.Rollback()
		step := fixture.Exec("SET LOCAL ROLE switch_b", 0)
		step.Prepared = s.prepared
		fixture.RunStep(s.t, s.p.Ctx, tx, s.record, step)
		fixture.RunStep(s.t, s.p.Ctx, tx, s.record, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"switch_b"}}))
		if err := tx.Commit(); err != nil {
			s.t.Fatal(err)
		}
		switchIdentity(s, "switch_s")
		s.exec("BEGIN")
		s.exec("SET ROLE switch_a")
		s.exec("COMMIT AND CHAIN")
		switchIdentity(s, "switch_a")
		s.exec("SET LOCAL ROLE switch_b")
		s.exec("ROLLBACK AND CHAIN")
		switchIdentity(s, "switch_a")
		s.exec("ROLLBACK")
	})
}

func TestReferenceRoleSwitchSavepoints(t *testing.T) {
	roleSwitchCases(t, func(s *membershipScenario) {
		s.exec("SET application_name=entry")
		s.exec("BEGIN")
		s.exec("SET ROLE switch_a")
		s.exec("SET LOCAL ROLE switch_b")
		s.exec("SET application_name=session_value")
		s.exec("SET LOCAL application_name=local_value")
		s.exec("SAVEPOINT x")
		s.exec("SET ROLE switch_c")
		s.exec("SET application_name=discarded")
		s.exec("SAVEPOINT y")
		s.exec("SET LOCAL ROLE switch_a")
		s.exec("ROLLBACK TO x")
		switchIdentity(s, "switch_b")
		s.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"local_value"}}))
		s.exec("SET ROLE switch_c")
		s.exec("ROLLBACK TO x")
		switchIdentity(s, "switch_b")
		s.fail("SET ROLE switch_missing", "22023", "role \"switch_missing\" does not exist", "")
		aborted := fixture.RoleReferenceError("SET ROLE switch_a", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", s.prepared, "execute")
		if s.prepared {
			aborted.ErrorStage = "prepare"
		}
		s.run(aborted)
		s.exec("ROLLBACK TO x")
		switchIdentity(s, "switch_b")
		s.exec("RELEASE x")
		switchIdentity(s, "switch_b")
		s.exec("COMMIT")
		switchIdentity(s, "switch_a")
		s.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"session_value"}}))
		s.exec("BEGIN")
		s.exec("SET ROLE switch_c")
		s.fail("SET ROLE switch_missing", "22023", "role \"switch_missing\" does not exist", "")
		s.exec("ROLLBACK")
		switchIdentity(s, "switch_a")
	})
}

func TestReferenceRoleSwitchDiagnosticsAndNotices(t *testing.T) {
	roleSwitchCases(t, func(s *membershipScenario) {
		for _, q := range []string{"SET LOCAL ROLE switch_a", "SET LOCAL ROLE switch_missing", "SET LOCAL ROLE switch_d"} {
			stmt, err := s.c.PrepareContext(s.p.Ctx, q)
			if err != nil {
				s.t.Fatal(err)
			}
			stmt.Close()
			if len(*s.notices) != 0 {
				s.t.Fatal("Prepare emitted notices", *s.notices)
			}
			switchIdentity(s, "switch_s")
			switch q {
			case "SET LOCAL ROLE switch_a":
				s.exec(q)
			case "SET LOCAL ROLE switch_missing":
				s.fail(q, "22023", "role \"switch_missing\" does not exist", "")
			default:
				s.fail(q, "42501", "permission denied to set role \"switch_d\"", "")
			}
			s.notice("WARNING", "25P01", "SET LOCAL can only be used in transaction blocks")
			switchIdentity(s, "switch_s")
		}
		s.exec("SET client_min_messages=error")
		s.exec("SET LOCAL ROLE switch_a")
		if len(*s.notices) != 0 {
			s.t.Fatal(*s.notices)
		}
		s.exec("RESET client_min_messages")
		for _, q := range []string{"SET ROLE none", "SET ROLE 'none'", "SET ROLE \"none\"", "SET ROLE TO DEFAULT", "SET ROLE = DEFAULT", "SET SESSION ROLE TO DEFAULT"} {
			s.exec("SET ROLE switch_a")
			s.exec(q)
			switchIdentity(s, "switch_s")
		}
		switchAdmin(s, `CREATE ROLE "NONE"`)
		switchAdmin(s, `CREATE ROLE "default"`)
		defer switchAdmin(s, `DROP ROLE "NONE","default"`)
		switchAdmin(s, `GRANT "NONE","default" TO switch_s WITH INHERIT FALSE, SET TRUE`)
		for _, q := range []string{`SET ROLE "NONE"`, `SET ROLE 'NONE'`} {
			s.exec(q)
			switchIdentity(s, "NONE")
		}
		for _, q := range []string{`SET ROLE "default"`, `SET ROLE 'default'`} {
			s.exec(q)
			switchIdentity(s, "default")
		}
		s.exec("SET ROLE NONE")
		for _, tc := range []struct{ q, pos string }{{"SET ROLE DEFAULT", "10"}, {"SET SESSION ROLE DEFAULT", "18"}, {"SET LOCAL ROLE DEFAULT", "16"}, {"SET LOCAL ROLE switch_a trailing", "25"}} {
			want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+map[bool]string{true: "trailing", false: "DEFAULT"}[strings.Contains(tc.q, "trailing")]+`"`)
			want.Position = tc.pos
			step := fixture.ErrorDiagnostic(tc.q, want)
			step.Prepared = s.prepared
			if s.prepared {
				step.ErrorStage = "prepare"
			}
			s.run(step)
			switchIdentity(s, "switch_s")
		}
		q := "SET ROLE switch_a; SET LOCAL ROLE switch_b trailing"
		want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "trailing"`)
		want.Position = "44"
		step := fixture.ErrorDiagnostic(q, want)
		if s.prepared {
			step.ErrorStage = "prepare"
		}
		s.run(step)
		switchIdentity(s, "switch_s")
	})
}

func TestReferenceRoleSwitchCachedRestoration(t *testing.T) {
	roleSwitchCases(t, func(s *membershipScenario) {
		for _, kind := range []string{"commit", "savepoint", "rollback"} {
			switchAdmin(s, "GRANT switch_a TO switch_s WITH SET TRUE")
			s.exec("SET ROLE switch_a")
			s.exec("BEGIN")
			s.exec("SAVEPOINT x")
			s.exec("SET LOCAL ROLE switch_b")
			switchAdmin(s, "GRANT switch_a TO switch_s WITH SET FALSE")
			if kind == "savepoint" {
				s.exec("ROLLBACK TO x")
				switchIdentity(s, "switch_a")
				s.exec("COMMIT")
			} else if kind == "commit" {
				s.exec("COMMIT")
			} else {
				s.exec("ROLLBACK")
			}
			switchIdentity(s, "switch_a")
			switchDenied(s, "switch_a")
			switchIdentity(s, "switch_a")
			s.exec("SET ROLE NONE")
		}
	})
}

func TestReferenceRoleSwitchConfiguredReset(t *testing.T) {
	roleSwitchCases(t, func(s *membershipScenario) {
		s.exec("SET ROLE switch_b")
		s.exec("SET ROLE NONE")
		switchIdentity(s, "switch_s")
		s.exec("SET ROLE switch_b")
		s.exec("RESET ROLE")
		switchIdentity(s, "switch_s")
		if !s.backend.PG {
			return
		} // Native public connectors have no configured-role option.
		switchAdmin(s, "ALTER ROLE switch_s PASSWORD 'role-core-fixture'")
		u, err := url.Parse(fixture.DSN)
		if err != nil {
			s.t.Fatal(err)
		}
		u.User = url.UserPassword("switch_s", "role-core-fixture")
		values := u.Query()
		values.Set("options", "-c role=switch_a")
		u.RawQuery = values.Encode()
		connector, err := pq.NewConnector(u.String())
		if err != nil {
			s.t.Fatal(err)
		}
		db, record := fixture.InsertSelectRecordedPool(s.t, connector)
		record.RawBackend = fixture.RawPostgres
		defer db.Close()
		c, err := db.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer c.Close()
		configured := *s
		configured.c = c
		configured.record = record
		switchIdentity(&configured, "switch_a")
		configured.exec("SET ROLE switch_b")
		configured.exec("SET ROLE NONE")
		switchIdentity(&configured, "switch_s")
		configured.exec("RESET ROLE")
		switchIdentity(&configured, "switch_a")
		switchAdmin(s, "GRANT switch_a TO switch_s WITH SET FALSE")
		configured.exec("SET ROLE NONE")
		configured.exec("RESET ROLE")
		switchIdentity(&configured, "switch_a")
		switchDenied(&configured, "switch_a")
	})
}

func TestReferenceRoleSwitchLifecycle(t *testing.T) {
	roleSwitchCases(t, func(s *membershipScenario) {
		fixture.PreparedReferenceCleanup(s.t, s.backend, "role_switch_lifecycle")
		s.exec("BEGIN")
		s.exec("SET ROLE switch_a")
		s.exec("SET LOCAL ROLE switch_b")
		s.exec("PREPARE TRANSACTION 'role_switch_lifecycle'")
		switchIdentity(s, "switch_a")
		switchAdmin(s, "COMMIT PREPARED 'role_switch_lifecycle'")
		switchIdentity(s, "switch_a")
		s.exec("RESET ROLE")
		stmt, err := s.c.PrepareContext(s.p.Ctx, "SELECT current_user,session_user")
		if err != nil {
			s.t.Fatal(err)
		}
		defer stmt.Close()
		rows, err := stmt.QueryContext(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		if !rows.Next() {
			s.t.Fatal("missing snapshot")
		}
		var old, session string
		if err := rows.Scan(&old, &session); err != nil {
			s.t.Fatal(err)
		}
		rows.Close()
		held := fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: "SELECT current_user,session_user"}
		for _, q := range []string{"SET ROLE switch_a", "BEGIN", "SET LOCAL ROLE switch_b", "ROLLBACK", "RESET ROLE"} {
			s.exec(q)
			want := "switch_a"
			if q == "SET LOCAL ROLE switch_b" {
				want = "switch_b"
			}
			if q == "RESET ROLE" {
				want = "switch_s"
			}
			fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Select(held.SQL, []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{want, "switch_s"}}))
		}
		if old != "switch_s" || session != "switch_s" {
			s.t.Fatal(old, session)
		}
		if !s.backend.PG {
			// Pre-canceled context preservation is a native adapter guarantee, not a
			// portable pq guarantee: pq may finish and asynchronously retire its owner.
			ctx, cancel := context.WithCancel(s.p.Ctx)
			cancel()
			for _, q := range []string{"SET ROLE switch_a", "SET LOCAL ROLE switch_b", "RESET ROLE"} {
				_, err := s.c.ExecContext(ctx, q)
				if !errors.Is(err, context.Canceled) {
					s.t.Fatal(err)
				}
				switchIdentity(s, "switch_s")
			}
		}
		if !s.backend.PG {
			s.exec("SET ROLE switch_a")
			var dump strings.Builder
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
				s.t.Fatal(err)
			}
			switchIdentity(s, "switch_a")
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String())); err != nil {
				s.t.Fatal(err)
			}
			switchIdentity(s, "switch_a")
		}
		peer, err := s.backend.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		if err := s.c.Close(); err != nil {
			s.t.Fatal(err)
		}
		fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{s.bootstrap}}))
	})
}

func TestReferenceRoleSwitchFeatureSmoke(t *testing.T) {
	roleSwitchCases(t, func(s *membershipScenario) {
		s.exec("SET ROLE switch_a")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "switch_a", func() { s.exec("CREATE TABLE switch_smoke_owned(id INT)") })
		s.run(fixture.Exec("INSERT INTO switch_smoke_owned VALUES(7)", 1))
		s.run(fixture.Select("SELECT id FROM switch_smoke_owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		// Querying a utility independently checks its empty rows/columns/types.
		s.run(fixture.Select("SET ROLE NONE", []string{}, []string{}, [][]any{}))
		s.exec("SET SESSION ROLE switch_a")
		switchIdentity(s, "switch_a")
		s.exec("BEGIN")
		s.exec("SET LOCAL ROLE switch_b")
		s.exec("SAVEPOINT x")
		s.exec("SET ROLE switch_a")
		s.exec("ROLLBACK TO x")
		switchIdentity(s, "switch_b")
		s.exec("RELEASE x")
		s.exec("COMMIT")
		switchIdentity(s, "switch_a")
		s.exec("SET ROLE NONE")
		s.exec("RESET ROLE")
		switchIdentity(s, "switch_s")
		switchDenied(s, "switch_d")
		s.exec("SET LOCAL ROLE switch_a")
		s.notice("WARNING", "25P01", "SET LOCAL can only be used in transaction blocks")
		switchIdentity(s, "switch_s")
	})
}
