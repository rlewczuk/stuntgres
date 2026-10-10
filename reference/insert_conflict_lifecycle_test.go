package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceInsertConflictHeldLifecycle(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(k INT,p TEXT)", 0))
		run(referenceExec("CREATE UNIQUE INDEX ui ON t(k)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'kept')", 1))
		query := "INSERT INTO t VALUES($1,$2) ON CONFLICT(k) DO NOTHING"
		stmt, err := c.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 0, int64(1), "ignored"))
		run(referenceExec("DROP INDEX ui", 0))
		s := insertConflictNoMatch(query)
		s.args = []any{int64(2), "new"}
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		run(referenceExec("CREATE UNIQUE INDEX replacement ON t(k DESC)", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 1, int64(2), "new"))
		run(referenceExec("ALTER INDEX replacement RENAME TO current_ui", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 0, int64(2), "ignored"))
		run(referenceExec("ALTER TABLE t ALTER k TYPE BIGINT", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 0, int64(1), "ignored"))
		run(referenceExec("ALTER TABLE t RENAME TO renamed", 0))
		run(referenceExec("ALTER TABLE renamed RENAME TO t", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 0, int64(2), "ignored"))
		run(referenceSelect("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "kept"}, {int64(2), "new"}}))
	})
}
func TestReferenceInsertConflictPrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"conflict_actor"})
	insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE ROLE conflict_actor", 0))
		run(referenceExec("CREATE TABLE t(a INT,b INT,p TEXT,active BOOL,CONSTRAINT u UNIQUE(a,b))", 0))
		run(referenceExec("CREATE UNIQUE INDEX ui ON t(a) INCLUDE(p) WHERE active", 0))
		run(referenceExec("INSERT INTO t VALUES(1,2,'secret',TRUE)", 1))
		if b.pg {
			run(referenceExec("GRANT USAGE ON SCHEMA public TO conflict_actor", 0))
		}
		run(referenceExec("SET SESSION AUTHORIZATION conflict_actor", 0))
		denied := controlsReferenceDiagnostic("42501", "permission denied for table t")
		s := referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT DO NOTHING", denied))
		s.errorStage = "execute"
		run(s)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		if !b.pg {
			return
		} // Native actual ACL grants are supplemental catalog units, not SQL GRANT.
		run(referenceExec("GRANT INSERT ON t TO conflict_actor", 0))
		run(referenceExec("SET SESSION AUTHORIZATION conflict_actor", 0))
		run(referenceExec("INSERT INTO t VALUES(1,2,'x',TRUE),(2,3,'kept',FALSE) ON CONFLICT DO NOTHING", 1))
		for _, target := range []string{"(a,b)", "ON CONSTRAINT u", "(a) WHERE active"} {
			s := referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT "+target+" DO NOTHING", denied))
			s.errorStage = "execute"
			run(s)
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT(a,b) ON t TO conflict_actor", 0))
		run(referenceExec("SET SESSION AUTHORIZATION conflict_actor", 0))
		run(referenceExec("INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT ON CONSTRAINT u DO NOTHING", 0))
		s = referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT(a) WHERE active DO NOTHING", denied))
		s.errorStage = "execute"
		run(s)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT(active) ON t TO conflict_actor", 0))
		run(referenceExec("SET SESSION AUTHORIZATION conflict_actor", 0))
		query := "INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT(a) WHERE active DO NOTHING"
		stmt, err := c.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 0)) // no SELECT(p), no UPDATE privilege
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("REVOKE SELECT(active) ON t FROM conflict_actor", 0))
		run(referenceExec("SET SESSION AUTHORIZATION conflict_actor", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceErrorDiagnostic(query, denied))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceSelect("SELECT a,b,p FROM t ORDER BY a", []string{"a", "b", "p"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "secret"}, {int64(2), int64(3), "kept"}}))
	})
}
func TestReferenceInsertConflictNativeControls(t *testing.T) {
	insertConflictScenario(t, insertConflictControls)
}
func insertConflictControls(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
	run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v TEXT,active BOOL,p TEXT)", 0))
	run(referenceExec("CREATE UNIQUE INDEX CONCURRENTLY expr ON t(lower(v) COLLATE \"C\" text_ops) INCLUDE(p)", 0))
	run(referenceExec("CREATE UNIQUE INDEX subset_index ON t(p) WHERE active", 0))
	run(referenceExec("INSERT INTO t VALUES(1,'A',TRUE,'kept')", 1))
	query := "INSERT INTO t VALUES(1,'a',TRUE,'ignored') ON CONFLICT DO NOTHING"
	stmt, err := b.db.PrepareContext(p.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: query}
	referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 0))
	if !b.pg {
		retained, err := b.db.QueryContext(p.ctx, "SELECT k,p FROM t ORDER BY k")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, definition := range []string{"UNIQUE", "lower", "INCLUDE", "WHERE"} {
			if !strings.Contains(dump.String(), definition) {
				t.Fatal("missing provider", definition, dump.String())
			}
		}
		if strings.Contains(dump.String(), "ignored") {
			t.Fatal("dump serialized omitted candidate")
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		cols, err := retained.Columns()
		if err != nil || len(cols) != 2 || cols[0] != "k" || cols[1] != "p" {
			t.Fatal(cols, err)
		}
		types, err := retained.ColumnTypes()
		if err != nil || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
			t.Fatal(types, err)
		}
		if !retained.Next() {
			t.Fatal("retained row lost", retained.Err())
		}
		var k any
		var payload any
		if err := retained.Scan(&k, &payload); err != nil || k != int64(1) || payload != "kept" {
			t.Fatal(k, payload, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained tail", retained.Err())
		}
		retained.Close()
		bad := "CREATE TABLE replacement(k INT UNIQUE); INSERT INTO replacement VALUES(1) ON CONFLICT DO NOTHING;"
		err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad))
		want := controlsReferenceDiagnostic("0A000", "restore INSERT conflict clauses are unsupported")
		referenceAssertDiagnostic(t, err, &want)
		ctx, cancel := context.WithCancel(p.ctx)
		err = stuntgres.Restore(ctx, p.native, &catalogControlCancelReader{cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		failure := errors.New("insert conflict restore reader failure")
		err = stuntgres.Restore(p.ctx, p.native, catalogControlFailingReader{failure})
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(k INT UNIQUE); INSERT INTO replacement VALUES(1),(1);"))
		want = uniqueReferenceDuplicate("replacement", "replacement_k_key", "Key (k)=(1) already exists.")
		referenceAssertDiagnostic(t, err, &want)
		var preserved bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &preserved); err != nil || preserved.String() != dump.String() {
			t.Fatal("failed Restore changed persistent state", err, preserved.String())
		}
	}
	referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 0))
	run(referenceExec("INSERT INTO t VALUES(2,'a',FALSE,'different') ON CONFLICT(lower(v)) DO NOTHING", 0))
	run(referenceExec("INSERT INTO t VALUES(2,'B',FALSE,'kept') ON CONFLICT(p) WHERE active DO NOTHING", 1))
	run(referenceExec("INSERT INTO t VALUES(1,'C',TRUE,'new') ON CONFLICT ON CONSTRAINT u DO NOTHING", 0))
	run(referenceSelect("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}, {int64(2), "kept"}}))
}
func TestReferenceInsertConflictFeatureSmoke(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE,p TEXT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'old')", 1))
		run(referenceExec("INSERT INTO t VALUES(1,'ignored'),(2,'kept') ON CONFLICT DO NOTHING", 1))
		query := "INSERT INTO t VALUES(1,'ignored'),(2,'ignored') ON CONFLICT(k) DO NOTHING"
		stmt, err := b.db.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: query}, b.recorder, referenceExec(query, 0))
		run(referenceExec("INSERT INTO t VALUES(2,'ignored'),(3,NULL) ON CONFLICT ON CONSTRAINT u DO NOTHING", 1))
		run(referenceSelect("INSERT INTO t VALUES(1,'ignored') ON CONFLICT DO NOTHING", nil, nil, nil))
		run(referenceSelect("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "kept"}, {int64(3), nil}}))
		run(referenceExec("CREATE UNIQUE INDEX payload_unique ON t(p)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'kept') ON CONFLICT(k) DO NOTHING", 0))
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(4,'free'),(5,'old') ON CONFLICT(k) DO NOTHING", uniqueReferenceDuplicate("t", "payload_unique", "Key (p)=(old) already exists.")))
		run(referenceSelect("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "kept"}, {int64(3), nil}}))
		run(referenceExec("CREATE TABLE partial(k INT,n INT,active BOOL)", 0))
		run(referenceExec("CREATE UNIQUE INDEX pi ON partial(k) WHERE active AND n>0", 0))
		run(referenceExec("INSERT INTO partial VALUES(1,20,TRUE)", 1))
		run(referenceExec("INSERT INTO partial VALUES(1,20,TRUE),(1,20,FALSE),(1,20,NULL) ON CONFLICT(k) WHERE active AND n>10 DO NOTHING", 2))
		run(insertConflictNoMatch("INSERT INTO partial VALUES(2,0,TRUE) ON CONFLICT(k) WHERE active AND n>=0 DO NOTHING"))
		run(referenceSelect("SELECT count(*) AS n FROM partial", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(referenceExec("CREATE TABLE truth_smoke(k INT,active BOOL)", 0))
		run(referenceExec("CREATE UNIQUE INDEX truth_smoke_ui ON truth_smoke(k) WHERE active IS NOT NULL", 0))
		run(referenceExec("INSERT INTO truth_smoke VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3))
		for _, prepared := range []bool{false, true} {
			negative := insertConflictNoMatch("INSERT INTO truth_smoke VALUES(4,FALSE),(2,FALSE) ON CONFLICT(k) WHERE NOT (active IS TRUE) DO NOTHING")
			negative.prepared, negative.errorStage = prepared, "execute"
			run(negative)
			run(referenceSelect("SELECT k,active FROM truth_smoke ORDER BY k", []string{"k", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}))
		}
		run(referenceExec("DROP TABLE t", 0))
		insertConflictControls(t, p, b)
		a, c := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE snapshot_smoke(k INT CONSTRAINT snapshot_u UNIQUE)", 0)
		referenceLockExec(t, p.ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		referenceRunStep(t, p.ctx, a, b.recorder, insertConflictRows("SELECT k FROM snapshot_smoke"))
		referenceLockExec(t, p.ctx, c, "INSERT INTO snapshot_smoke VALUES(1)", 1)
		referenceRunStep(t, p.ctx, a, b.recorder, referenceErrorDiagnostic("INSERT INTO snapshot_smoke VALUES(1) ON CONFLICT DO NOTHING", controlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")))
		referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO snapshot_smoke VALUES(1) ON CONFLICT DO NOTHING", 0)
		referenceRunStep(t, p.ctx, a, b.recorder, referenceErrorDiagnostic("INSERT INTO snapshot_smoke VALUES(1)", uniqueReferenceDuplicate("snapshot_smoke", "snapshot_u", "Key (k)=(1) already exists.")))
		referenceRunStep(t, p.ctx, a, b.recorder, insertConflictRows("SELECT k FROM snapshot_smoke", []any{int64(1)}))
	})
}
