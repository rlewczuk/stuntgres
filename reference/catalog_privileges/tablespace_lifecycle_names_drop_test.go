package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTablespaceLifecycleNamesAndSettings(t *testing.T) {
	tablespaceLifecycleScenario(t, nil, func(t *testing.T, s *lifecycleCase) {
		s.begin(t)
		defer s.rollback(t)
		s.exec(t, "CREATE ROLE ts_lifecycle_settings_reader")
		s.exec(t, "SET default_tablespace='pg_default'")
		s.exec(t, "SET temp_tablespaces='pg_global'")
		// Resolve/cache the real global OID before its spelling changes. A
		// savepoint leaves the same transaction alive after each expected error.
		s.error(t, "CREATE TEMP TABLE ts_lifecycle_cache_before(a integer)", tablespaceDiagnostic("22023", "only shared relations can be placed in pg_global tablespace"), false)
		s.exec(t, "ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_default")
		s.exec(t, "ALTER TABLESPACE pg_global RENAME TO \"ts lifecycle global\"")
		s.names(t, "ts_lifecycle_default", "ts lifecycle global")
		s.both(t, fixture.Select("SELECT current_setting('default_tablespace') AS d,current_setting('temp_tablespaces') AS t", []string{"d", "t"}, []string{"TEXT", "TEXT"}, [][]any{{"pg_default", "pg_global"}}))
		s.error(t, "CREATE TEMP TABLE ts_lifecycle_cache_after(a integer)", tablespaceDiagnostic("22023", "only shared relations can be placed in pg_global tablespace"), false)
		// Stale default text falls back to the database default at use, not
		// an alias to the renamed descriptor. Stored setting text is unchanged.
		s.exec(t, "CREATE TABLE ts_lifecycle_default_fallback(a integer)")
		s.both(t, fixture.Select("SELECT reltablespace::bigint AS placement FROM pg_class WHERE relname='ts_lifecycle_default_fallback'", []string{"placement"}, []string{"INT8"}, [][]any{{int64(0)}}))
		d := tablespaceDiagnostic("22023", "invalid value for parameter \"default_tablespace\": \"pg_default\"")
		d.Detail = "Tablespace \"pg_default\" does not exist."
		s.error(t, "SET default_tablespace='pg_default'", d, false)
		s.error(t, "SET temp_tablespaces='pg_default'", lifecycleMissing("pg_default"), false)
		s.exec(t, "REVOKE CREATE ON TABLESPACE ts_lifecycle_default FROM PUBLIC")
		s.exec(t, "SET ROLE ts_lifecycle_settings_reader")
		// The renamed database-default identity still maps to OID0 and does
		// not require explicit tablespace CREATE permission.
		s.both(t, fixture.Exec("SET temp_tablespaces='ts_lifecycle_default'", 0))
		s.both(t, fixture.Exec("SET default_tablespace='ts_lifecycle_default'", 0))
		s.exec(t, "RESET ROLE")
		s.exec(t, "CREATE TEMP TABLE ts_lifecycle_default_temp(a integer)")
		s.both(t, fixture.Select("SELECT reltablespace::bigint AS placement FROM pg_class WHERE relname='ts_lifecycle_default_temp'", []string{"placement"}, []string{"INT8"}, [][]any{{int64(0)}}))
		s.both(t, fixture.Select("SELECT classid::bigint AS classid,objid::bigint AS objid,objsubid FROM pg_get_object_address('tablespace',ARRAY['ts_lifecycle_default'],ARRAY[]::text[])", []string{"classid", "objid", "objsubid"}, []string{"INT8", "INT8", "INT4"}, [][]any{{int64(1213), int64(1663), int64(0)}}))
		for _, name := range []string{"pg_default", "pg_global"} {
			q := "SELECT * FROM pg_get_object_address('tablespace',ARRAY['" + name + "'],ARRAY[]::text[])"
			// This is a row-producing function: assert the same full runtime
			// diagnostic through both actual driver query routes.
			for _, prepared := range []bool{false, true} {
				s.exec(t, "SAVEPOINT ts_lifecycle_address")
				step := fixture.ErrorDiagnostic(q, lifecycleMissing(name))
				step.Prepared, step.SelectRows, step.ErrorStage = prepared, true, "execute"
				s.step(t, step)
				s.exec(t, "ROLLBACK TO ts_lifecycle_address")
				s.exec(t, "RELEASE ts_lifecycle_address")
			}
		}
		s.both(t, fixture.Select("SELECT type,schema,name,identity FROM pg_identify_object(1213,1664,0)", []string{"type", "schema", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"tablespace", nil, "\"ts lifecycle global\"", "\"ts lifecycle global\""}}))
		s.both(t, fixture.Select("SELECT pg_describe_object(1213,1664,0) AS description,pg_tablespace_location(1664) AS location", []string{"description", "location"}, []string{"TEXT", "TEXT"}, [][]any{{"tablespace ts lifecycle global", ""}}))
		s.both(t, fixture.Select("SELECT tablespace FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_tablespace'", []string{"tablespace"}, []string{"NAME"}, [][]any{{"ts lifecycle global"}}))
		s.both(t, fixture.Select("SELECT reltablespace::bigint AS placement FROM pg_class WHERE oid=1213", []string{"placement"}, []string{"INT8"}, [][]any{{int64(1664)}}))
	})
}

func TestReferenceTablespaceLifecycleDrop(t *testing.T) {
	tablespaceLifecycleScenario(t, []string{"ts_lifecycle_drop_reader"}, func(t *testing.T, s *lifecycleCase) {
		s.error(t, "DROP TABLESPACE ts_lifecycle_missing", lifecycleMissing("ts_lifecycle_missing"), false)
		for _, prepared := range []bool{false, true} {
			*s.notices = nil
			q := "DROP TABLESPACE IF EXISTS ts_lifecycle_missing"
			if prepared {
				held, err := s.c.PrepareContext(s.p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				s.noticesWant(t) // Prepare must not emit the execution NOTICE.
				for range 2 {
					fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: q}, s.b.Recorder, fixture.Exec(q, 0))
					s.noticesWant(t, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "tablespace \"ts_lifecycle_missing\" does not exist, skipping"})
				}
				held.Close()
			} else {
				s.exec(t, q)
				s.noticesWant(t, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "tablespace \"ts_lifecycle_missing\" does not exist, skipping"})
			}
			s.utility(t, q, prepared)
			s.noticesWant(t, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "tablespace \"ts_lifecycle_missing\" does not exist, skipping"})
		}
		for _, name := range []string{"pg_default", "pg_global"} {
			for _, optional := range []string{"", "IF EXISTS "} {
				s.error(t, "DROP TABLESPACE "+optional+name, tablespaceDiagnostic("42501", "permission denied for tablespace "+name), false)
				s.names(t, "pg_default", "pg_global")
			}
		}
		// PostgreSQL builtin mutations remain transactional. Its autocommit
		// witnesses cover canonical superuser/nonowner pinned checks; the
		// actual-owner and renamed-pinned witnesses are committed native phases.
		s.exec(t, "CREATE ROLE ts_lifecycle_drop_reader")
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			queries := []string{"ROLLBACK", "RESET ROLE"}
			for _, q := range queries {
				if _, err := s.c.ExecContext(ctx, q); err != nil {
					t.Error("pinned owner cleanup", q, err)
				}
			}
		}()
		if !s.b.PG {
			for _, name := range []string{"pg_default", "pg_global"} {
				s.exec(t, "ALTER TABLESPACE "+name+" OWNER TO ts_lifecycle_drop_reader")
				s.exec(t, "SET ROLE ts_lifecycle_drop_reader")
				for _, optional := range []string{"", "IF EXISTS "} {
					s.error(t, "DROP TABLESPACE "+optional+name, tablespaceDiagnostic("42501", "permission denied for tablespace "+name), false)
				}
				s.exec(t, "RESET ROLE")
				s.owner(t, name, "ts_lifecycle_drop_reader", "")
				s.exec(t, "ALTER TABLESPACE "+name+" OWNER TO CURRENT_USER")
			}
		}
		s.exec(t, "SET ROLE ts_lifecycle_drop_reader")
		for _, name := range []string{"pg_default", "pg_global"} {
			s.error(t, "DROP TABLESPACE "+name, tablespaceDiagnostic("42501", "must be owner of tablespace "+name), false)
		}
		s.exec(t, "RESET ROLE")
		s.names(t, "pg_default", "pg_global")
		if !s.b.PG {
			t.Run("native-renamed-owner-pinned", func(t *testing.T) {
				s.exec(t, "ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_pinned")
				s.exec(t, "ALTER TABLESPACE ts_lifecycle_pinned OWNER TO ts_lifecycle_drop_reader")
				s.exec(t, "SET ROLE ts_lifecycle_drop_reader")
				for _, optional := range []string{"", "IF EXISTS "} {
					s.error(t, "DROP TABLESPACE "+optional+"ts_lifecycle_pinned", tablespaceDiagnostic("42501", "permission denied for tablespace ts_lifecycle_pinned"), false)
				}
				s.exec(t, "RESET ROLE")
				s.owner(t, "ts_lifecycle_pinned", "ts_lifecycle_drop_reader", "")
				// Release the role dependency without a forbidden reverse rename.
				s.exec(t, "ALTER TABLESPACE ts_lifecycle_pinned OWNER TO CURRENT_USER")
			})
		}
	})
}
