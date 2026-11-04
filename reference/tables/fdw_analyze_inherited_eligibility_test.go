package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeInheritedEligibility(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		_, foreign := fdwAnalyzeMixedSetup(t, p, b, r, run, "ordinary", 10, 30)
		for _, role := range []string{"fdw_owner", "fdw_other", "fdw_reader"} {
			run(fixture.Exec("CREATE ROLE "+role, 0))
		}
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_owner,fdw_other", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_owner,fdw_other", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE "+foreign+" OWNER TO fdw_owner", 0))
		run(fixture.Exec("GRANT MAINTAIN ON fdw_analyze_parent TO fdw_other", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_owner SERVER fdw_live OPTIONS("+fdwAnalyzeOwnerOptions(b)+")", 0))
		// An unusable actor mapping must never replace the child's owner mapping.
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_other SERVER fdw_live OPTIONS(user 'fdw_reader')", 0))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("SET ROLE fdw_other", 0))
			step := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
			step.Prepared = prepared
			run(step)
			run(fixture.Exec("RESET ROLE", 0))
			fdwAnalyzeRelationPopulation(run, foreign, -1)
			fdwAnalyzeBaselineFacts(t, p, b, c, run)
		}
		if !b.PG {
			r.advanced.mu.Lock()
			requests := append([]stuntgres.ForeignRequest(nil), r.advanced.requests...)
			negotiations := append([]stuntgres.ForeignRequest(nil), r.advanced.negotiations...)
			r.advanced.mu.Unlock()
			if len(requests) == 0 || len(negotiations) == 0 {
				t.Fatal("missing owner acquisition/metadata negotiation")
			}
			for _, request := range requests {
				if request.Role != "fdw_owner" {
					t.Fatal("inherited sampler used parent or actor authority", request.Role)
				}
			}
			for _, request := range negotiations {
				if len(request.MappingOptions) != 0 || request.Role != "fdw_owner" {
					t.Fatal("negotiation resolved credentials or wrong owner", request)
				}
			}
		}
		// PUBLIC supplies a missing exact owner mapping, never the actor mapping.
		run(fixture.Exec("DROP USER MAPPING FOR fdw_owner SERVER fdw_live", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_live OPTIONS("+fdwAnalyzeOwnerOptions(b)+")", 0))
		run(fixture.Exec("SET ROLE fdw_other", 0))
		run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)))
		run(fixture.Exec("RESET ROLE", 0))
		fdwAnalyzeBaselineFacts(t, p, b, c, run)
		// A diamond through a real foreign node is traversed and counted once.
		run(fixture.Exec("CREATE TABLE fdw_analyze_sibling() INHERITS(fdw_analyze_parent)", 0))
		run(fixture.Exec("CREATE TABLE fdw_analyze_grandchild() INHERITS(fdw_analyze_foreign,fdw_analyze_sibling)", 0))
		run(fixture.Exec("INSERT INTO fdw_analyze_grandchild VALUES"+fdwAnalyzeValues(5, 4, "grandchild", false), 5))
		run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)))
		fdwAnalyzeRelationPopulation(run, "fdw_analyze_grandchild", -1)
		run(fixture.Select("SELECT count(*) FROM fdw_analyze_parent", []string{"count"}, []string{"INT8"}, [][]any{{int64(45)}}))
		if !b.PG {
			fdwAnalyzeHierarchyEstimate(t, p, c, "id=4", 5)
		}
		run(fixture.Exec("ANALYZE fdw_analyze_parent", 0))
		fdwAnalyzeRelationPopulation(run, foreign, 30)
		fdwAnalyzeRelationPopulation(run, "fdw_analyze_grandchild", 5)
		if b.PG {
			return
		}
		// Native-only capability controls have no stock postgres_fdw analogue.
		// False negotiation and optional-interface absence must silently exclude
		// just the foreign contributor, never prune its native descendants.
		for _, handler := range []string{"fdw_sql_handler", "fdw_scan_only_handler"} {
			r.advanced.mu.Lock()
			r.advanced.analyzeSupported = func(stuntgres.ForeignRequest) bool { return false }
			before := len(r.advanced.acquisitions)
			r.advanced.mu.Unlock()
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER "+handler, 0))
			var notices []fixture.Diagnostic
			_, own, rec := fixture.NoticeReferencePool(t, p, false, &notices)
			fixture.RunStep(t, p.Ctx, own, rec, fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
			if len(notices) != 0 {
				t.Fatal("unsupported inherited contributor emitted direct warning", notices)
			}
			fdwAnalyzeHierarchyEstimate(t, p, c, "id=4", 5)
			fdwAnalyzeHierarchyEstimate(t, p, c, "id=1", 10)
			r.advanced.mu.Lock()
			after := len(r.advanced.acquisitions)
			r.advanced.mu.Unlock()
			if after != before {
				t.Fatal("unsupported inherited contributor acquired")
			}
			notices = nil
			fixture.RunStep(t, p.Ctx, own, rec, fixture.Prepared(fixture.Exec("ANALYZE fdw_analyze_foreign", 0)))
			want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `skipping "fdw_analyze_foreign" --- cannot analyze this foreign table`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatal("separate unsupported target warning", notices)
			}
		}
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_sql_handler", 0))
		r.advanced.mu.Lock()
		r.advanced.analyzeSupported = nil
		r.advanced.mu.Unlock()
	})
}
