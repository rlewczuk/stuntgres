package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINPendingListLimitDiagnosticsAndPrivileges(t *testing.T) {
	t.Run("invalid-values-are-atomic", func(t *testing.T) {
		physicalCases(t, nil, func(s *physicalCase) {
			s.run(fixture.Exec("SET gin_pending_list_limit='8MB'", 0))
			for _, value := range []string{"63", "-1", "2147483648", "1mb", "broken"} {
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{"SET gin_pending_list_limit=" + physicalLiteral(value), "SELECT set_config('gin_pending_list_limit'," + physicalLiteral(value) + ",false)"} {
						s.run(physicalError(q, ginLimitInvalid(value), prepared, q[:6] == "SELECT"))
						s.ginState("8MB", "8192", "session", false)
					}
				}
				q := "SET gin_pending_list_limit=" + physicalLiteral(value)
				statement, err := s.conn.PrepareContext(s.p.Ctx, q)
				if err != nil {
					s.t.Fatal("invalid candidate must be validated at execution", err)
				}
				s.ginState("8MB", "8192", "session", true)
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: statement, SQL: q}, s.recorder, physicalError(q, ginLimitInvalid(value), false, false))
				statement.Close()
				s.ginState("8MB", "8192", "session", false)
			}
		})
	})
	t.Run("real-owner-and-execute-authority", func(t *testing.T) {
		physicalCases(t, []string{"gin_limit_login", "gin_limit_owner"}, func(s *physicalCase) {
			if s.pg {
				var original sql.NullString
				if err := s.conn.QueryRowContext(s.p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=3789").Scan(&original); err != nil {
					s.t.Fatal(err)
				}
				defer func() {
					if _, err := s.conn.ExecContext(s.p.Ctx, "DROP OWNED BY gin_limit_login,gin_limit_owner CASCADE"); err != nil {
						s.t.Error(err)
					}
					var value any
					if original.Valid {
						value = original.String
					}
					// Shared builtin ACLs outlive schema cleanup; restore their exact prior state.
					if _, err := s.conn.ExecContext(s.p.Ctx, "UPDATE pg_catalog.pg_proc SET proacl=$1::aclitem[] WHERE oid=3789", value); err != nil {
						s.t.Error(err)
					}
					var restored sql.NullString
					if err := s.conn.QueryRowContext(s.p.Ctx, "SELECT proacl::text FROM pg_catalog.pg_proc WHERE oid=3789").Scan(&restored); err != nil {
						s.t.Error(err)
					} else if restored != original {
						s.t.Errorf("restored raw gin_clean_pending_list ACL got %#v want %#v", restored, original)
					}
				}()
			}
			s.run(fixture.Exec("CREATE ROLE gin_limit_login LOGIN PASSWORD 'role-core-fixture'", 0))
			s.run(fixture.Exec("CREATE ROLE gin_limit_owner", 0))
			s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO gin_limit_login,gin_limit_owner", 0))
			s.run(fixture.Exec("CREATE TABLE gin_limit_table(id integer,a integer[])", 0))
			s.run(fixture.Exec("CREATE INDEX gin_limit_btree ON gin_limit_table(id)", 0))
			s.run(fixture.Exec("CREATE INDEX gin_limit_owner_i ON gin_limit_table USING gin(a) WITH(fastupdate=on)", 0))
			s.run(fixture.Exec("CREATE VIEW gin_limit_wrong_view AS SELECT id FROM gin_limit_table", 0))
			s.run(fixture.Exec("CREATE SEQUENCE gin_limit_wrong_sequence", 0))
			ordinary := s.login("gin_limit_login")
			// USERSET is real: setting either threshold needs no special grant.
			for _, state := range []struct{ display, raw string }{{"64kB", "64"}, {"8MB", "8192"}} {
				ordinary.run(fixture.Prepared(fixture.Exec("SET gin_pending_list_limit="+physicalLiteral(state.display), 0)))
				ordinary.ginState(state.display, state.raw, "session", false)
			}
			for _, actor := range []*physicalCase{s, ordinary} {
				for _, target := range []struct{ name, message string }{
					{"gin_limit_table", `"gin_limit_table" is not an index`},
					{"gin_limit_wrong_view", `"gin_limit_wrong_view" is not an index`},
					{"gin_limit_wrong_sequence", `"gin_limit_wrong_sequence" is not an index`},
					{"gin_limit_btree", `"gin_limit_btree" is not a GIN index`},
				} {
					q := "SELECT gin_clean_pending_list(" + physicalLiteral(target.name) + "::regclass)"
					actor.run(physicalError(q, physicalDiagnostic("42809", target.message), false, true))
					actor.run(physicalError(q, physicalDiagnostic("42809", target.message), true, true))
				}
			}
			const q = "SELECT gin_clean_pending_list('gin_limit_owner_i'::regclass) AS cleaned"
			denied := physicalDiagnostic("42501", "must be owner of index gin_limit_owner_i")
			ordinary.run(physicalError(q, denied, false, true))
			ordinary.run(physicalError(q, denied, true, true))
			s.run(fixture.Exec("GRANT SELECT,MAINTAIN ON gin_limit_table TO gin_limit_login", 0))
			s.run(fixture.Exec("GRANT EXECUTE ON FUNCTION gin_clean_pending_list(regclass) TO gin_limit_login", 0))
			ordinary.run(physicalError(q, denied, true, true))
			statement, err := ordinary.conn.PrepareContext(s.p.Ctx, q)
			if err != nil {
				s.t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: ordinary.conn, Statement: statement, SQL: q}
			s.run(fixture.Exec("ALTER TABLE gin_limit_table OWNER TO gin_limit_owner", 0))
			s.run(fixture.Exec("GRANT gin_limit_owner TO gin_limit_login", 0))
			fixture.RunStep(s.t, s.p.Ctx, held, ordinary.recorder, ginLimitDemand(s.pg, q, "INT8", int64(0)))
			ordinary.run(fixture.Exec("SET ROLE gin_limit_owner", 0))
			fixture.RunStep(s.t, s.p.Ctx, held, ordinary.recorder, ginLimitDemand(s.pg, q, "INT8", int64(0)))
			ordinary.run(fixture.Exec("RESET ROLE", 0))
			s.run(fixture.Exec("REVOKE gin_limit_owner FROM gin_limit_login", 0))
			fixture.RunStep(s.t, s.p.Ctx, held, ordinary.recorder, physicalError(q, denied, false, true))
			// Superuser can clean an empty index without index ownership.
			s.run(ginLimitDemand(s.pg, q, "INT8", int64(0)))
			const nullSQL = "SELECT gin_clean_pending_list(NULL::regclass) AS cleaned"
			nullStatement, err := ordinary.conn.PrepareContext(s.p.Ctx, nullSQL)
			if err != nil {
				s.t.Fatal(err)
			}
			defer nullStatement.Close()
			ordinary.run(ginLimitResult(s.pg, nullSQL, "INT8", [][]any{{nil}}))
			// Strict constant-NULL simplification removes the invocation before EXECUTE admission.
			s.run(fixture.Exec("REVOKE EXECUTE ON FUNCTION gin_clean_pending_list(regclass) FROM PUBLIC,gin_limit_login", 0))
			executeDenied := physicalDiagnostic("42501", "permission denied for function gin_clean_pending_list")
			fixture.RunStep(s.t, s.p.Ctx, held, ordinary.recorder, physicalError(q, executeDenied, false, true))
			ordinary.run(physicalError(q, executeDenied, false, true))
			ordinary.run(physicalError(q, executeDenied, true, true))
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: nullStatement, SQL: nullSQL}, ordinary.recorder, ginLimitResult(s.pg, nullSQL, "INT8", [][]any{{nil}}))
			ordinary.run(ginLimitResult(s.pg, nullSQL, "INT8", [][]any{{nil}}))
			ordinary.run(fixture.Prepared(ginLimitResult(s.pg, nullSQL, "INT8", [][]any{{nil}})))
			s.run(fixture.Exec("GRANT EXECUTE ON FUNCTION gin_clean_pending_list(regclass) TO gin_limit_login", 0))
			fixture.RunStep(s.t, s.p.Ctx, held, ordinary.recorder, physicalError(q, denied, false, true))
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: nullStatement, SQL: nullSQL}, ordinary.recorder, ginLimitResult(s.pg, nullSQL, "INT8", [][]any{{nil}}))
			ordinary.ginState("8MB", "8192", "session", true)
		})
	})
	t.Run("other-session-temporary-index", func(t *testing.T) {
		physicalCases(t, nil, func(s *physicalCase) {
			peer := s.ginPeer()
			peer.run(fixture.Exec("CREATE TEMP TABLE gin_limit_temp(a integer[])", 0))
			peer.run(fixture.Exec("CREATE INDEX gin_limit_temp_i ON gin_limit_temp USING gin(a)", 0))
			var oid int64
			if err := peer.conn.QueryRowContext(s.p.Ctx, "SELECT 'gin_limit_temp_i'::regclass::oid::bigint").Scan(&oid); err != nil {
				s.t.Fatal(err)
			}
			q := "SELECT gin_clean_pending_list($1::oid::regclass)"
			s.run(physicalError(q, physicalDiagnostic("0A000", "cannot access temporary indexes of other sessions"), false, true, oid))
			s.run(physicalError(q, physicalDiagnostic("0A000", "cannot access temporary indexes of other sessions"), true, true, oid))
		})
	})
}
