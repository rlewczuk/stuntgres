package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceExclusionOnConflict(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
				run(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE USING "+method+"(v WITH =))", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				run(referenceExec("INSERT INTO t VALUES(1),(2) ON CONFLICT DO NOTHING", 1))
				run(referencePrepared(referenceExec("INSERT INTO t VALUES(1) ON CONFLICT ON CONSTRAINT ex DO NOTHING", 0)))
				run(referenceExec("INSERT INTO t SELECT 2 UNION ALL SELECT 3 ON CONFLICT ON CONSTRAINT ex DO NOTHING", 1))
				d := controlsReferenceDiagnostic("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification")
				for _, prepared := range []bool{false, true} {
					s := referenceErrorDiagnostic("INSERT INTO t VALUES(1) ON CONFLICT(v) DO NOTHING", d)
					s.prepared, s.errorStage = prepared, "execute"
					run(s)
				}
				run(exclusionReferenceRows("SELECT v FROM t ORDER BY v", 1, 2, 3))
			})
		})
	}
	for _, target := range []string{"", "ON CONSTRAINT ex"} {
		t.Run("deferred/"+target, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
				run(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", 0))
				d := controlsReferenceDiagnostic("55000", "ON CONFLICT does not support deferrable unique constraints/exclusion constraints as arbiters")
				d.Schema, d.Table, d.Constraint = "public", "t", "ex"
				for _, prepared := range []bool{false, true} {
					s := referenceErrorDiagnostic("INSERT INTO t VALUES(1) ON CONFLICT "+target+" DO NOTHING", d)
					s.prepared, s.errorStage = prepared, "execute"
					run(s)
					s = referenceExec("INSERT INTO t SELECT 1 WHERE FALSE ON CONFLICT "+target+" DO NOTHING", 0)
					s.prepared = prepared
					run(s)
				}
				run(exclusionReferenceRows("SELECT v FROM t"))
			})
		})
	}
	t.Run("unselected_violation", func(t *testing.T) {
		exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
			run(referenceExec("CREATE TABLE t(id INT UNIQUE,v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
			run(referenceExec("INSERT INTO t VALUES(1,1)", 1))
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(2,1) ON CONFLICT(id) DO NOTHING", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
			run(referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
		})
	})
}

func TestReferenceExclusionWriteConsumers(t *testing.T) {
	for _, consumer := range []struct {
		query   string
		matched bool
	}{
		{"INSERT INTO t SELECT 1", false},
		{"UPDATE t SET v=1 WHERE v=2", false},
		{"UPDATE t SET v=s.v FROM (SELECT 1 AS v) s WHERE t.v=2", false},
		{"MERGE INTO t USING (SELECT 2 AS old,1 AS v) s ON t.v=s.old WHEN MATCHED THEN UPDATE SET v=s.v", true},
		{"MERGE INTO t USING (SELECT 1 AS v) s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.v)", false},
	} {
		q := consumer.query
		t.Run(q, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
				run(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
				run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
				for _, prepared := range []bool{false, true} {
					s := referenceErrorDiagnostic(q, exclusionReferenceConflict("t", "ex", "v", "1", "1"))
					s.prepared, s.errorStage = prepared, "execute"
					if consumer.matched && !b.pg {
						d := controlsReferenceDiagnostic("0A000", "MERGE MATCHED actions are unsupported")
						d.Position = referencePosition(strings.Index(q, "MATCHED") + 1)
						s = referenceErrorDiagnostic(q, d)
						s.prepared = prepared
						s.errorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
					}
					run(s)
					run(exclusionReferenceRows("SELECT v FROM t ORDER BY v", 1, 2))
				}
				run(referenceExec("DELETE FROM t USING (SELECT 1 AS v) s WHERE t.v=s.v", 1))
				run(referenceExec("INSERT INTO t SELECT 1", 1))
			})
		})
	}
}

func TestReferenceExclusionAlterLifecycle(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(fmt.Sprintf("build/valid=%t", valid), func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
				run(referenceExec("CREATE TABLE t(v INT,note TEXT)", 0))
				if valid {
					run(referenceExec("INSERT INTO t VALUES(1,'a'),(2,'b')", 2))
					run(referencePrepared(referenceExec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0)))
					run(referenceErrorDiagnostic("INSERT INTO t VALUES(1,'c')", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
				} else {
					run(referenceExec("INSERT INTO t VALUES(1,'a'),(1,'b')", 2))
					run(referencePrepared(referenceErrorDiagnostic("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", exclusionReferenceBuild("t", "ex", "v", "1"))))
					run(referenceSelect("SELECT conname FROM pg_constraint WHERE conname='ex'", []string{"conname"}, []string{"NAME"}, nil))
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ex'", []string{"relname"}, []string{"NAME"}, nil))
					run(referenceSelect("SELECT v,note FROM t ORDER BY note", []string{"v", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}}))
					run(referenceExec("DELETE FROM t WHERE note='b'", 1))
					run(referenceExec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0))
				}
				d := controlsReferenceDiagnostic("2BP01", "cannot drop index ex because constraint ex on table t requires it")
				d.Hint = "You can drop constraint ex on table t instead."
				run(referenceErrorDiagnostic("DROP INDEX ex", d))
				run(referenceExec("ALTER TABLE t DROP CONSTRAINT ex", 0))
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ex'", []string{"relname"}, []string{"NAME"}, nil))
				run(referenceExec("INSERT INTO t VALUES(1,'now valid')", 1))
			})
		})
	}
	t.Run("rename_type_schema_dependencies", func(t *testing.T) {
		exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			c := concurrentIndexConn(t, p, b)
			run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
			run(referenceExec("CREATE SCHEMA exclusion_move", 0))
			defer c.ExecContext(p.ctx, "DROP SCHEMA exclusion_move CASCADE")
			run(referenceExec("CREATE TABLE t(v INT,note TEXT,active BOOL,CONSTRAINT ex EXCLUDE(lower(note) WITH =) WHERE(active))", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'A',TRUE)", 1))
			run(referenceExec("ALTER INDEX ex RENAME TO renamed", 0))
			run(referenceExec("ALTER TABLE t RENAME COLUMN note TO label", 0))
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(2,'a',TRUE)", exclusionReferenceConflict("t", "renamed", "lower(label)", "a", "a")))
			run(referenceExec("ALTER TABLE t ALTER COLUMN label TYPE VARCHAR(20)", 0))
			run(referenceExec("ALTER TABLE t SET SCHEMA exclusion_move", 0))
			d := exclusionReferenceConflict("t", "renamed", "lower(label::text)", "a", "a")
			d.Schema = "exclusion_move"
			run(referenceErrorDiagnostic("INSERT INTO exclusion_move.t VALUES(2,'a',TRUE)", d))
			run(referenceExec("ALTER TABLE exclusion_move.t DROP COLUMN active CASCADE", 0))
			run(referenceSelect("SELECT conname FROM pg_constraint WHERE conname='renamed'", []string{"conname"}, []string{"NAME"}, nil))
			run(referenceExec("INSERT INTO exclusion_move.t VALUES(2,'a')", 1))
		})
	})
	t.Run("transactional_add_drop", func(t *testing.T) {
		exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			c := concurrentIndexConn(t, p, b)
			run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
			run(referenceExec("CREATE TABLE t(v INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0))
			run(referenceExec("SAVEPOINT s", 0))
			run(referenceExec("ALTER TABLE t DROP CONSTRAINT ex", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceExec("ROLLBACK TO s", 0))
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
			run(referenceExec("ROLLBACK", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
		})
	})
}

func TestReferenceExclusionConstraintRenameBoundary(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
		run(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		query := "ALTER TABLE t RENAME CONSTRAINT ex TO renamed"
		stmt, err := tx.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: tx, statement: stmt, query: query}
		step := referenceExec(query, 0)
		if !b.pg {
			step = referenceErrorDiagnostic(query, controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction"))
			step.errorStage = "execute"
		}
		exclusionReferenceRun(t, p, b, held, step)
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if b.pg {
			exclusionReferenceRun(t, p, b, tx, referenceSelect("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, [][]any{{"renamed"}}))
			exclusionReferenceRun(t, p, b, tx, referenceSelect("SELECT relname FROM pg_class WHERE relname='renamed'", []string{"relname"}, []string{"NAME"}, [][]any{{"renamed"}}))
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(referenceSelect("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, [][]any{{"ex"}}))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='renamed'", []string{"relname"}, []string{"NAME"}, nil))
	})
}
