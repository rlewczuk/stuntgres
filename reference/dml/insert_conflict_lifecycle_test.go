package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictHeldLifecycle(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT,p TEXT)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX ui ON t(k)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'kept')", 1))
		query := "INSERT INTO t VALUES($1,$2) ON CONFLICT(k) DO NOTHING"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0, int64(1), "ignored"))
		run(fixture.Exec("DROP INDEX ui", 0))
		s := insertConflictNoMatch(query)
		s.Args = []any{int64(2), "new"}
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Exec("CREATE UNIQUE INDEX replacement ON t(k DESC)", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, int64(2), "new"))
		run(fixture.Exec("ALTER INDEX replacement RENAME TO current_ui", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0, int64(2), "ignored"))
		run(fixture.Exec("ALTER TABLE t ALTER k TYPE BIGINT", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0, int64(1), "ignored"))
		run(fixture.Exec("ALTER TABLE t RENAME TO renamed", 0))
		run(fixture.Exec("ALTER TABLE renamed RENAME TO t", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0, int64(2), "ignored"))
		run(fixture.Select("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "kept"}, {int64(2), "new"}}))
	})
}
func TestReferenceInsertConflictPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"conflict_actor"})
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE conflict_actor", 0))
		run(fixture.Exec("CREATE TABLE t(a INT,b INT,p TEXT,active BOOL,CONSTRAINT u UNIQUE(a,b))", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX ui ON t(a) INCLUDE(p) WHERE active", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,2,'secret',TRUE)", 1))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO conflict_actor", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION conflict_actor", 0))
		denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t")
		s := fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT DO NOTHING", denied))
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT INSERT ON t TO conflict_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION conflict_actor", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,2,'x',TRUE),(2,3,'kept',FALSE) ON CONFLICT DO NOTHING", 1))
		for _, target := range []string{"(a,b)", "ON CONSTRAINT u", "(a) WHERE active"} {
			s := fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT "+target+" DO NOTHING", denied))
			s.ErrorStage = "execute"
			run(s)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(a,b) ON t TO conflict_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION conflict_actor", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT ON CONSTRAINT u DO NOTHING", 0))
		s = fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT(a) WHERE active DO NOTHING", denied))
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(active) ON t TO conflict_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION conflict_actor", 0))
		query := "INSERT INTO t VALUES(1,2,'x',TRUE) ON CONFLICT(a) WHERE active DO NOTHING"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0)) // no SELECT(p), no UPDATE privilege
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(active) ON t FROM conflict_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION conflict_actor", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(query, denied))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT a,b,p FROM t ORDER BY a", []string{"a", "b", "p"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "secret"}, {int64(2), int64(3), "kept"}}))
	})
}
func TestReferenceInsertConflictNativeControls(t *testing.T) {
	insertConflictScenario(t, insertConflictControls)
}
func insertConflictControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v TEXT,active BOOL,p TEXT)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX CONCURRENTLY expr ON t(lower(v) COLLATE \"C\" text_ops) INCLUDE(p)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX subset_index ON t(p) WHERE active", 0))
	run(fixture.Exec("INSERT INTO t VALUES(1,'A',TRUE,'kept')", 1))
	query := "INSERT INTO t VALUES(1,'a',TRUE,'ignored') ON CONFLICT DO NOTHING"
	stmt, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0))
	if !b.PG {
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT k,p FROM t ORDER BY k")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
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
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
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
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
		want := fixture.ControlsReferenceDiagnostic("0A000", "restore INSERT conflict clauses are unsupported")
		fixture.AssertDiagnostic(t, err, &want)
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		failure := errors.New("insert conflict restore reader failure")
		err = stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{failure})
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(k INT UNIQUE); INSERT INTO replacement VALUES(1),(1);"))
		want = fixture.UniqueReferenceDuplicate("replacement", "replacement_k_key", "Key (k)=(1) already exists.")
		fixture.AssertDiagnostic(t, err, &want)
		var preserved bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &preserved); err != nil || preserved.String() != dump.String() {
			t.Fatal("failed Restore changed persistent state", err, preserved.String())
		}
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0))
	run(fixture.Exec("INSERT INTO t VALUES(2,'a',FALSE,'different') ON CONFLICT(lower(v)) DO NOTHING", 0))
	run(fixture.Exec("INSERT INTO t VALUES(2,'B',FALSE,'kept') ON CONFLICT(p) WHERE active DO NOTHING", 1))
	run(fixture.Exec("INSERT INTO t VALUES(1,'C',TRUE,'new') ON CONFLICT ON CONSTRAINT u DO NOTHING", 0))
	run(fixture.Select("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}, {int64(2), "kept"}}))
}
func TestReferenceInsertConflictFeatureSmoke(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE,p TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
		run(fixture.Exec("INSERT INTO t VALUES(1,'ignored'),(2,'kept') ON CONFLICT DO NOTHING", 1))
		query := "INSERT INTO t VALUES(1,'ignored'),(2,'ignored') ON CONFLICT(k) DO NOTHING"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}, b.Recorder, fixture.Exec(query, 0))
		run(fixture.Exec("INSERT INTO t VALUES(2,'ignored'),(3,NULL) ON CONFLICT ON CONSTRAINT u DO NOTHING", 1))
		run(fixture.Select("INSERT INTO t VALUES(1,'ignored') ON CONFLICT DO NOTHING", nil, nil, nil))
		run(fixture.Select("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "kept"}, {int64(3), nil}}))
		run(fixture.Exec("CREATE UNIQUE INDEX payload_unique ON t(p)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'kept') ON CONFLICT(k) DO NOTHING", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,'free'),(5,'old') ON CONFLICT(k) DO NOTHING", fixture.UniqueReferenceDuplicate("t", "payload_unique", "Key (p)=(old) already exists.")))
		run(fixture.Select("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "kept"}, {int64(3), nil}}))
		run(fixture.Exec("CREATE TABLE partial(k INT,n INT,active BOOL)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pi ON partial(k) WHERE active AND n>0", 0))
		run(fixture.Exec("INSERT INTO partial VALUES(1,20,TRUE)", 1))
		run(fixture.Exec("INSERT INTO partial VALUES(1,20,TRUE),(1,20,FALSE),(1,20,NULL) ON CONFLICT(k) WHERE active AND n>10 DO NOTHING", 2))
		run(insertConflictNoMatch("INSERT INTO partial VALUES(2,0,TRUE) ON CONFLICT(k) WHERE active AND n>=0 DO NOTHING"))
		run(fixture.Select("SELECT count(*) AS n FROM partial", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Exec("CREATE TABLE truth_smoke(k INT,active BOOL)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX truth_smoke_ui ON truth_smoke(k) WHERE active IS NOT NULL", 0))
		run(fixture.Exec("INSERT INTO truth_smoke VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3))
		for _, prepared := range []bool{false, true} {
			negative := insertConflictNoMatch("INSERT INTO truth_smoke VALUES(4,FALSE),(2,FALSE) ON CONFLICT(k) WHERE NOT (active IS TRUE) DO NOTHING")
			negative.Prepared, negative.ErrorStage = prepared, "execute"
			run(negative)
			run(fixture.Select("SELECT k,active FROM truth_smoke ORDER BY k", []string{"k", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}))
		}
		run(fixture.Exec("DROP TABLE t", 0))
		insertConflictControls(t, p, b)
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE snapshot_smoke(k INT CONSTRAINT snapshot_u UNIQUE)", 0)
		fixture.LockExec(t, p.Ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, insertConflictRows("SELECT k FROM snapshot_smoke"))
		fixture.LockExec(t, p.Ctx, c, "INSERT INTO snapshot_smoke VALUES(1)", 1)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO snapshot_smoke VALUES(1) ON CONFLICT DO NOTHING", fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")))
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO snapshot_smoke VALUES(1) ON CONFLICT DO NOTHING", 0)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO snapshot_smoke VALUES(1)", fixture.UniqueReferenceDuplicate("snapshot_smoke", "snapshot_u", "Key (k)=(1) already exists.")))
		fixture.RunStep(t, p.Ctx, a, b.Recorder, insertConflictRows("SELECT k FROM snapshot_smoke", []any{int64(1)}))
	})
}
