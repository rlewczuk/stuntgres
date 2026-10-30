package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func tempTablespacesAuthority(t *testing.T) {
	physicalCases(t, []string{"tt_setting_login"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE tt_setting_login LOGIN PASSWORD 'role-core-fixture'", 0))
		s.run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO tt_setting_login", 0))
		// Restore shared-object ACLs even if an assertion terminates this callback.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, q := range []string{"REVOKE CREATE ON TABLESPACE pg_global FROM tt_setting_login", "REVOKE CREATE ON TABLESPACE pg_default FROM tt_setting_login", "REVOKE USAGE,CREATE ON SCHEMA public FROM tt_setting_login"} {
				if _, err := s.conn.ExecContext(ctx, q); err != nil { s.t.Error(err) }
			}
		}()
		var database string
		if err := s.conn.QueryRowContext(s.p.Ctx, "SELECT current_database()").Scan(&database); err != nil { s.t.Fatal(err) }
		s.run(fixture.Exec("GRANT TEMP ON DATABASE "+`"`+database+`"`+" TO tt_setting_login", 0))
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := s.conn.ExecContext(ctx, "REVOKE TEMP ON DATABASE "+`"`+database+`"`+" FROM tt_setting_login"); err != nil { s.t.Error(err) }
		}()
		ordinary := s.login("tt_setting_login")
		defer ordinary.conn.Close()
		// A returned sql.Conn leaves its physical session in the pool. Drop
		// role-owned temporary objects before shared-role cleanup visits them.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			names := []string{"tt_sentinel", "tt_granted", "tt_filtered", "tt_after_denied"}
			for i := range 3 { names = append(names, fmt.Sprintf("tt_default_%d", i)) }
			for i := range 4 { names = append(names, fmt.Sprintf("tt_mixed_filtered_%d", i)) }
			for _, name := range names {
				if _, err := ordinary.conn.ExecContext(ctx, "DROP TABLE IF EXISTS pg_temp."+name); err != nil { s.t.Error(err) }
			}
		}()
		ordinary.run(fixture.Exec("CREATE TEMP TABLE tt_sentinel(v INT)", 0))
		ordinary.run(fixture.Exec("INSERT INTO tt_sentinel VALUES(73)", 1))
		denied := physicalDiagnostic("42501", "permission denied for tablespace pg_global")
		for _, prepared := range []bool{false, true} {
			ordinary.run(physicalError("SET temp_tablespaces='pg_global'", denied, prepared, false))
			ordinary.run(physicalError("SELECT set_config('temp_tablespaces','pg_global',false)", denied, prepared, true))
			ordinary.tempTablespacesReads("", "default")
		}
		s.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_default FROM tt_setting_login", 0))
		for i, value := range []string{"", `""`, "pg_default"} {
			ordinary.run(clientText("SELECT set_config('temp_tablespaces',"+physicalLiteral(value)+",false)", "set_config", value))
			ordinary.run(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE tt_default_%d(v INT)", i), 0))
		}
		s.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_global TO tt_setting_login", 0))
		ordinary.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
		ordinary.run(physicalError("CREATE TEMP TABLE tt_granted(v INT)", defaultTablespaceGlobal(), false, false))
		ordinary.tempTablespacesAbsent("tt_granted")
		s.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_global FROM tt_setting_login", 0))
		ordinary.tempTablespacesReads("pg_global", "session")
		ordinary.run(fixture.Exec("CREATE TEMP TABLE tt_filtered(v INT)", 0))
		ordinary.run(physicalError("SET temp_tablespaces='pg_global'", denied, false, false))
		ordinary.tempTablespacesReads("pg_global", "session")
		ordinary.run(fixture.Exec("CREATE TEMP TABLE tt_after_denied(v INT)", 0))
		// A retained mixed list drops only its now-ineligible member next transaction.
		s.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_global TO tt_setting_login", 0))
		ordinary.run(clientText("SELECT set_config('temp_tablespaces','pg_global, pg_default',false)", "set_config", "pg_global, pg_default"))
		s.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_global FROM tt_setting_login", 0))
		ordinary.run(fixture.Exec("BEGIN", 0))
		for i := range 4 { ordinary.run(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE tt_mixed_filtered_%d(v INT)", i), 0)) }
		ordinary.run(fixture.Exec("COMMIT", 0))
		ordinary.tempTablespacesReads("pg_global, pg_default", "session")
		ordinary.tempTablespacesSentinel()
	})
}

func TestReferenceTempTablespacesAuthorityAndDiagnostics(t *testing.T) {
	t.Run("authority", tempTablespacesAuthority)
	t.Run("invalid-values", func(t *testing.T) {
		physicalCases(t, nil, func(s *physicalCase) {
			s.run(fixture.Exec("CREATE TEMP TABLE tt_sentinel(v INT)", 0))
			s.run(fixture.Exec("INSERT INTO tt_sentinel VALUES(73)", 1))
			s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ input string; diagnostic fixture.Diagnostic }{
					{"missing", physicalDiagnostic("42704", `tablespace "missing" does not exist`)},
					{`"a""b"`, physicalDiagnostic("42704", `tablespace "a"b" does not exist`)},
					{`"unclosed`, clientInvalid("temp_tablespaces", `"unclosed`, "List syntax is invalid.", "")},
					{"pg_default,", clientInvalid("temp_tablespaces", "pg_default,", "List syntax is invalid.", "")},
				} {
					s.run(physicalError("SELECT set_config('temp_tablespaces',"+physicalLiteral(tc.input)+",false)", tc.diagnostic, prepared, true))
					s.tempTablespacesReads("pg_global", "session")
					s.run(physicalError("CREATE TEMP TABLE tt_invalid_effect(v INT)", defaultTablespaceGlobal(), false, false))
					s.tempTablespacesAbsent("tt_invalid_effect")
					s.tempTablespacesSentinel()
				}
				s.run(physicalError("SET temp_tablespaces='missing'", physicalDiagnostic("42704", `tablespace "missing" does not exist`), prepared, false))
				s.tempTablespacesReads("pg_global", "session")
				s.run(physicalError("CREATE TEMP TABLE tt_invalid_set_effect(v INT)", defaultTablespaceGlobal(), false, false))
				s.tempTablespacesAbsent("tt_invalid_set_effect")
				s.tempTablespacesSentinel()
			}
			for i, value := range []string{"", `""`, "pg_default, pg_default", `"pg_default", ""`, "  PG_DEFAULT , pg_default "} {
				for _, prepared := range []bool{false, true} {
					step := clientText("SELECT set_config('temp_tablespaces',"+physicalLiteral(value)+",false)", "set_config", value)
					step.Prepared = prepared
					s.run(step)
					s.tempTablespacesReads(value, "session")
				}
				s.run(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE tt_spelling_%d(v INT)", i), 0))
			}
			s.run(fixture.Exec("SET temp_tablespaces TO 'pg_default',''", 0))
			s.tempTablespacesReads(`pg_default, ""`, "session")
			s.tempTablespacesSentinel()
		})
	})
}
