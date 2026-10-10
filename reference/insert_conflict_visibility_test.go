package reference_test

import "testing"

func TestReferenceInsertConflictReadCommittedInvisibleConflict(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c := referenceLockConnections(t, p.ctx, b.db)
		peer := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(k INT UNIQUE)", 0))
		run(referenceExec("CREATE TABLE source(k INT)", 0))
		run(referenceExec("INSERT INTO source VALUES(1)", 1))
		run(referenceExec("CREATE TABLE witness(k INT)", 0))
		run(referenceExec("INSERT INTO witness VALUES(1)", 1))
		run(referenceExec("CREATE SEQUENCE s", 0))
		query := "INSERT INTO t SELECT a.k+0*nextval('s')::INT AS k FROM source a JOIN witness b ON a.k=b.k ORDER BY k FOR UPDATE OF a ON CONFLICT DO NOTHING"
		stmt, err := c.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		holder, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		referenceRunStep(t, p.ctx, holder, b.recorder, referenceSelect("SELECT k FROM source FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		var ap, cp int
		if b.pg {
			ap, cp = referenceRowPIDs(t, p.ctx, a, c)
		}
		done := referenceRowAsync(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}, query, false, false)
		if b.pg {
			referenceRowWait(t, p.ctx, cp, ap)
		} else {
			// JOIN's volatile ORDER output is an existing consuming frontier before
			// the physical rowmark. Its positive public effect proves capture of the
			// pre-peer-commit source candidate, not an invented native wait catalog.
			referenceLimitSequenceCapture(t, p.ctx, holder)
		}
		referenceLockExec(t, p.ctx, peer, "INSERT INTO t VALUES(1)", 1)
		if err := holder.Commit(); err != nil {
			t.Fatal(err)
		}
		result := referenceLockResult(t, done)
		referenceState(t, result.err, "")
		referenceLockAffected(t, result.result, 0)
		run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
	})
}
func TestReferenceInsertConflictPlainRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) {
					s.nonContext = true
					s.prepared = prepared
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				}
				run(referenceExec("CREATE TABLE conflict(k INT CONSTRAINT u UNIQUE)", 0))
				run(referenceExec("INSERT INTO conflict VALUES(1)", 1))
				run(referenceExec("INSERT INTO conflict VALUES(1),(2) ON CONFLICT DO NOTHING", 1))
				run(referenceExec("INSERT INTO conflict VALUES(1),(2) ON CONFLICT ON CONSTRAINT u DO NOTHING", 0))
				run(insertConflictRows("SELECT k FROM conflict ORDER BY k", []any{int64(1)}, []any{int64(2)}))
			})
		})
	}
}
