package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func predefinedMissingFunction(s *fixture.RoleMembershipScenario, q, signature string) {
	s.T.Helper()
	if !s.Backend.PG {
		predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: "functions are unsupported", Position: "8"}, true, "prepare")
		return
	}
	predefinedFailure(s, q, fixture.Diagnostic{Code: "42883", Message: "function " + signature + " does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true, "prepare")
}

func predefinedNativeBoundaries(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	for _, q := range []string{"CHECKPOINT", "CLUSTER t"} {
		predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"}, false, "prepare")
	}
	for _, q := range []string{"COPY t FROM '/nonexistent'", "COPY t TO '/nonexistent'"} {
		predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: "COPY server files are unsupported"}, false, "prepare")
	}
	predefinedFailure(s, "COPY t TO PROGRAM 'false'", fixture.Diagnostic{Code: "0A000", Message: "COPY PROGRAM is unsupported"}, false, "prepare")
	predefinedFailure(s, "CREATE SUBSCRIPTION pdef_sub CONNECTION 'host=invalid' PUBLICATION pub WITH(connect=false,slot_name=NONE)", fixture.Diagnostic{Code: "0A000", Message: "CREATE syntax is unsupported", Position: "8"}, false, "prepare")
	for _, tc := range []struct{ q, signature string }{
		{"SELECT pg_read_file('/nonexistent')", "pg_read_file(unknown)"},
		{"SELECT pg_cancel_backend(2147483647)", "pg_cancel_backend(integer)"},
		{"SELECT pg_terminate_backend(2147483647)", "pg_terminate_backend(integer)"},
	} {
		predefinedMissingFunction(s, tc.q, tc.signature)
	}
	predefinedFailure(s, "SELECT query FROM pg_stat_activity", fixture.Diagnostic{Code: "42P01", Message: `relation "pg_stat_activity" does not exist`, Position: "19"}, true, "prepare")
	predefinedSelect(s, "SHOW reserved_connections", []string{"reserved_connections"}, []string{"TEXT"}, [][]any{{"0"}})
}

func TestReferencePredefinedUnavailableCapabilities(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE TABLE t(id INT)")
		s.Run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		s.Exec("CREATE SCHEMA pdef_ad")
		s.Run(fixture.Exec("CREATE MATERIALIZED VIEW pdef_ad.mv AS SELECT id FROM t", 1))
		s.Actor("pdef_actor")
		predefinedFailure(s, "SELECT pgrowlocks('t')", fixture.Diagnostic{Code: "42883", Message: "function pgrowlocks(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true, "prepare")
		predefinedFailure(s, "SELECT pg_write_file('/nonexistent','x',false)", fixture.Diagnostic{Code: "42883", Message: "function pg_write_file(unknown, unknown, boolean) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true, "prepare")
		s.ClearNotices()
		s.Exec("ANALYZE t")
		s.Notice("WARNING", "01000", `permission denied to analyze "t", skipping it`)
		s.ClearNotices()
		predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(-1)}})
		if !s.Backend.PG {
			predefinedNativeBoundaries(s)
		} else {
			predefinedFailure(s, "CHECKPOINT", fixture.Diagnostic{Code: "42501", Message: "permission denied to execute CHECKPOINT command", Detail: `Only roles with privileges of the "pg_checkpoint" role may execute this command.`}, false, "execute")
			predefinedFailure(s, "CREATE SUBSCRIPTION pdef_sub CONNECTION 'host=invalid' PUBLICATION pub WITH(connect=false,slot_name=NONE)", fixture.Diagnostic{Code: "42501", Message: "permission denied to create subscription", Detail: `Only roles with privileges of the "pg_create_subscription" role may create subscriptions.`}, false, "execute")
			for _, tc := range []struct{ q, message, detail string }{
				{"COPY t FROM '/nonexistent'", "permission denied to COPY from a file", `Only roles with privileges of the "pg_read_server_files" role may COPY from a file.`},
				{"COPY t TO '/nonexistent'", "permission denied to COPY to a file", `Only roles with privileges of the "pg_write_server_files" role may COPY to a file.`},
				{"COPY t TO PROGRAM 'false'", "permission denied to COPY to or from an external program", `Only roles with privileges of the "pg_execute_server_program" role may COPY to or from an external program.`},
			} {
				d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: tc.message, Detail: tc.detail, Hint: `Anyone can COPY to stdout or from stdin. psql's \copy command also works for anyone.`}
				if s.Prepared {
					predefinedCopyDriverBoundary(s, tc.q)
					// Keep the safe server denial independently exercised even when
					// lib/pq cannot prepare this command outside a transaction.
					fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.ErrorDiagnostic(tc.q, d))
				} else {
					predefinedFailure(s, tc.q, d, false, "execute")
				}
			}
		}
		s.Actor("")
		for _, role := range []string{"pg_checkpoint", "pg_create_subscription", "pg_read_server_files", "pg_write_server_files", "pg_execute_server_program", "pg_signal_autovacuum_worker", "pg_signal_backend", "pg_use_reserved_connections", "pg_maintain", "pg_monitor"} {
			s.Exec("GRANT " + role + " TO pdef_actor")
		}
		if s.Backend.PG {
			s.Exec("GRANT CREATE ON DATABASE stuntgres_reference TO pdef_actor")
		}
		s.Actor("pdef_actor")
		predefinedFailure(s, "SELECT pgrowlocks('t')", fixture.Diagnostic{Code: "42883", Message: "function pgrowlocks(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true, "prepare")
		predefinedFailure(s, "SELECT pg_write_file('/nonexistent','x',false)", fixture.Diagnostic{Code: "42883", Message: "function pg_write_file(unknown, unknown, boolean) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true, "prepare")
		s.ClearNotices()
		s.Exec("ANALYZE t")
		if len(*s.Notices) != 0 {
			s.T.Fatalf("pg_maintain ANALYZE notices %#v", *s.Notices)
		}
		predefinedSelect(s, "SELECT reltuples FROM pg_class WHERE oid='t'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(1)}})
		if !s.Backend.PG {
			predefinedNativeBoundaries(s)
		} else {
			// Safe PG-only local capability consumers, not native WAL/network proof.
			s.Exec("CHECKPOINT")
			s.ClearNotices()
			s.Exec("CREATE SUBSCRIPTION pdef_sub CONNECTION 'host=invalid password=unused' PUBLICATION pub WITH(connect=false,slot_name=NONE)")
			wantNotice := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "subscription was created, but is not connected", Hint: "To initiate replication, you must manually create the replication slot, enable the subscription, and refresh the subscription."}
			if len(*s.Notices) != 1 || (*s.Notices)[0] != wantNotice {
				s.T.Fatalf("disabled subscription notices %#v want %#v", *s.Notices, wantNotice)
			}
			s.ClearNotices()
			s.Exec("DROP SUBSCRIPTION pdef_sub")
			for _, function := range []string{"pg_cancel_backend", "pg_terminate_backend"} {
				s.ClearNotices()
				predefinedSelect(s, "SELECT "+function+"(2147483647)", []string{function}, []string{"BOOL"}, [][]any{{false}})
				s.Notice("WARNING", "01000", "PID 2147483647 is not a PostgreSQL backend process")
			}
			predefinedFailure(s, "SELECT pg_terminate_backend(2147483647,-1)", fixture.Diagnostic{Code: "22003", Message: `"timeout" must not be negative`}, true, "execute")
			predefinedSelect(s, "SHOW reserved_connections", []string{"reserved_connections"}, []string{"TEXT"}, [][]any{{"0"}})
			// Do not run authorized file COPY or PROGRAM against the host.
		}
		s.Actor("")
		predefinedSelect(s, "SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		// The reserved-tier identity does not bypass actual role admission.
		s.Exec("ALTER ROLE pdef_actor PASSWORD 'role-attributes-fixture' CONNECTION LIMIT 1")
		first, err := predefinedPhysical(s, "pdef_actor")
		if err != nil {
			s.T.Fatal(err)
		}
		defer first.Close()
		second, err := predefinedPhysical(s, "pdef_actor")
		if second != nil {
			second.Close()
			s.T.Fatal("role limit returned connection")
		}
		want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: `too many connections for role "pdef_actor"`}
		fixture.AssertDiagnostic(s.T, err, &want)
		s.Exec("ALTER ROLE pdef_actor NOLOGIN")
		second, err = predefinedPhysical(s, "pdef_actor")
		if second != nil {
			second.Close()
			s.T.Fatal("NOLOGIN returned connection")
		}
		want = fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: `role "pdef_actor" is not permitted to log in`}
		fixture.AssertDiagnostic(s.T, err, &want)
	}, predefinedSubscriptionCleanup)
}

// Register before NewScenario so failure cleanup runs after physical borrowers
// and schema reset, but before role cleanup. Only this case introduces database
// CREATE; its disabled, slot-less subscription never has a remote slot to drop.
func predefinedSubscriptionCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if !fixture.Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		c, err := fixture.Admin.Conn(ctx)
		if err != nil {
			t.Error("subscription cleanup connection", err)
			return
		}
		defer c.Close()
		for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE", "DROP SUBSCRIPTION IF EXISTS pdef_sub"} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Errorf("subscription cleanup %s: %v", q, err)
			}
		}
		var actorExists bool
		if err := c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='pdef_actor')").Scan(&actorExists); err != nil {
			t.Error("subscription cleanup role lookup", err)
			return
		}
		if actorExists {
			if _, err := c.ExecContext(ctx, "REVOKE CREATE ON DATABASE stuntgres_reference FROM pdef_actor"); err != nil {
				t.Error("subscription cleanup database CREATE", err)
			}
		}
	})
}

func TestReferencePredefinedRLSBoundary(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE SCHEMA pdef_rls")
		s.Exec("CREATE TABLE pdef_rls.t(id INT,v TEXT)")
		s.Run(fixture.Exec("INSERT INTO pdef_rls.t VALUES(1,'a'),(2,'b')", 2))
		if s.Backend.PG {
			predefinedSelect(s, "SELECT rolbypassrls FROM pg_roles WHERE rolname='pdef_actor'", []string{"rolbypassrls"}, []string{"BOOL"}, [][]any{{false}})
		}
		s.Exec("GRANT pg_read_all_data,pg_write_all_data TO pdef_actor")
		s.Exec("ALTER TABLE pdef_rls.t ENABLE ROW LEVEL SECURITY")
		s.Actor("pdef_actor")
		predefinedRows(s, "pdef_rls.t", nil)
		s.Fail("INSERT INTO pdef_rls.t VALUES(3,'c')", "42501", `new row violates row-level security policy for table "t"`, "")
		s.Actor("")
		if s.Backend.PG {
			predefinedSelect(s, "SELECT rolbypassrls FROM pg_roles WHERE rolname='pdef_actor'", []string{"rolbypassrls"}, []string{"BOOL"}, [][]any{{false}})
		}
		predefinedRows(s, "pdef_rls.t", predefinedInitialRows())
	})
}

func TestReferencePredefinedBoundaryFeatureSmoke(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE TABLE t(id INT)")
		s.Run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		if s.Backend.PG {
			for _, role := range []string{"pg_read_all_settings", "pg_read_all_stats", "pg_stat_scan_tables"} {
				s.Flags(role, "pg_monitor", s.Bootstrap, 1, false, true, true)
			}
		}
		for _, role := range predefinedRoles {
			s.Exec("SET ROLE " + role.name)
			predefinedSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{role.name}})
			s.Exec("RESET ROLE")
		}
		s.Fail("GRANT pg_database_owner TO pdef_actor", "0A000", `role "pg_database_owner" cannot have explicit members`, "")
		s.Exec("GRANT pg_checkpoint,pg_create_subscription,pg_execute_server_program TO pdef_actor")
		s.Actor("pdef_actor")
		if s.Backend.PG {
			s.Exec("CHECKPOINT")
		} else {
			predefinedFailure(s, "CHECKPOINT", fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"}, false, "prepare")
			predefinedFailure(s, "COPY t TO PROGRAM 'false'", fixture.Diagnostic{Code: "0A000", Message: "COPY PROGRAM is unsupported"}, false, "prepare")
			predefinedFailure(s, "CREATE SUBSCRIPTION pdef_sub CONNECTION 'host=invalid' PUBLICATION pub WITH(connect=false,slot_name=NONE)", fixture.Diagnostic{Code: "0A000", Message: "CREATE syntax is unsupported", Position: "8"}, false, "prepare")
		}
		s.Actor("")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		predefinedSelect(s, "SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	})
}

func predefinedCopyDriverBoundary(s *fixture.RoleMembershipScenario, q string) {
	s.T.Helper()
	stmt, err := s.C.PrepareContext(s.P.Ctx, q)
	if stmt != nil {
		stmt.Close()
		s.T.Fatal("COPY prepare exposed a statement outside a transaction")
	}
	var sqlState interface{ SQLState() string }
	if err == nil || err.Error() != "pq: COPY is only allowed inside a transaction" || errors.As(err, &sqlState) {
		s.T.Fatalf("COPY prepare ordinary driver error got %T %v", err, err)
	}
}
