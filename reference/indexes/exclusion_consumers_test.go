package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExclusionOnConflict(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
				run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE USING "+method+"(v WITH =))", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2) ON CONFLICT DO NOTHING", 1))
				run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(1) ON CONFLICT ON CONSTRAINT ex DO NOTHING", 0)))
				run(fixture.Exec("INSERT INTO t SELECT 2 UNION ALL SELECT 3 ON CONFLICT ON CONSTRAINT ex DO NOTHING", 1))
				d := fixture.ControlsReferenceDiagnostic("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO t VALUES(1) ON CONFLICT(v) DO NOTHING", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
				run(fixture.ExclusionReferenceRows("SELECT v FROM t ORDER BY v", 1, 2, 3))
			})
		})
	}
	for _, target := range []string{"", "ON CONSTRAINT ex"} {
		t.Run("deferred/"+target, func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
				run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", 0))
				d := fixture.ControlsReferenceDiagnostic("55000", "ON CONFLICT does not support deferrable unique constraints/exclusion constraints as arbiters")
				d.Schema, d.Table, d.Constraint = "public", "t", "ex"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO t VALUES(1) ON CONFLICT "+target+" DO NOTHING", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					s = fixture.Exec("INSERT INTO t SELECT 1 WHERE FALSE ON CONFLICT "+target+" DO NOTHING", 0)
					s.Prepared = prepared
					run(s)
				}
				run(fixture.ExclusionReferenceRows("SELECT v FROM t"))
			})
		})
	}
	t.Run("unselected_violation", func(t *testing.T) {
		fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
			run(fixture.Exec("CREATE TABLE t(id INT UNIQUE,v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,1)", 1))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,1) ON CONFLICT(id) DO NOTHING", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
			run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
		})
	})
}

func TestReferenceExclusionWriteConsumers(t *testing.T) {
	for _, q := range []string{
		"INSERT INTO t SELECT 1",
		"UPDATE t SET v=1 WHERE v=2",
		"UPDATE t SET v=s.v FROM (SELECT 1 AS v) s WHERE t.v=2",
		"MERGE INTO t USING (SELECT 2 AS old,1 AS v) s ON t.v=s.old WHEN MATCHED THEN UPDATE SET v=s.v",
		"MERGE INTO t USING (SELECT 1 AS v) s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.v)",
	} {
		t.Run(q, func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
				run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1"))
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.ExclusionReferenceRows("SELECT v FROM t ORDER BY v", 1, 2))
				}
				run(fixture.Exec("DELETE FROM t USING (SELECT 1 AS v) s WHERE t.v=s.v", 1))
				run(fixture.Exec("INSERT INTO t SELECT 1", 1))
			})
		})
	}
}

func TestReferenceExclusionAlterLifecycle(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(fmt.Sprintf("build/valid=%t", valid), func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
				run(fixture.Exec("CREATE TABLE t(v INT,note TEXT)", 0))
				if valid {
					run(fixture.Exec("INSERT INTO t VALUES(1,'a'),(2,'b')", 2))
					run(fixture.Prepared(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0)))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'c')", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
				} else {
					run(fixture.Exec("INSERT INTO t VALUES(1,'a'),(1,'b')", 2))
					run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", fixture.ExclusionReferenceBuild("t", "ex", "v", "1"))))
					run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='ex'", []string{"conname"}, []string{"NAME"}, nil))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ex'", []string{"relname"}, []string{"NAME"}, nil))
					run(fixture.Select("SELECT v,note FROM t ORDER BY note", []string{"v", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}}))
					run(fixture.Exec("DELETE FROM t WHERE note='b'", 1))
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0))
				}
				d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop index ex because constraint ex on table t requires it")
				d.Hint = "You can drop constraint ex on table t instead."
				run(fixture.ErrorDiagnostic("DROP INDEX ex", d))
				run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT ex", 0))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ex'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("INSERT INTO t VALUES(1,'now valid')", 1))
			})
		})
	}
	t.Run("rename_type_schema_dependencies", func(t *testing.T) {
		fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := fixture.ConcurrentIndexConn(t, p, b)
			run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
			run(fixture.Exec("CREATE SCHEMA exclusion_move", 0))
			defer c.ExecContext(p.Ctx, "DROP SCHEMA exclusion_move CASCADE")
			run(fixture.Exec("CREATE TABLE t(v INT,note TEXT,active BOOL,CONSTRAINT ex EXCLUDE(lower(note) WITH =) WHERE(active))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'A',TRUE)", 1))
			run(fixture.Exec("ALTER INDEX ex RENAME TO renamed", 0))
			run(fixture.Exec("ALTER TABLE t RENAME COLUMN note TO label", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,'a',TRUE)", fixture.ExclusionReferenceConflict("t", "renamed", "lower(label)", "a", "a")))
			run(fixture.Exec("ALTER TABLE t ALTER COLUMN label TYPE VARCHAR(20)", 0))
			run(fixture.Exec("ALTER TABLE t SET SCHEMA exclusion_move", 0))
			d := fixture.ExclusionReferenceConflict("t", "renamed", "lower(label::text)", "a", "a")
			d.Schema = "exclusion_move"
			run(fixture.ErrorDiagnostic("INSERT INTO exclusion_move.t VALUES(2,'a',TRUE)", d))
			run(fixture.Exec("ALTER TABLE exclusion_move.t DROP COLUMN active CASCADE", 0))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='renamed'", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Exec("INSERT INTO exclusion_move.t VALUES(2,'a')", 1))
		})
	})
	t.Run("transactional_add_drop", func(t *testing.T) {
		fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := fixture.ConcurrentIndexConn(t, p, b)
			run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
			run(fixture.Exec("CREATE TABLE t(v INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0))
			run(fixture.Exec("SAVEPOINT s", 0))
			run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT ex", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		})
	})
}
