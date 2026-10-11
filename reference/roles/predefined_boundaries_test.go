package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func predefinedMissingFunction(s *membershipScenario, q, signature string) {
	s.t.Helper()
	if !s.backend.PG {
		predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: "functions are unsupported", Position: "8"}, true, "prepare")
		return
	}
	predefinedFailure(s, q, fixture.Diagnostic{Code: "42883", Message: "function " + signature + " does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true, "prepare")
}

func predefinedNativeBoundaries(s *membershipScenario) {
	s.t.Helper()
	for _, q := range []string{"CHECKPOINT", "COPY t FROM '/nonexistent'", "COPY t TO '/nonexistent'", "COPY t TO PROGRAM 'false'", "VACUUM t", "ANALYZE t", "CLUSTER t", "REFRESH MATERIALIZED VIEW pdef_ad.mv"} {
		predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"}, false, "prepare")
	}
	predefinedFailure(s, "CREATE SUBSCRIPTION pdef_sub CONNECTION 'host=invalid' PUBLICATION pub WITH(connect=false,slot_name=NONE)", fixture.Diagnostic{Code: "0A000", Message: "CREATE syntax is unsupported", Position: "8"}, false, "prepare")
	for _, tc := range []struct{ q, signature string }{
		{"SELECT pg_read_file('/nonexistent')", "pg_read_file(unknown)"},
		{"SELECT pg_write_file('/nonexistent','x',false)", "pg_write_file(unknown, unknown, boolean)"},
		{"SELECT pg_cancel_backend(2147483647)", "pg_cancel_backend(integer)"},
		{"SELECT pg_terminate_backend(2147483647)", "pg_terminate_backend(integer)"},
		{"SELECT pgrowlocks('t')", "pgrowlocks(unknown)"},
	} {
		predefinedMissingFunction(s, tc.q, tc.signature)
	}
	predefinedFailure(s, "SELECT query FROM pg_stat_activity", fixture.Diagnostic{Code: "42P01", Message: `relation "pg_stat_activity" does not exist`, Position: "19"}, true, "prepare")
	predefinedFailure(s, "SHOW reserved_connections", fixture.Diagnostic{Code: "42704", Message: `unrecognized configuration parameter "reserved_connections"`}, true, "prepare")
}

func TestReferencePredefinedUnavailableCapabilities(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE TABLE t(id INT)")
		s.run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		s.exec("CREATE SCHEMA pdef_ad")
		s.run(fixture.Exec("CREATE MATERIALIZED VIEW pdef_ad.mv AS SELECT id FROM t", 1))
		s.actor("pdef_actor")
		if !s.backend.PG {
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
				if s.prepared {
					predefinedCopyDriverBoundary(s, tc.q)
					// Keep the safe server denial independently exercised even when
					// lib/pq cannot prepare this command outside a transaction.
					fixture.RunStep(s.t, s.p.Ctx, s.c, s.record, fixture.ErrorDiagnostic(tc.q, d))
				} else {
					predefinedFailure(s, tc.q, d, false, "execute")
				}
			}
		}
		s.actor("")
		for _, role := range []string{"pg_checkpoint", "pg_create_subscription", "pg_read_server_files", "pg_write_server_files", "pg_execute_server_program", "pg_signal_autovacuum_worker", "pg_signal_backend", "pg_use_reserved_connections", "pg_maintain", "pg_monitor"} {
			s.exec("GRANT " + role + " TO pdef_actor")
		}
		if s.backend.PG {
			s.exec("GRANT CREATE ON DATABASE stuntgres_reference TO pdef_actor")
		}
		s.actor("pdef_actor")
		if !s.backend.PG {
			predefinedNativeBoundaries(s)
		} else {
			// Safe PG-only local capability consumers, not native WAL/network proof.
			s.exec("CHECKPOINT")
			s.clearNotices()
			s.exec("CREATE SUBSCRIPTION pdef_sub CONNECTION 'host=invalid password=unused' PUBLICATION pub WITH(connect=false,slot_name=NONE)")
			wantNotice := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "subscription was created, but is not connected", Hint: "To initiate replication, you must manually create the replication slot, enable the subscription, and refresh the subscription."}
			if len(*s.notices) != 1 || (*s.notices)[0] != wantNotice {
				s.t.Fatalf("disabled subscription notices %#v want %#v", *s.notices, wantNotice)
			}
			s.clearNotices()
			s.exec("DROP SUBSCRIPTION pdef_sub")
			for _, function := range []string{"pg_cancel_backend", "pg_terminate_backend"} {
				s.clearNotices()
				predefinedSelect(s, "SELECT "+function+"(2147483647)", []string{function}, []string{"BOOL"}, [][]any{{false}})
				s.notice("WARNING", "01000", "PID 2147483647 is not a PostgreSQL backend process")
			}
			predefinedFailure(s, "SELECT pg_terminate_backend(2147483647,-1)", fixture.Diagnostic{Code: "22003", Message: `"timeout" must not be negative`}, true, "execute")
			predefinedSelect(s, "SHOW reserved_connections", []string{"reserved_connections"}, []string{"TEXT"}, [][]any{{"0"}})
			// Do not run authorized file COPY or PROGRAM against the host.
		}
		s.actor("")
		predefinedSelect(s, "SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		// The reserved-tier identity does not bypass actual role admission.
		s.exec("ALTER ROLE pdef_actor PASSWORD 'role-attributes-fixture' CONNECTION LIMIT 1")
		first, err := predefinedPhysical(s, "pdef_actor")
		if err != nil {
			s.t.Fatal(err)
		}
		defer first.Close()
		second, err := predefinedPhysical(s, "pdef_actor")
		if second != nil {
			second.Close()
			s.t.Fatal("role limit returned connection")
		}
		want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: `too many connections for role "pdef_actor"`}
		fixture.AssertDiagnostic(s.t, err, &want)
		s.exec("ALTER ROLE pdef_actor NOLOGIN")
		second, err = predefinedPhysical(s, "pdef_actor")
		if second != nil {
			second.Close()
			s.t.Fatal("NOLOGIN returned connection")
		}
		want = fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: `role "pdef_actor" is not permitted to log in`}
		fixture.AssertDiagnostic(s.t, err, &want)
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
	predefinedCases(t, []string{"pdef_actor"}, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE SCHEMA pdef_rls")
		s.exec("CREATE TABLE pdef_rls.t(id INT,v TEXT)")
		s.run(fixture.Exec("INSERT INTO pdef_rls.t VALUES(1,'a'),(2,'b')", 2))
		if s.backend.PG {
			predefinedSelect(s, "SELECT rolbypassrls FROM pg_roles WHERE rolname='pdef_actor'", []string{"rolbypassrls"}, []string{"BOOL"}, [][]any{{false}})
		}
		s.exec("GRANT pg_read_all_data,pg_write_all_data TO pdef_actor")
		if s.backend.PG {
			s.exec("ALTER TABLE pdef_rls.t ENABLE ROW LEVEL SECURITY")
			s.actor("pdef_actor")
			predefinedRows(s, "pdef_rls.t", [][]any{})
			s.fail("INSERT INTO pdef_rls.t VALUES(3,'c')", "42501", `new row violates row-level security policy for table "t"`, "")
			s.actor("")
			predefinedSelect(s, "SELECT rolbypassrls FROM pg_roles WHERE rolname='pdef_actor'", []string{"rolbypassrls"}, []string{"BOOL"}, [][]any{{false}})
		} else {
			predefinedFailure(s, "ALTER TABLE pdef_rls.t ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE action is unsupported", Position: "24"}, false, "prepare")
			// Truthful absent policy owner, NOT native RLS enforcement parity.
			s.actor("pdef_actor")
			predefinedRows(s, "pdef_rls.t", predefinedInitialRows())
			s.actor("")
		}
		predefinedRows(s, "pdef_rls.t", predefinedInitialRows())
	})
}

func TestReferencePredefinedBoundaryFeatureSmoke(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE TABLE t(id INT)")
		s.run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		if s.backend.PG {
			for _, role := range []string{"pg_read_all_settings", "pg_read_all_stats", "pg_stat_scan_tables"} {
				s.flags(role, "pg_monitor", s.bootstrap, 1, false, true, true)
			}
		}
		for _, role := range predefinedRoles {
			s.exec("SET ROLE " + role.name)
			predefinedSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{role.name}})
			s.exec("RESET ROLE")
		}
		s.fail("GRANT pg_database_owner TO pdef_actor", "0A000", `role "pg_database_owner" cannot have explicit members`, "")
		s.exec("GRANT pg_checkpoint,pg_create_subscription,pg_execute_server_program TO pdef_actor")
		s.actor("pdef_actor")
		if s.backend.PG {
			s.exec("CHECKPOINT")
		} else {
			for _, q := range []string{"CHECKPOINT", "COPY t TO PROGRAM 'false'"} {
				predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"}, false, "prepare")
			}
			predefinedFailure(s, "CREATE SUBSCRIPTION pdef_sub CONNECTION 'host=invalid' PUBLICATION pub WITH(connect=false,slot_name=NONE)", fixture.Diagnostic{Code: "0A000", Message: "CREATE syntax is unsupported", Position: "8"}, false, "prepare")
		}
		s.actor("")
		if !s.backend.PG {
			predefinedFailure(s, "ALTER TABLE t ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE action is unsupported", Position: "15"}, false, "prepare")
		}
		predefinedSelect(s, "SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	})
}

func predefinedCopyDriverBoundary(s *membershipScenario, q string) {
	s.t.Helper()
	stmt, err := s.c.PrepareContext(s.p.Ctx, q)
	if stmt != nil {
		stmt.Close()
		s.t.Fatal("COPY prepare exposed a statement outside a transaction")
	}
	var sqlState interface{ SQLState() string }
	if err == nil || err.Error() != "pq: COPY is only allowed inside a transaction" || errors.As(err, &sqlState) {
		s.t.Fatalf("COPY prepare ordinary driver error got %T %v", err, err)
	}
}
