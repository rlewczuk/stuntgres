package reference_test

import (
	"database/sql"
	"testing"
)

const querySetOpsLockReader = "set_lock_reader"

// Catalogs already grant SELECT to PUBLIC, without granting UPDATE. Reader-owned
// invoker views exercise that authority in both modes without inventing native
// SQL GRANT support. Ordinary-table ACL controls remain PostgreSQL fixture SQL.
func querySetOpsStoredLockSeed(t *testing.T, p *referencePair, b referenceBackend) *sql.Conn {
	t.Helper()
	c, err := b.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); c.Close() })
	run := func(q string, n int64) { referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, n)) }
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
	if b.pg {
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

func querySetOpsStoredLockReads(reader, pg bool, clause string) []referenceStep {
	names := []string{"set_lock_catalog", "set_lock_catalog_nested"}
	if reader {
		names = []string{"set_lock_reader_catalog", "set_lock_reader_nested"}
	}
	var steps []referenceStep
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
	roleReferenceCleanup(t, []string{querySetOpsLockReader})
	querySetOpsScenario(t, func(p *referencePair, b referenceBackend) {
		c := querySetOpsStoredLockSeed(t, p, b)
		for _, reader := range []bool{false, true} {
			if reader {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET SESSION AUTHORIZATION "+querySetOpsLockReader, 0))
			}
			for _, clause := range referenceRowClauses {
				for _, prepared := range []bool{false, true} {
					for _, s := range querySetOpsStoredLockReads(reader, b.pg, clause) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, b.recorder, s)
					}
					// A locally stored mark survives the set boundary independently
					// of its inherited outer mark.
					if !reader {
						s := querySetOpsBag("SELECT v FROM set_lock_local_set "+clause, "v", "INT4", [][]any{{int64(7)}, {int64(7)}, {int64(9)}, {int64(7)}, {int64(7)}, {int64(9)}})
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, b.recorder, s)
					} else if b.pg {
						s := referenceImplicitError("SELECT v FROM set_lock_local_set "+clause, referenceDiagnostic{Code: "42501", Message: "permission denied for table set_lock_base"})
						s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
						referenceRunStep(t, p.ctx, c, b.recorder, s)
					}
					q := "SELECT relname FROM (SELECT relname FROM pg_catalog.pg_class UNION ALL SELECT relname FROM pg_catalog.pg_class) d " + clause
					s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: clause + " is not allowed with UNION/INTERSECT/EXCEPT"})
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					s.query = "SELECT v FROM (SELECT v FROM set_lock_base UNION ALL SELECT v FROM set_lock_base) d " + clause
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				}
			}
		}
		if b.pg {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("RESET SESSION AUTHORIZATION", 0))
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("REVOKE SELECT ON set_lock_base FROM "+querySetOpsLockReader, 0))
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET SESSION AUTHORIZATION "+querySetOpsLockReader, 0))
			// Owner-authority views still read through their owner after the
			// reader's base SELECT is revoked, including zero-demand cursors.
			for _, name := range []string{"set_lock_owned", "set_lock_owned_nested"} {
				for _, prepared := range []bool{false, true} {
					s := querySetOpsBag("SELECT v FROM "+name+" FOR UPDATE", "v", "INT4", [][]any{{int64(7)}, {int64(7)}, {int64(9)}, {int64(7)}, {int64(7)}, {int64(9)}})
					s.prepared = prepared
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					s.query, s.rows = "SELECT v FROM "+name+" LIMIT 0 FOR UPDATE", [][]any{}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				}
			}
			for _, name := range []string{"set_lock_invoker", "set_lock_invoker_nested"} {
				for _, tail := range []string{" FOR UPDATE", " LIMIT 0 FOR UPDATE"} {
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError("SELECT v FROM "+name+tail, referenceDiagnostic{Code: "42501", Message: "permission denied for table set_lock_base"})
						s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
						referenceRunStep(t, p.ctx, c, b.recorder, s)
					}
				}
			}
		}
	})
}
