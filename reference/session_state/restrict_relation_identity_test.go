package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceRestrictRelationKindIdentitySchema(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		pool := s.p.Memory
		if s.pg {
			pool = s.p.Postgres
		}
		s.t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := pool.ExecContext(ctx, "DROP VIEW IF EXISTS pg_catalog.rk_schema_v"); err != nil {
				s.t.Errorf("system-schema witness cleanup: %v", err)
			}
		})
		rkSeed(s)
		s.run(fixture.Exec("CREATE VIEW rk_schema_v AS SELECT id FROM rk_t", 0))
		var originalOID int64
		if err := s.conn.QueryRowContext(s.p.Ctx, "SELECT 'public.rk_schema_v'::regclass::oid::bigint").Scan(&originalOID); err != nil {
			s.t.Fatal(err)
		}
		// PostgreSQL permits this move; the native namespace owner explicitly
		// rejects system-schema destinations. Keep that adjacent boundary intact.
		qualified, schema := "public.rk_schema_v", "public"
		move := "ALTER VIEW rk_schema_v SET SCHEMA pg_catalog"
		if s.pg {
			s.run(fixture.Exec(move, 0))
			qualified, schema = "pg_catalog.rk_schema_v", "pg_catalog"
		} else {
			s.run(physicalError(move, physicalDiagnostic("0A000", "system schema creation is unsupported"), false, false))
		}
		s.run(rkRows(qualified, 1, 2))
		var currentOID int64
		if err := s.conn.QueryRowContext(s.p.Ctx, "SELECT '"+qualified+"'::regclass::oid::bigint").Scan(&currentOID); err != nil || currentOID != originalOID {
			s.t.Fatal("schema setup replaced the original view identity", currentOID, originalOID, err)
		}
		s.run(fixture.Select("SELECT c.oid>=16384 AS normal,n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='"+qualified+"'::regclass", []string{"normal", "nspname"}, []string{"BOOL", "NAME"}, [][]any{{true, schema}}))
		rkSet(s, "view")
		for _, prepared := range []bool{false, true} {
			s.run(rkDenied("SELECT id FROM "+qualified+" ORDER BY id", "rk_schema_v", prepared, true))
		}
		s.run(fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='"+schema+"' AND viewname='rk_schema_v'", []string{"viewname"}, []string{"NAME"}, [][]any{{"rk_schema_v"}}))
		s.run(fixture.Exec("RESET "+rkSetting, 0))
		s.run(rkRows(qualified, 1, 2))
		s.run(fixture.Exec("DROP VIEW "+qualified, 0))
	})
}
