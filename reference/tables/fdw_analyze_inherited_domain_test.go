package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeInheritedDomain(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				r.advanced = &fdwAdvancedState{}
				fdwLive(t, p, b, r, run)
				fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.data", 3)
				fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES"+fdwAnalyzeValues(30, 2, "foreign", true), 30)
				// The independently queried remote column remains ordinary integer.
				// Local descriptor import must enforce the real constrained domain.
				run(fixture.Exec("CREATE DOMAIN fdw_analyze_positive AS integer CONSTRAINT fdw_analyze_positive_check CHECK(VALUE>0)", 0))
				run(fixture.Exec("CREATE TABLE fdw_analyze_parent(id fdw_analyze_positive,label text,flag boolean)", 0))
				run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_foreign() INHERITS(fdw_analyze_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
				run(fixture.Exec("INSERT INTO fdw_analyze_parent VALUES"+fdwAnalyzeValues(10, 1, "local", false), 10))
				run(fixture.Select("SELECT c.relname,a.atttypid='fdw_analyze_positive'::regtype AS domain_type FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname IN('fdw_analyze_parent','fdw_analyze_foreign') AND a.attname='id' ORDER BY c.relname", []string{"relname", "domain_type"}, []string{"NAME", "BOOL"}, [][]any{{"fdw_analyze_foreign", true}, {"fdw_analyze_parent", true}}))
				run(fixture.Exec("CREATE STATISTICS fdw_analyze_joint(ndistinct,dependencies,mcv) ON id,flag FROM fdw_analyze_parent", 0))
				run(fixture.Exec("CREATE STATISTICS fdw_analyze_expression ON (id+1) FROM fdw_analyze_parent", 0))
				analyze := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
				analyze.Prepared = prepared
				run(analyze)
				fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", 10)
				fdwAnalyzeRelationPopulation(run, "fdw_analyze_foreign", -1)
				fdwAnalyzeHierarchyFacts(t, p, b, c, run, "label='foreign'", 30, "{2,1}", "{foreign,local}", 0)
				fdwAnalyzeExpressionValues(run, "{3,2}")
				fdwAnalyzeDomainJointGroups(run, 1, 2)
				fdwAnalyzeDomainRows(run, prepared, false)
				before := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", -1)
				joint := fdwAnalyzeJointPayloads(t, p, c)
				expressions := fdwAnalyzeDomainExpressions(t, p, c)
				query := "EXPLAIN SELECT * FROM fdw_analyze_parent WHERE label='foreign'"
				held, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				old := fdwExplainText(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, query)
				saved := strings.Join(old, "\n")
				// A distinguishable completed local pass must survive the later
				// imported-domain failure; neither typed flavor may publish.
				run(fixture.Exec("INSERT INTO fdw_analyze_parent VALUES"+fdwAnalyzeValues(20, 3, "new", false), 20))
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET id=-1", 30)
				// Use the same real restricted visibility on both callers, so the
				// domain name is qualified without a backend-specific message.
				run(fixture.Exec("SET search_path=pg_catalog", 0))
				want := fdwDiagnostic("23514", `value for domain public.fdw_analyze_positive violates check constraint "fdw_analyze_positive_check"`)
				want.Schema, want.DataType, want.Constraint = "public", "fdw_analyze_positive", "fdw_analyze_positive_check"
				// postgres_fdw adds its relation-aware text-conversion Context.
				// The native Go provider returns typed logical values through the
				// established utility import boundary, whose Context stays empty.
				if b.PG {
					want.Context = `column "id" of foreign table "fdw_analyze_foreign"`
				}
				failure := fixture.ErrorDiagnostic("ANALYZE ONLY public.fdw_analyze_parent", want)
				failure.Prepared = prepared
				run(failure)
				run(fixture.Exec("SET search_path=public", 0))
				fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", 30)
				fdwAnalyzeRelationPopulation(run, "fdw_analyze_foreign", -1)
				after := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", before.count+1)
				if !after.at.After(before.at) {
					t.Fatal("inherited domain failure retracted completed local time", before, after)
				}
				fdwAnalyzeBaselineFacts(t, p, b, c, run)
				fdwAnalyzeLocalBaselineFacts(t, p, b, c, run, "ordinary", 30)
				if got := fdwAnalyzeJointPayloads(t, p, c); !reflect.DeepEqual(got, joint) {
					t.Fatal("failed domain import replaced joint typed payload", got, joint)
				}
				if got := fdwAnalyzeDomainExpressions(t, p, c); !reflect.DeepEqual(got, expressions) {
					t.Fatal("failed domain import replaced expression payload", got, expressions)
				}
				run(fixture.Select("SELECT id::integer AS id,label,flag FROM ONLY fdw_analyze_parent ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, append(fdwAnalyzeRepeatedRows(10, 1, "local", false), fdwAnalyzeRepeatedRows(20, 3, "new", false)...)))
				if !b.PG {
					if state := r.snapshot(); state.openRows != 0 || state.openPools != 0 {
						t.Fatal("domain import failure retained source resources", state)
					}
				}
				// Restore genuine source data and retry through the same public route.
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET id=4", 30)
				run(analyze)
				fdwAnalyzeCompletedPass(t, p, b, c, after)
				fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", 30)
				fdwAnalyzeRelationPopulation(run, "fdw_analyze_foreign", -1)
				fdwAnalyzeDomainJointGroups(run, 2, 3)
				fdwAnalyzeDomainRows(run, prepared, true)
				if b.PG {
					run(fixture.Select("SELECT n_distinct,null_frac FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND inherited AND attname='id'", []string{"n_distinct", "null_frac"}, []string{"FLOAT4", "FLOAT4"}, [][]any{{float64(3), float64(0)}}))
				} else {
					fdwAnalyzeHierarchyEstimate(t, p, c, "label='local'", 10)
					fdwAnalyzeHierarchyEstimate(t, p, c, "label='new'", 20)
					fdwAnalyzeHierarchyEstimate(t, p, c, "label='foreign'", 30)
				}
				run(fixture.Select("SELECT (stxdexpr[1]).stavalues1::text AS values FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_expression' AND d.stxdinherit", []string{"values"}, []string{"TEXT"}, [][]any{{"{5,4,2}"}}))
				if strings.Join(old, "\n") != saved {
					t.Fatal("saved domain hierarchy EXPLAIN output mutated")
				}
			})
		})
	}
}

func fdwAnalyzeDomainJointGroups(run func(fixture.Step), local, inherited int) {
	run(fixture.Select("SELECT d.stxdinherit,d.stxdndistinct::text AS distincts FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_joint' ORDER BY d.stxdinherit", []string{"stxdinherit", "distincts"}, []string{"BOOL", "TEXT"}, [][]any{{false, fmt.Sprintf(`{"1, 3": %d}`, local)}, {true, fmt.Sprintf(`{"1, 3": %d}`, inherited)}}))
}

func fdwAnalyzeDomainRows(run func(fixture.Step), prepared, changed bool) {
	rows := fdwAnalyzeRepeatedRows(10, 1, "local", false)
	if changed {
		rows = append(rows, fdwAnalyzeRepeatedRows(20, 3, "new", false)...)
		rows = append(rows, fdwAnalyzeRepeatedRows(30, 4, "foreign", true)...)
	} else {
		rows = append(rows, fdwAnalyzeRepeatedRows(30, 2, "foreign", true)...)
	}
	step := fixture.Select("SELECT id::integer AS id,label,flag FROM fdw_analyze_parent ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, rows)
	step.Prepared = prepared
	run(step)
}

func fdwAnalyzeDomainExpressions(t *testing.T, p *fixture.Pair, c *sql.Conn) []string {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT stxdexpr::text FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_expression' ORDER BY d.stxdinherit")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatal("missing local/inherited domain expression payloads", result)
	}
	return result
}
