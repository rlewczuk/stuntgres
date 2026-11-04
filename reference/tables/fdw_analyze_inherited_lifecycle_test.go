package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// These are independently specified per-pass effects, not physical plan parity.
// The ordinary mapping failure is the tests17 PG18.6 oracle witness. Foreign
// roots and partition/no-local variants distinguish the two completion points.
func TestReferenceFDWAnalyzeInheritedLifecycle(t *testing.T) {
	for _, kind := range []string{"ordinary", "partition", "foreign"} {
		for _, prepared := range []bool{false, true} {
			name := kind + "/direct"
			if prepared {
				name = kind + "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					local, foreign := fdwAnalyzeMixedSetup(t, p, b, r, run, kind, 10, 30)
					run(fixture.Exec("CREATE ROLE fdw_owner", 0))
					run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_owner", 0))
					failing := foreign
					if kind == "foreign" {
						fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.empty(id integer,label text,flag boolean)", 0)
						run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_failure() INHERITS(fdw_analyze_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'empty')", 0))
						failing = "fdw_analyze_failure"
					}
					if kind == "partition" {
						// Establish the leaf's genuine owner while detached; this
						// fixture does not require ALTER OWNER on an attached leaf.
						run(fixture.Exec("ALTER TABLE fdw_analyze_parent DETACH PARTITION "+failing, 0))
					}
					run(fixture.Exec("ALTER FOREIGN TABLE "+failing+" OWNER TO fdw_owner", 0))
					if kind == "partition" {
						run(fixture.Exec("ALTER TABLE fdw_analyze_parent ATTACH PARTITION "+failing+" FOR VALUES FROM(2) TO(100)", 0))
					}
					run(fixture.Exec("CREATE USER MAPPING FOR fdw_owner SERVER fdw_live OPTIONS("+fdwAnalyzeOwnerOptions(b)+")", 0))
					run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
					before := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", -1)
					// Local changes make completed local maintenance distinguishable.
					if kind == "foreign" {
						fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES"+fdwAnalyzeValues(20, 3, "new", false), 20)
					} else {
						run(fixture.Exec("INSERT INTO "+local+" VALUES"+fdwAnalyzeValues(20, 1, "new", false), 20))
					}
					run(fixture.Exec("DROP USER MAPPING FOR fdw_owner SERVER fdw_live", 0))
					failure := fixture.ErrorDiagnostic("ANALYZE ONLY fdw_analyze_parent", fdwDiagnostic("42704", `user mapping not found for user "fdw_owner", server "fdw_live"`))
					failure.Prepared = prepared
					run(failure)
					increment, population := int64(1), float64(30)
					if kind == "partition" {
						increment, population = 0, 40
					}
					if kind == "foreign" {
						population = 50
					}
					fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", population)
					after := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", before.count+increment)
					if after.observable && increment == 0 && !after.at.Equal(before.at) {
						t.Fatal("unfinished inherited pass advanced time", before, after)
					}
					if after.observable && increment != 0 && !after.at.After(before.at) {
						t.Fatal("completed local pass did not advance time", before, after)
					}
					fdwAnalyzeBaselineFacts(t, p, b, c, run)
					fdwAnalyzeLocalBaselineFacts(t, p, b, c, run, kind, population)
					// Column admission happens before any local pass.
					early := fixture.ErrorDiagnostic("ANALYZE ONLY fdw_analyze_parent(missing)", fdwDiagnostic("42703", `column "missing" of relation "fdw_analyze_parent" does not exist`))
					early.Prepared = prepared
					run(early)
					unchanged := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", after.count)
					if unchanged.observable && !unchanged.at.Equal(after.at) {
						t.Fatal("early failure advanced local time")
					}
					// Failed savepoint work loses typed deltas, but not a completed
					// existing-object local pass. Full rollback has the same split.
					for _, savepoint := range []bool{false, true} {
						run(fixture.Exec("BEGIN", 0))
						if savepoint {
							run(fixture.Exec("SAVEPOINT analyze_pass", 0))
						}
						run(failure)
						if savepoint {
							run(fixture.Exec("ROLLBACK TO SAVEPOINT analyze_pass", 0))
							run(fixture.Exec("COMMIT", 0))
						} else {
							run(fixture.Exec("ROLLBACK", 0))
						}
						after = fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", after.count+increment)
						fdwAnalyzeBaselineFacts(t, p, b, c, run)
						fdwAnalyzeLocalBaselineFacts(t, p, b, c, run, kind, population)
					}
					run(fixture.Exec("CREATE TABLE fdw_analyze_earlier(id integer,label text,flag boolean)", 0))
					run(fixture.Exec("INSERT INTO fdw_analyze_earlier VALUES"+fdwAnalyzeValues(10, 1, "earlier", true), 10))
					run(fixture.Exec("ANALYZE fdw_analyze_earlier", 0))
					run(fixture.Exec("INSERT INTO fdw_analyze_earlier VALUES"+fdwAnalyzeValues(20, 2, "later", false), 20))
					prior := failure
					prior.Query = "ANALYZE fdw_analyze_earlier,fdw_analyze_parent"
					run(prior)
					fdwAnalyzeRelationPopulation(run, "fdw_analyze_earlier", 30)
					query := "EXPLAIN SELECT * FROM fdw_analyze_earlier WHERE id=2"
					if got := fdwEstimate(t, fdwExplainText(t, p, c, query)); got != 20 {
						t.Fatalf("earlier completed target lost publication: %d", got)
					}
					fdwAnalyzeBaselineFacts(t, p, b, c, run)
					// A private newly created target has no public metadata ghost.
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("CREATE TABLE fdw_analyze_private(id integer,label text,flag boolean)", 0))
					run(fixture.Exec("INSERT INTO fdw_analyze_private VALUES(9,'private',true)", 1))
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_private_child() INHERITS(fdw_analyze_private) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
					run(fixture.Exec("ALTER FOREIGN TABLE fdw_analyze_private_child OWNER TO fdw_owner", 0))
					run(fixture.ErrorDiagnostic("ANALYZE ONLY fdw_analyze_private", fdwDiagnostic("42704", `user mapping not found for user "fdw_owner", server "fdw_live"`)))
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN('fdw_analyze_private','fdw_analyze_private_child')", []string{"relname"}, []string{"NAME"}, nil))
					// Repair and refresh succeeds with the actual changed records.
					run(fixture.Exec("CREATE USER MAPPING FOR fdw_owner SERVER fdw_live OPTIONS("+fdwAnalyzeOwnerOptions(b)+")", 0))
					beforeSuccess := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", -1)
					run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)))
					fdwAnalyzeCompletedPass(t, p, b, c, beforeSuccess)
					successPopulation := float64(30)
					if kind == "partition" {
						successPopulation = 60
					} else if kind == "foreign" {
						successPopulation = 50
					}
					fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", successPopulation)
					if !b.PG {
						fdwAnalyzeHierarchyEstimate(t, p, c, "label='foreign'", 30)
						fdwAnalyzeHierarchyEstimate(t, p, c, "label='new'", 20)
						fdwAnalyzeHierarchyEstimate(t, p, c, "label='local'", 10)
					}
					run(fixture.Select("SELECT count(*) FROM fdw_analyze_parent", []string{"count"}, []string{"INT8"}, [][]any{{int64(60)}}))
					if kind == "foreign" {
						// Failure negotiating/acquiring the root's local pass has no
						// completed pass to preserve, unlike its inherited failure.
						beforeEarly := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", -1)
						var owner string
						if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
							t.Fatal(err)
						}
						run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_live", 0))
						run(fixture.Prepared(fixture.ErrorDiagnostic("ANALYZE ONLY fdw_analyze_parent", fdwDiagnostic("42704", `user mapping not found for user "`+owner+`", server "fdw_live"`))))
						fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", 50)
						unchanged := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", beforeEarly.count)
						if unchanged.observable && !unchanged.at.Equal(beforeEarly.at) {
							t.Fatal("failed foreign local pass advanced time")
						}
						run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS("+fdwAnalyzeOwnerOptions(b)+")", 0))
					}
				})
			})
		}
	}
}

func fdwAnalyzeOwnerOptions(b fixture.Backend) string {
	if b.PG {
		return "user 'stuntgres',password 'stuntgres',password_required 'false'"
	}
	return "user 'postgres'"
}

func fdwAnalyzeValues(n, id int, label string, flag bool) string {
	value := "false"
	if flag {
		value = "true"
	}
	return strings.TrimSuffix(strings.Repeat("("+strconv.Itoa(id)+",'"+label+"',"+value+"),", n), ",")
}

func fdwAnalyzeMixedSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader, run func(fixture.Step), kind string, native, remote int) (local, foreign string) {
	t.Helper()
	r.advanced = &fdwAdvancedState{}
	fdwLive(t, p, b, r, run)
	fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.data", 3)
	fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES"+fdwAnalyzeValues(remote, 2, "foreign", true), int64(remote))
	local, foreign = "fdw_analyze_parent", "fdw_analyze_foreign"
	switch kind {
	case "partition":
		run(fixture.Exec("CREATE TABLE fdw_analyze_parent(id integer,label text,flag boolean) PARTITION BY RANGE(id)", 0))
		run(fixture.Exec("CREATE TABLE fdw_analyze_local PARTITION OF fdw_analyze_parent FOR VALUES FROM(0) TO(2)", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_foreign PARTITION OF fdw_analyze_parent FOR VALUES FROM(2) TO(100) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		local = "fdw_analyze_local"
	case "foreign":
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_parent(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		run(fixture.Exec("CREATE TABLE fdw_analyze_local() INHERITS(fdw_analyze_parent)", 0))
		local, foreign = "fdw_analyze_local", "fdw_analyze_parent"
	default:
		run(fixture.Exec("CREATE TABLE fdw_analyze_parent(id integer,label text,flag boolean)", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_foreign() INHERITS(fdw_analyze_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
	}
	run(fixture.Exec("INSERT INTO "+local+" VALUES"+fdwAnalyzeValues(native, 1, "local", false), int64(native)))
	return local, foreign
}

type fdwAnalyzePass struct {
	count      int64
	at         time.Time
	observable bool
}

func fdwAnalyzePassState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, relation string, want int64) fdwAnalyzePass {
	t.Helper()
	var kind string
	if err := c.QueryRowContext(p.Ctx, "SELECT relkind FROM pg_class WHERE oid='"+relation+"'::regclass").Scan(&kind); err != nil {
		t.Fatal(err)
	}
	// The released table-stat view excludes foreign relations. Native getters
	// are declaration-only, so internal canonical ownership tests cover their
	// count/time instead; do not invent a public row or extend adjacent APIs.
	if kind == "f" && !b.PG {
		return fdwAnalyzePass{}
	}
	query := "SELECT analyze_count,last_analyze FROM pg_stat_all_tables WHERE relid='" + relation + "'::regclass"
	if kind == "f" {
		query = "SELECT pg_stat_get_analyze_count('" + relation + "'::regclass),pg_stat_get_last_analyze_time('" + relation + "'::regclass)"
	}
	if b.PG {
		if _, err := c.ExecContext(p.Ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		state := fdwAnalyzePass{observable: true}
		var at sql.NullTime
		if _, err := c.ExecContext(ctx, "SELECT pg_stat_clear_snapshot()"); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, query).Scan(&state.count, &at); err != nil {
			t.Fatal(err)
		}
		state.at = at.Time
		if want < 0 && state.count > 0 || state.count == want {
			return state
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatalf("%s analyze count=%d want=%d: %v", relation, state.count, want, ctx.Err())
		}
	}
}

func fdwAnalyzeBaselineFacts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	if b.PG {
		run(fixture.Select("SELECT most_common_vals::text AS vals,most_common_freqs::text AS freqs FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND inherited AND attname='label'", []string{"vals", "freqs"}, []string{"TEXT", "TEXT"}, [][]any{{"{foreign,local}", "{0.75,0.25}"}}))
	} else {
		fdwAnalyzeHierarchyEstimate(t, p, c, "label='foreign'", 30)
		fdwAnalyzeHierarchyEstimate(t, p, c, "label='local'", 10)
	}
}

func fdwAnalyzeLocalBaselineFacts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), kind string, population float64) {
	t.Helper()
	label := "local"
	if kind == "foreign" {
		label = "foreign"
	}
	if b.PG {
		var rows [][]any
		if kind != "partition" {
			rows = [][]any{{"{" + label + "}", "{1}"}}
		}
		run(fixture.Select("SELECT most_common_vals::text AS vals,most_common_freqs::text AS freqs FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND NOT inherited AND attname='label'", []string{"vals", "freqs"}, []string{"TEXT", "TEXT"}, rows))
	} else if kind != "partition" {
		query := "EXPLAIN SELECT * FROM ONLY fdw_analyze_parent WHERE label='" + label + "'"
		if got := fdwEstimate(t, fdwExplainText(t, p, c, query)); got != int64(population) {
			t.Fatalf("transactional local typed facts changed after inherited failure: estimate=%d want=%v", got, population)
		}
	}
}
