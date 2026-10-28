package reference_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsExpressionFeatureSmoke(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		expressionPopulation(s)
		// Explicitly exercise both public utility routes in each smoke scenario.
		s.run(fixture.Prepared(fixture.Exec("CREATE STATISTICS expr_single ON (a+1) FROM expr_t", 0)))
		s.run(fixture.Exec("CREATE STATISTICS expr_joint(ndistinct) ON (a+1),b FROM expr_t", 0))
		s.run(expressionDefinition("expr_single", "", "{e}"))
		s.run(expressionDefinition("expr_joint", "2", "{d,e}"))
		s.run(expressionMissingData())
		s.run(fixture.Exec("ANALYZE expr_t", 0))
		t.Log("expression smoke phase: initial ANALYZE")
		s.run(expressionSkew("expr_single", 1, "{2,3}"))
		s.run(expressionJoint(s.pg, "expr_joint", `{"2, -1": 3}`, 1))
		s.run(expressionSkew("expr_joint", 1, "{2,3}"))
		expressionRawDatum(t, s, "expr_single")
		oid := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='expr_single'")
		s.run(fixture.Exec("ALTER TABLE expr_t RENAME COLUMN a TO renamed", 0))
		var definition string
		if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT pg_get_expr(stxexprs,stxrelid) FROM pg_statistic_ext WHERE stxname='expr_single'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, "renamed") {
			t.Fatal("renamed binding absent", definition)
		}
		if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='expr_single'"); got != oid {
			t.Fatal("rename changed identity")
		}
		t.Log("expression smoke phase: renamed input")
		s.run(expressionSkew("expr_single", 1, "{2,3}"))
		for _, prepared := range []bool{false, true} {
			step := statisticsError("CREATE STATISTICS expr_bad(ndistinct) ON (renamed+1) FROM expr_t", "0A000", "when building statistics on a single expression, statistics kinds may not be specified")
			if prepared {
				step = fixture.Prepared(step)
			}
			s.run(step)
		}
		fixture.RoleReferenceCleanup(t, []string{"expr_smoke_reader"})
		// Remove grants before RoleReferenceCleanup drops the role. This
		// backend subtest finishes before its parent pair resets public.
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			db := s.pair.Native
			if s.pg {
				db = s.pair.Postgres
			}
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			for _, q := range []string{"RESET ROLE", "DROP OWNED BY expr_smoke_reader RESTRICT"} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Error("expression reader cleanup", err)
				}
			}
		})
		for _, q := range []string{"CREATE ROLE expr_smoke_reader", "GRANT USAGE,CREATE ON SCHEMA public TO expr_smoke_reader", "GRANT SELECT ON expr_t TO expr_smoke_reader", "SET ROLE expr_smoke_reader"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(statisticsError("CREATE STATISTICS expr_denied ON (renamed+1) FROM expr_t", "42501", "must be owner of table expr_t"))
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(expressionDefinition("expr_single", "", "{e}"))
		s.run(expressionDefinition("expr_joint", "2", "{d,e}"))
		s.run(fixture.Exec("UPDATE expr_t SET renamed=9,b=90", 20))
		t.Log("expression smoke phase: stale after source UPDATE")
		s.run(expressionSkew("expr_single", 1, "{2,3}"))
		if !s.pg {
			var saved bytes.Buffer
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(s.pair.Ctx, s.pair.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		t.Log("expression smoke phase: stale after native Dump/Reset/Restore (or PostgreSQL DML)")
		s.run(expressionSkew("expr_single", 1, "{2,3}"))
		s.run(expressionJoint(s.pg, "expr_joint", `{"2, -1": 3}`, 1))
		s.run(fixture.Exec("ANALYZE expr_t", 0))
		s.run(expressionUniform("expr_single", "{10}"))
		s.run(expressionJoint(s.pg, "expr_joint", `{"2, -1": 1}`, 1))
	})
}
