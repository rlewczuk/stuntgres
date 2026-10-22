package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePartitionInfoPrepared(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		queries := []string{"SELECT * FROM pg_partition_tree($1::regclass)", "SELECT * FROM pg_partition_ancestors($1::regclass)", "SELECT pg_partition_root($1::regclass)"}
		held := make([]fixture.HeldStatement, 3)
		for i, q := range queries {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		}
		check := func(input any, tree, ancestors [][]any, root any) {
			t.Helper()
			steps := []fixture.Step{partitionInfoTree(queries[0], tree, input), fixture.Select(queries[1], []string{"relid"}, []string{"REGCLASS"}, ancestors, input), fixture.Select(queries[2], []string{"pg_partition_root"}, []string{"REGCLASS"}, [][]any{{root}}, input)}
			for i, s := range steps {
				s.Bag = true
				partitionInfoRun(t, p, b, held[i], s)
			}
		}
		leaf := [][]any{{"pinfo_l", "pinfo_m", true, int64(0)}}
		chain := [][]any{{"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}}
		check("pinfo_l", leaf, chain, "pinfo_r")
		check(nil, nil, nil, nil)
		check("0", nil, nil, nil)
		check("pinfo_l", leaf, chain, "pinfo_r")
		var retained *sql.Rows
		if !b.PG {
			var err error
			retained, err = held[1].Statement.QueryContext(p.Ctx, "pinfo_l")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		partitionInfoExec(t, p, b, c, "CREATE TABLE pinfo_new(k INT,payload TEXT)", "ALTER TABLE pinfo_m ATTACH PARTITION pinfo_new FOR VALUES FROM(5) TO(10)")
		check("pinfo_m", [][]any{{"pinfo_m", "pinfo_r", false, int64(0)}, {"pinfo_l", "pinfo_m", true, int64(1)}, {"pinfo_new", "pinfo_m", true, int64(1)}}, [][]any{{"pinfo_m"}, {"pinfo_r"}}, "pinfo_r")
		partitionInfoRoot(t, p, b, c, "'pinfo_new_k_payload_key'", "pinfo_r_key")
		indexLeaf := [][]any{{"pinfo_l_k_payload_key", "pinfo_m_k_payload_key", true, int64(0)}}
		indexChain := [][]any{{"pinfo_l_k_payload_key"}, {"pinfo_m_k_payload_key"}, {"pinfo_r_key"}}
		check("pinfo_l_k_payload_key", indexLeaf, indexChain, "pinfo_r_key")
		partitionInfoExec(t, p, b, c, "BEGIN", "SAVEPOINT pinfo_before", "ALTER TABLE pinfo_r DETACH PARTITION pinfo_m")
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		check("pinfo_l", leaf, [][]any{{"pinfo_l"}, {"pinfo_m"}}, "pinfo_m")
		check("pinfo_m", [][]any{{"pinfo_m", nil, false, int64(0)}, {"pinfo_l", "pinfo_m", true, int64(1)}, {"pinfo_new", "pinfo_m", true, int64(1)}}, [][]any{{"pinfo_m"}}, "pinfo_m")
		partitionInfoRoot(t, p, b, c, "'pinfo_l_k_payload_key'", "pinfo_m_k_payload_key")
		check("pinfo_l_k_payload_key", indexLeaf, [][]any{{"pinfo_l_k_payload_key"}, {"pinfo_m_k_payload_key"}}, "pinfo_m_k_payload_key")
		partitionInfoExec(t, p, b, c, "ROLLBACK TO pinfo_before", "COMMIT")
		check("pinfo_l", leaf, chain, "pinfo_r")
		check("pinfo_l_k_payload_key", indexLeaf, indexChain, "pinfo_r_key")
		partitionInfoExec(t, p, b, c, "ALTER TABLE pinfo_r DETACH PARTITION pinfo_m", "ALTER TABLE pinfo_m DETACH PARTITION pinfo_l")
		check("pinfo_l", nil, nil, nil)
		check("pinfo_l_k_payload_key", nil, nil, nil)
		partitionInfoEmpty(t, p, b, c, "'pinfo_l_k_payload_key'")
		if retained != nil {
			seen := map[string]int{}
			for retained.Next() {
				var name string
				if err := retained.Scan(&name); err != nil {
					t.Fatal(err)
				}
				seen[name]++
			}
			if retained.Err() != nil || len(seen) != 3 || seen["pinfo_l"] != 1 || seen["pinfo_m"] != 1 || seen["pinfo_r"] != 1 {
				t.Fatal("retained ancestry changed", seen, retained.Err())
			}
		}
	})
}

func TestReferencePartitionInfoLifecycle(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		for _, q := range []string{"SELECT * FROM pg_partition_tree($1::regclass)", "SELECT * FROM pg_partition_ancestors($1::regclass)", "SELECT pg_partition_root($1::regclass)"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if !b.PG {
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				expired, expire := context.WithDeadline(p.Ctx, time.Unix(1, 0))
				defer expire()
				for _, tc := range []struct {
					ctx  context.Context
					want error
				}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
					for _, route := range []string{"direct", "prepare", "held"} {
						var rows *sql.Rows
						var err error
						if route == "prepare" {
							s, e := c.PrepareContext(tc.ctx, q)
							err = e
							if s != nil {
								s.Close()
								t.Fatal("canceled prepare published statement")
							}
						} else if route == "held" {
							rows, err = stmt.QueryContext(tc.ctx, "pinfo_l")
						} else {
							rows, err = c.QueryContext(tc.ctx, q, "pinfo_l")
						}
						if rows != nil {
							rows.Close()
							t.Fatal("context failure published rows")
						}
						if !errors.Is(err, tc.want) {
							t.Fatal(route, err, tc.want)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("fabricated context SQLSTATE", state.SQLState())
						}
					}
				}
			}
			active := "SELECT pg_partition_root(relid),pg_sleep(600) FROM pg_partition_tree('pinfo_r') LIMIT 1"
			fixture.SleepActiveCancel(t, p, b, c, active, nil, func(ctx context.Context) fixture.RelationOutcome {
				result, err := c.ExecContext(ctx, active)
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			var s fixture.Step
			if q == "SELECT * FROM pg_partition_tree($1::regclass)" {
				s = partitionInfoTree(q, [][]any{{"pinfo_l", "pinfo_m", true, int64(0)}}, "pinfo_l")
			} else if q == "SELECT * FROM pg_partition_ancestors($1::regclass)" {
				s = fixture.Select(q, []string{"relid"}, []string{"REGCLASS"}, [][]any{{"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}}, "pinfo_l")
			} else {
				s = fixture.Select(q, []string{"pg_partition_root"}, []string{"REGCLASS"}, [][]any{{"pinfo_r"}}, "pinfo_l")
			}
			s.Bag = true
			partitionInfoRun(t, p, b, held, s)
		}
	})
}

func TestReferencePartitionInfoPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pinfo_reader"})
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		partitionInfoExec(t, p, b, c, "CREATE ROLE pinfo_reader NOSUPERUSER", "GRANT USAGE ON SCHEMA public TO pinfo_reader")
		for _, name := range []string{"pg_partition_tree", "pg_partition_ancestors", "pg_partition_root"} {
			t.Run(name, func(t *testing.T) {
				q := "SELECT * FROM " + name + "($1::regclass)"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				defer func() {
					c.ExecContext(p.Ctx, "RESET ROLE")
					c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_catalog."+name+"(regclass) TO PUBLIC")
					stmt.Close()
				}()
				partitionInfoExec(t, p, b, c, "SET ROLE pinfo_reader")
				// No table SELECT grant: topology inquiries do not read table rows.
				partitionInfoRoot(t, p, b, c, "'pinfo_l'", "pinfo_r")
				partitionInfoExec(t, p, b, c, "RESET ROLE", "REVOKE EXECUTE ON FUNCTION pg_catalog."+name+"(regclass) FROM PUBLIC", "SET ROLE pinfo_reader")
				d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name)
				for _, route := range []struct {
					name string
					x    fixture.Executor
				}{{"direct", c}, {"held", held}} {
					t.Run(route.name+"/nonnull", func(t *testing.T) {
						s := fixture.ErrorDiagnostic(q, d, "pinfo_l")
						s.SelectRows = true
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, route.x, b.Recorder, s)
					})
					t.Run(route.name+"/parameter-null", func(t *testing.T) {
						// Pinned 18.6 oracle: scalar root NULL folds before EXECUTE
						// admission on direct and held routes; both SRFs still deny.
						if name == "pg_partition_root" {
							partitionInfoRun(t, p, b, route.x, fixture.Select(q, []string{"pg_partition_root"}, []string{"REGCLASS"}, [][]any{{nil}}, nil))
						} else {
							s := fixture.ErrorDiagnostic(q, d, nil)
							s.SelectRows = true
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, route.x, b.Recorder, s)
						}
					})
				}
				t.Run("direct/constant-null", func(t *testing.T) {
					if name == "pg_partition_root" {
						partitionInfoRoot(t, p, b, c, "NULL::regclass", nil)
					} else {
						s := fixture.ErrorDiagnostic("SELECT * FROM "+name+"(NULL::regclass)", d)
						s.SelectRows = true
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				})
				partitionInfoExec(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog."+name+"(regclass) TO PUBLIC", "SET ROLE pinfo_reader")
				t.Run("held/regranted", func(t *testing.T) {
					var s fixture.Step
					if name == "pg_partition_tree" {
						s = partitionInfoTree(q, [][]any{{"pinfo_l", "pinfo_m", true, int64(0)}}, "pinfo_l")
					} else if name == "pg_partition_ancestors" {
						s = fixture.Select(q, []string{"relid"}, []string{"REGCLASS"}, [][]any{{"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}}, "pinfo_l")
					} else {
						s = fixture.Select(q, []string{"pg_partition_root"}, []string{"REGCLASS"}, [][]any{{"pinfo_r"}}, "pinfo_l")
					}
					s.Bag = true
					partitionInfoRun(t, p, b, held, s)
				})
			})
		}
	})
}
