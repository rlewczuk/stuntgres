package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceQueryTiesPersistenceSmoke(t *testing.T) {
	t.Run("definitions", TestReferenceQueryTiesViews)
	t.Run("captured exists", TestReferenceQueryTiesExists)
	t.Run("persistent controls and held cursor", func(t *testing.T) {
		referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range []referenceStep{
				referenceExec("DROP SCHEMA IF EXISTS ties_capture CASCADE", 0), referenceExec("DROP SCHEMA IF EXISTS ties_capture_renamed CASCADE", 0),
				referenceExec("CREATE SCHEMA ties_capture", 0), referenceExec("CREATE TABLE ties_capture.src(id INT,k INT)", 0), referenceExec("INSERT INTO ties_capture.src VALUES(1,1),(2,2),(3,2),(4,3)", 4),
				referenceExec("CREATE SEQUENCE ties_capture.s START WITH 1", 0), referenceExec("CREATE VIEW ties_capture.v AS SELECT id FROM ties_capture.src ORDER BY k OFFSET NULL FETCH FIRST (nextval('ties_capture.s')+1) ROWS WITH TIES", 0),
				referenceExec("CREATE VIEW ties_capture.nested AS SELECT id FROM ties_capture.v ORDER BY id", 0),
				referenceExec("ALTER SCHEMA ties_capture RENAME TO ties_capture_renamed", 0), referenceExec("ALTER TABLE ties_capture_renamed.src RENAME COLUMN k TO renamed_k", 0), referenceExec("ALTER TABLE ties_capture_renamed.src RENAME TO renamed_src", 0), referenceExec("ALTER SEQUENCE ties_capture_renamed.s RENAME TO renamed_s", 0),
			} {
				run(s)
			}
			run(referenceSelect("SELECT is_called FROM ties_capture_renamed.renamed_s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			// Definition export must not execute bounds. Replay exercises structural
			// SQL independently of the native control API on both backends.
			var definition string
			if err := b.db.QueryRowContext(p.ctx, "SELECT pg_get_viewdef('ties_capture_renamed.v'::regclass)").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(definition, "WITH TIES") {
				t.Fatal("definition lost ties", definition)
			}
			run(referenceExec("CREATE VIEW ties_replay AS "+definition, 0))
			run(referenceSelect("SELECT is_called FROM ties_capture_renamed.renamed_s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			var pristine bytes.Buffer
			if !b.pg {
				if err := stuntgres.Dump(p.ctx, p.native, &pristine); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(pristine.Bytes())); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT is_called FROM ties_capture_renamed.renamed_s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			}
			held, err := b.db.QueryContext(p.ctx, "SELECT id FROM ties_capture_renamed.nested ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			columns, err := held.Columns()
			if err != nil || len(columns) != 1 || columns[0] != "id" {
				t.Fatal(columns, err)
			}
			types, err := held.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
				t.Fatal(types, err)
			}
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				run(referenceExec("INSERT INTO ties_capture_renamed.renamed_src VALUES(5,2)", 1))
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"\nCREATE VIEW ties_bad AS SELECT missing FROM ties_capture_renamed.renamed_src;")); err == nil {
					t.Fatal("late invalid restore accepted")
				}
				run(referenceSelect("SELECT id FROM ties_capture_renamed.renamed_src ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4, 5)))
				cancelled, stop := context.WithCancel(p.ctx)
				reader := enumCancelReader{Reader: bytes.NewReader(dump.Bytes()), cancel: stop}
				err := stuntgres.Restore(cancelled, p.native, &reader)
				stop()
				if !errors.Is(err, context.Canceled) {
					t.Fatal("canceled import", err)
				}
				run(referenceSelect("SELECT id FROM ties_capture_renamed.renamed_src ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4, 5)))
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			referenceViewValues(t, relationOutcome{rows: held}, "id", "INT4", 1, 2, 3)
			run(referenceSelect("SELECT id FROM ties_replay ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3)))
			run(referencePrepared(referenceSelect("SELECT id FROM ties_capture_renamed.nested ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4))))
			referenceTiesCanceledBeforeExecution(t, p.ctx, b.db, "SELECT id FROM ties_replay ORDER BY id")
			cancelCtx, stop := context.WithCancel(p.ctx)
			rows, err := b.db.QueryContext(cancelCtx, "SELECT id FROM ties_replay ORDER BY id")
			if err != nil {
				stop()
				t.Fatal(err)
			}
			stop()
			referenceTiesCanceledCursor(t, rows, b.pg)
			run(referenceSelect("SELECT nextval('ties_capture_renamed.renamed_s') AS n", []string{"n"}, []string{"INT8"}, referenceLimitIDs(5)))
			run(referenceExec("DROP VIEW ties_replay", 0))
			run(referenceExec("DROP SCHEMA ties_capture_renamed CASCADE", 0))
		})
	})
}
