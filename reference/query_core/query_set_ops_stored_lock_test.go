package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

const querySetOpsLockReader = "set_lock_reader"

// Catalogs already grant SELECT to PUBLIC, without granting UPDATE. Reader-owned
// invoker views exercise that authority in both modes without inventing native
// SQL GRANT support. Ordinary-table ACL controls remain PostgreSQL fixture SQL.
func querySetOpsStoredLockSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); c.Close() })
	run := func(q string, n int64) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, n)) }
	run("CREATE USER "+querySetOpsLockReader, 0)
	run("CREATE TABLE set_lock_base(v INT)", 0)
	run("INSERT INTO set_lock_base VALUES(7),(7),(9)", 3)
	run("CREATE SEQUENCE set_lock_sequence", 0)
	for _, q := range []string{
		"CREATE VIEW set_lock_catalog AS SELECT relname FROM pg_catalog.pg_class WHERE relname='set_lock_base' UNION ALL SELECT relname FROM pg_catalog.pg_class WHERE relname='set_lock_base'",
		"CREATE VIEW set_lock_catalog_nested AS SELECT relname FROM set_lock_catalog",
		"CREATE VIEW set_lock_sequence_view AS SELECT last_value FROM set_lock_sequence UNION ALL SELECT last_value FROM set_lock_sequence",
		"CREATE VIEW set_lock_sequence_nested AS SELECT last_value FROM set_lock_sequence_view",
		"CREATE VIEW set_lock_owned AS SELECT v FROM set_lock_base UNION ALL SELECT v FROM set_lock_base",
		"CREATE VIEW set_lock_invoker WITH(security_invoker=true) AS SELECT v FROM set_lock_base UNION ALL SELECT v FROM set_lock_base",
		"CREATE VIEW set_lock_owned_nested AS SELECT v FROM set_lock_owned",
		"CREATE VIEW set_lock_invoker_nested AS SELECT v FROM set_lock_invoker",
		"CREATE VIEW set_lock_local WITH(security_invoker=true) AS SELECT v FROM set_lock_base FOR UPDATE",
		"CREATE VIEW set_lock_local_set WITH(security_invoker=true) AS SELECT v FROM set_lock_local UNION ALL SELECT v FROM set_lock_base",
	} {
		run(q, 0)
	}
	if b.PG {
		for _, q := range []string{
			"GRANT USAGE,CREATE ON SCHEMA public TO " + querySetOpsLockReader,
			"GRANT SELECT ON set_lock_base TO " + querySetOpsLockReader,
			"GRANT SELECT,UPDATE ON set_lock_owned,set_lock_invoker,set_lock_owned_nested,set_lock_invoker_nested,set_lock_local,set_lock_local_set TO " + querySetOpsLockReader,
		} {
			run(q, 0)
		}
	}
	run("SET SESSION AUTHORIZATION "+querySetOpsLockReader, 0)
	run("CREATE VIEW set_lock_reader_catalog WITH(security_invoker=true) AS SELECT relname FROM pg_catalog.pg_class WHERE relname='set_lock_base' UNION ALL SELECT relname FROM pg_catalog.pg_class WHERE relname='set_lock_base'", 0)
	run("CREATE VIEW set_lock_reader_nested AS SELECT relname FROM set_lock_reader_catalog", 0)
	run("RESET SESSION AUTHORIZATION", 0)
	return c
}

func querySetOpsStoredLockReads(reader, pg bool, clause string) []fixture.Step {
	names := []string{"set_lock_catalog", "set_lock_catalog_nested"}
	if reader {
		names = []string{"set_lock_reader_catalog", "set_lock_reader_nested"}
	}
	var steps []fixture.Step
	for _, name := range names {
		steps = append(steps, querySetOpsBag("SELECT relname FROM "+name+" "+clause, "relname", "NAME", [][]any{{"set_lock_base"}, {"set_lock_base"}}))
	}
	if !reader {
		for _, name := range []string{"set_lock_sequence_view", "set_lock_sequence_nested"} {
			steps = append(steps, querySetOpsBag("SELECT last_value FROM "+name+" "+clause, "last_value", "INT8", [][]any{{int64(1)}, {int64(1)}}))
		}
	}
	if !reader || pg {
		for _, name := range []string{"set_lock_owned", "set_lock_invoker", "set_lock_owned_nested", "set_lock_invoker_nested"} {
			steps = append(steps, querySetOpsBag("SELECT v FROM "+name+" "+clause, "v", "INT4", [][]any{{int64(7)}, {int64(7)}, {int64(9)}, {int64(7)}, {int64(7)}, {int64(9)}}))
		}
	}
	return steps
}

// Pinned PostgreSQL 18.0 discovery in tests-13 establishes the bags and stages
// below: inline sets fail at Prepare; stored local/SELECT authority fails only
// at Execute. Every omitted logical diagnostic field is asserted empty.
func TestReferenceQuerySetOpsStoredLockBoundaries(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{querySetOpsLockReader})
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		c := querySetOpsStoredLockSeed(t, p, b)
		for _, reader := range []bool{false, true} {
			if reader {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION "+querySetOpsLockReader, 0))
			}
			for _, clause := range fixture.RowClauses {
				for _, prepared := range []bool{false, true} {
					for _, s := range querySetOpsStoredLockReads(reader, b.PG, clause) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					// A locally stored mark survives the set boundary independently
					// of its inherited outer mark.
					if !reader {
						s := querySetOpsBag("SELECT v FROM set_lock_local_set "+clause, "v", "INT4", [][]any{{int64(7)}, {int64(7)}, {int64(9)}, {int64(7)}, {int64(7)}, {int64(9)}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					} else if b.PG {
						s := fixture.ImplicitError("SELECT v FROM set_lock_local_set "+clause, fixture.Diagnostic{Code: "42501", Message: "permission denied for table set_lock_base"})
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					q := "SELECT relname FROM (SELECT relname FROM pg_catalog.pg_class UNION ALL SELECT relname FROM pg_catalog.pg_class) d " + clause
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: clause + " is not allowed with UNION/INTERSECT/EXCEPT"})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					s.Query = "SELECT v FROM (SELECT v FROM set_lock_base UNION ALL SELECT v FROM set_lock_base) d " + clause
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
		}
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE SELECT ON set_lock_base FROM "+querySetOpsLockReader, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION "+querySetOpsLockReader, 0))
			// Owner-authority views still read through their owner after the
			// reader's base SELECT is revoked, including zero-demand cursors.
			for _, name := range []string{"set_lock_owned", "set_lock_owned_nested"} {
				for _, prepared := range []bool{false, true} {
					s := querySetOpsBag("SELECT v FROM "+name+" FOR UPDATE", "v", "INT4", [][]any{{int64(7)}, {int64(7)}, {int64(9)}, {int64(7)}, {int64(7)}, {int64(9)}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					s.Query, s.Rows = "SELECT v FROM "+name+" LIMIT 0 FOR UPDATE", [][]any{}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
			for _, name := range []string{"set_lock_invoker", "set_lock_invoker_nested"} {
				for _, tail := range []string{" FOR UPDATE", " LIMIT 0 FOR UPDATE"} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError("SELECT v FROM "+name+tail, fixture.Diagnostic{Code: "42501", Message: "permission denied for table set_lock_base"})
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			}
		}
	})
}
