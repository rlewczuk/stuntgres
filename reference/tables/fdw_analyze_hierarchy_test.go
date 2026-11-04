package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeInherited(t *testing.T) {
	fdwAnalyzeHierarchy(t, "ordinary")
}

func TestReferenceFDWAnalyzePartition(t *testing.T) {
	fdwAnalyzeHierarchy(t, "partition")
}

func TestReferenceFDWAnalyzeForeignRoot(t *testing.T) {
	fdwAnalyzeHierarchy(t, "foreign")
}

// All hierarchy kinds consume genuine independent source SQL. ONLY suppresses
// descendant refresh, not acquisition of descendants for the parent's flavor.
func fdwAnalyzeHierarchy(t *testing.T, kind string) {
	t.Helper()
	partition, foreignRoot := kind == "partition", kind == "foreign"
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		r.advanced = &fdwAdvancedState{}
		fdwLive(t, p, b, r, run)
		fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.data", 3)
		fdwSourceExec(t, p, b, r, run, "ALTER TABLE fdw_source.data ADD COLUMN extra bigint", 0)
		fdwAnalyzeHierarchySource(t, p, b, r, run, false)
		if partition {
			run(fixture.Exec("CREATE TABLE fdw_analyze_parent(id integer,label text,flag boolean) PARTITION BY RANGE(id)", 0))
			run(fixture.Exec("CREATE TABLE fdw_analyze_local PARTITION OF fdw_analyze_parent FOR VALUES FROM(0) TO(2)", 0))
			// Attachment preserves this foreign descriptor's different slot order.
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_foreign(flag boolean,label text,id integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
			run(fixture.Exec("ALTER TABLE fdw_analyze_parent ATTACH PARTITION fdw_analyze_foreign FOR VALUES FROM(2) TO(100)", 0))
		} else if foreignRoot {
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_parent(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
			run(fixture.Exec("CREATE TABLE fdw_analyze_local() INHERITS(fdw_analyze_parent)", 0))
		} else {
			run(fixture.Exec("CREATE TABLE fdw_analyze_parent(id integer,label text,flag boolean)", 0))
			// ALTER INHERIT retains this preexisting extra/reordered descriptor,
			// unlike CREATE INHERITS, which can normalize inherited slots.
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_foreign(extra bigint,label text,id integer,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
			run(fixture.Exec("ALTER FOREIGN TABLE fdw_analyze_foreign INHERIT fdw_analyze_parent", 0))
		}
		local := "fdw_analyze_parent"
		if partition || foreignRoot {
			local = "fdw_analyze_local"
		}
		run(fixture.Exec("INSERT INTO "+local+"(id,label,flag) VALUES"+strings.TrimSuffix(strings.Repeat("(1,'local',false),", 10), ","), 10))
		fdwAnalyzeHierarchyRows(run, 2, "foreign", true)
		foreign := "fdw_analyze_foreign"
		if foreignRoot {
			foreign = "fdw_analyze_parent"
		}
		fdwAnalyzeRelationPopulation(run, foreign, -1)
		pass := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", 0)
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
			step.Prepared = prepared
			run(step)
			pass = fdwAnalyzeCompletedPass(t, p, b, c, pass)
			population := float64(10)
			if partition {
				population = 40
				fdwAnalyzeRelationPopulation(run, local, -1)
			}
			if foreignRoot {
				population = 30
				fdwAnalyzeRelationPopulation(run, local, -1)
			}
			fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", population)
			if !foreignRoot {
				fdwAnalyzeRelationPopulation(run, foreign, -1)
			}
			onlyRows := int64(10)
			if partition {
				onlyRows = 0
			}
			if foreignRoot {
				onlyRows = 30
			}
			result := fixture.Select("SELECT count(*) FROM ONLY fdw_analyze_parent", []string{"count"}, []string{"INT8"}, [][]any{{onlyRows}})
			result.Prepared = prepared
			run(result)
			fdwAnalyzeHierarchyFacts(t, p, b, c, run, "id=2", 30, "{2,1}", "{foreign,local}", 0)
			fdwAnalyzeHierarchyFacts(t, p, b, c, run, "label='foreign'", 30, "{2,1}", "{foreign,local}", 0)
			if !b.PG {
				fdwAnalyzeHierarchySample(t, r, partition)
				fdwAnalyzeHierarchyEstimate(t, p, c, "flag=true", 30)
				fdwAnalyzeHierarchyEstimate(t, p, c, "id=1", 10)
			}
			fdwAnalyzeHierarchyRows(run, 2, "foreign", true)
		}

		query := "EXPLAIN SELECT * FROM fdw_analyze_parent WHERE label='foreign'"
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		old := fdwExplainText(t, p, held, query)
		saved := strings.Join(old, "\n")
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("ANALYZE fdw_analyze_parent", 0)
			step.Prepared = prepared
			run(step)
			pass = fdwAnalyzeCompletedPass(t, p, b, c, pass)
			fdwAnalyzeRelationPopulation(run, foreign, 30)
			fdwAnalyzeRelationPopulation(run, local, 10)
			fdwAnalyzeHierarchyFacts(t, p, b, c, run, "label='foreign'", 30, "{2,1}", "{foreign,local}", 0)
		}
		// Change two independent typed distributions. Refreshing only label must
		// preserve the old inherited boolean facts and the child's own facts.
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET label='changed',flag=false", 30)
		if !b.PG {
			fdwAnalyzeHierarchyEstimate(t, p, held, "label='foreign'", 30)
		}
		run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent(label)", 0)))
		fdwAnalyzeHierarchyFacts(t, p, b, c, run, "label='changed'", 30, "{2,1}", "{changed,local}", 0)
		if !b.PG {
			fdwAnalyzeHierarchyEstimate(t, p, c, "flag=true", 30)
			if got := fdwEstimate(t, fdwExplainText(t, p, held, query)); got > 1 {
				t.Fatalf("held statement retained old label statistics: %d", got)
			}
		} else {
			run(fixture.Select("SELECT most_common_vals::text AS vals,most_common_freqs::text AS freqs FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND inherited AND attname='flag'", []string{"vals", "freqs"}, []string{"TEXT", "TEXT"}, [][]any{{"{t,f}", "{0.75,0.25}"}}))
		}
		fdwAnalyzeRelationPopulation(run, foreign, 30)
		run(fixture.Prepared(fixture.Select("SELECT count(*) FROM "+foreign+" WHERE label='changed' AND flag=false", []string{"count"}, []string{"INT8"}, [][]any{{int64(30)}})))
		run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent(flag)", 0))
		if !b.PG {
			// Imported facts may use PostgreSQL's minimum-one estimate.
			if got := fdwEstimate(t, fdwExplainText(t, p, c, "EXPLAIN SELECT * FROM fdw_analyze_parent WHERE flag=true")); got > 1 {
				t.Fatalf("selected flag refresh kept obsolete true mass: %d", got)
			}
		}
		fdwAnalyzeHierarchyRows(run, 2, "changed", false)
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET label=NULL", 30)
		run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent(label)", 0))
		fdwAnalyzeHierarchyFacts(t, p, b, c, run, "label IS NULL", 30, "{2,1}", "{local}", .75)
		fdwAnalyzeHierarchyRows(run, 2, nil, false)
		run(fixture.Exec("UPDATE ONLY "+local+" SET label=NULL", 10))
		run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent(label)", 0)))
		if b.PG {
			run(fixture.Select("SELECT null_frac,n_distinct FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND inherited AND attname='label'", []string{"null_frac", "n_distinct"}, []string{"FLOAT4", "FLOAT4"}, [][]any{{float64(1), float64(0)}}))
		} else {
			fdwAnalyzeHierarchyEstimate(t, p, c, "label IS NULL", 40)
		}
		run(fixture.Exec("UPDATE ONLY "+local+" SET label='local'", 10))
		// A positive all-NULL acquisition above is distinct from an empty source.
		fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.data", 30)
		run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)))
		population := float64(10)
		if foreignRoot {
			population = 0
		}
		fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", population)
		if !foreignRoot {
			fdwAnalyzeRelationPopulation(run, foreign, 30)
		}
		run(fixture.Exec("ANALYZE fdw_analyze_parent", 0))
		fdwAnalyzeRelationPopulation(run, foreign, 0)
		run(fixture.Select("SELECT id,label,flag FROM fdw_analyze_parent ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, fdwAnalyzeRepeatedRows(10, 1, "local", false)))
		if partition {
			// Use the supported real leaf-emptying operation on both backends;
			// this scenario does not require partition-hierarchy DELETE support.
			run(fixture.Exec("TRUNCATE ONLY "+local, 0))
		} else {
			run(fixture.Exec("DELETE FROM ONLY "+local, 10))
		}
		run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)))
		fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", 0)
		run(fixture.Select("SELECT id,label,flag FROM fdw_analyze_parent ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, nil))
		if b.PG {
			run(fixture.Select("SELECT most_common_vals::text AS vals FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND inherited AND attname='label'", []string{"vals"}, []string{"TEXT"}, [][]any{{"{local}"}}))
		}
		run(fixture.Exec("INSERT INTO "+local+"(id,label,flag) VALUES"+strings.TrimSuffix(strings.Repeat("(1,'local',false),", 10), ","), 10))
		// Singleton foreign values distinguish true typed import from a constant
		// child cardinality or fabricated repeated representative.
		fdwAnalyzeHierarchySource(t, p, b, r, run, true)
		run(fixture.Prepared(fixture.Exec("ANALYZE fdw_analyze_parent", 0)))
		fdwAnalyzeRelationPopulation(run, foreign, 30)
		if !b.PG {
			fdwAnalyzeHierarchyEstimate(t, p, c, "id=7", 1)
		}
		run(fixture.Prepared(fixture.Select("SELECT id,label,flag FROM fdw_analyze_parent WHERE id=7", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "foreign", true}})))
		if strings.Join(old, "\n") != saved {
			t.Fatal("retained EXPLAIN output mutated after refresh")
		}
		state := r.snapshot()
		if !b.PG && (state.openPools != 0 || state.openRows != 0) {
			t.Fatal("inherited sample retained source resources", state)
		}
	})
}

func fdwAnalyzeHierarchySource(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader, run func(fixture.Step), uniform bool) {
	t.Helper()
	var values []string
	for i := range 30 {
		id := 2
		if uniform {
			id += i
		}
		values = append(values, fmt.Sprintf("(%d,'foreign',true,917)", id))
	}
	fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES"+strings.Join(values, ","), 30)
}

func fdwAnalyzeRepeatedRows(n int, id int64, label any, flag bool) [][]any {
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{id, label, flag}
	}
	return rows
}

func fdwAnalyzeHierarchyRows(run func(fixture.Step), id int64, label any, flag bool) {
	rows := append(fdwAnalyzeRepeatedRows(10, 1, "local", false), fdwAnalyzeRepeatedRows(30, id, label, flag)...)
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT id,label,flag FROM fdw_analyze_parent ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, rows)
		step.Prepared = prepared
		run(step)
	}
}

func fdwAnalyzeRelationPopulation(run func(fixture.Step), relation string, rows float64) {
	run(fixture.Select("SELECT reltuples FROM pg_class WHERE relname='"+relation+"'", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{rows}}))
}

func fdwAnalyzeHierarchyEstimate(t *testing.T, p *fixture.Pair, executor fixture.Executor, predicate string, want int64) {
	t.Helper()
	query := "EXPLAIN SELECT * FROM fdw_analyze_parent WHERE " + predicate
	if got := fdwEstimate(t, fdwExplainText(t, p, executor, query)); got != want {
		t.Fatalf("%s estimate=%d want=%d", predicate, got, want)
	}
}

// PostgreSQL Append estimates use independent child statistics, so inspect its
// existing pg_stats reader for the parent flavor rather than asserting parity
// with the native single logical hierarchy estimate.
func fdwAnalyzeHierarchyFacts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), predicate string, rows int64, ids, labels string, nullFraction float64) {
	t.Helper()
	if !b.PG {
		fdwAnalyzeHierarchyEstimate(t, p, c, predicate, rows)
		return
	}
	run(fixture.Select("SELECT attname,most_common_vals::text AS vals,null_frac FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND inherited AND attname IN('id','label') ORDER BY attname", []string{"attname", "vals", "null_frac"}, []string{"NAME", "TEXT", "FLOAT4"}, [][]any{{"id", ids, float64(0)}, {"label", labels, nullFraction}}))
	if nullFraction == 0 {
		run(fixture.Select("SELECT most_common_freqs::text AS freqs FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND inherited AND attname='label'", []string{"freqs"}, []string{"TEXT"}, [][]any{{"{0.75,0.25}"}}))
	}
}

func fdwAnalyzeHierarchySample(t *testing.T, r *fdwSQLReader, partition bool) {
	t.Helper()
	r.advanced.mu.Lock()
	sample := r.advanced.sample
	requests := append([]stuntgres.ForeignRequest(nil), r.advanced.requests...)
	r.advanced.mu.Unlock()
	if len(requests) == 0 || len(sample.Rows) != 30 || sample.TotalRows != 30 {
		t.Fatalf("positive inherited foreign acquisition missing: requests=%d sample=%+v", len(requests), sample)
	}
	request := requests[len(requests)-1]
	for _, row := range sample.Rows {
		if len(row) != len(request.RequiredAttributes) {
			t.Fatal("sample/descriptor shape mismatch", row, request.RequiredAttributes)
		}
		for i, attribute := range request.RequiredAttributes {
			found := false
			for _, column := range request.Columns {
				if column.Attribute != attribute {
					continue
				}
				found = true
				var want any
				switch column.Name {
				case "id":
					want = int64(2)
				case "label":
					want = "foreign"
				case "flag":
					want = true
				case "extra":
					if partition {
						t.Fatal("partition request fabricated extra attribute")
					}
					want = int64(917)
				default:
					t.Fatal("unexpected source attribute", column.Name)
				}
				if row[i] != want {
					t.Fatalf("genuine source %s=%v want=%v", column.Name, row[i], want)
				}
			}
			if !found {
				t.Fatal("sample attribute absent from descriptor", attribute)
			}
		}
	}
}

func fdwAnalyzeCompletedPass(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, before fdwAnalyzePass) fdwAnalyzePass {
	t.Helper()
	after := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", before.count+1)
	if after.observable && !after.at.After(before.at) {
		t.Fatal("successful target did not advance its single completion time", before, after)
	}
	// Partition population is an inherited estimate, not local monitoring work.
	var kind string
	if err := c.QueryRowContext(p.Ctx, "SELECT relkind FROM pg_class WHERE oid='fdw_analyze_parent'::regclass").Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind == "p" {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n_live_tup,n_dead_tup FROM pg_stat_all_tables WHERE relid='fdw_analyze_parent'::regclass", []string{"n_live_tup", "n_dead_tup"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(0)}}))
	}
	return after
}
