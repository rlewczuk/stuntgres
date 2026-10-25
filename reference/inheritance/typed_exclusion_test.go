package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func typedExclusionState(t *testing.T, p *fixture.Pair, x fixture.Executor, recorder *fixture.Recorder, postgres, prepared, attached bool) {
	t.Helper()
	run := func(s fixture.Step) {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, x, recorder, s)
	}
	rows := fixture.Select("SELECT id FROM t ORDER BY id NULLS LAST", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}, {nil}})
	run(rows)
	fixture.TableDDLMetadata(t, p.Ctx, x, postgres, prepared, "SELECT id FROM t ORDER BY id NULLS LAST", []string{"id"}, []string{"INT4"}, []bool{true}, 3)
	fixture.TableDDLMetadata(t, p.Ctx, x, postgres, prepared, "SELECT id FROM t WHERE id=0", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
	run(fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='t'::regclass::oid ORDER BY conname", []string{"conname", "contype", "definition"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{"ex", "x", "EXCLUDE USING btree (id WITH =)"}}))
	typedConstraintAttachmentState(run, attached)
	failure := fixture.ErrorDiagnostic("INSERT INTO t VALUES(11)", fixture.ExclusionReferenceConflict("t", "ex", "id", "11", "11"))
	failure.ErrorStage = "execute"
	run(failure)
	run(rows)
}

func TestReferenceTypedTableExclusionAttachmentControls(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		setup := []fixture.Step{
			fixture.Exec("CREATE TYPE pair AS (id integer)", 0),
			fixture.Exec("CREATE TABLE t(id integer,CONSTRAINT ex EXCLUDE USING btree(id WITH =))", 0),
			fixture.Exec("INSERT INTO t VALUES($1),($2),($3)", 3, int64(11), int64(12), nil),
		}
		for _, s := range setup {
			run(s)
		}
		typedExclusionState(t, p, c, b.Recorder, b.PG, prepared, false)
		before := typedConstraintIdentities(t, p, c, "ex")
		attach := fixture.Exec("ALTER TABLE t OF pair", 0)
		typedConstraintUtility(t, p, c, prepared, run, attach.Query, fixture.TypedTableAttachment("t", "pair", false))
		setup = append(setup, attach)
		typedExclusionState(t, p, c, b.Recorder, b.PG, prepared, true)
		if after := typedConstraintIdentities(t, p, c, "ex"); after != before {
			t.Fatal("attachment changed relation/source/own-row/exclusion identity", before, after)
		}
		state := func(x fixture.Executor, recorder *fixture.Recorder, postgres bool) {
			typedExclusionState(t, p, x, recorder, postgres, prepared, true)
		}
		typedConstraintControls(t, p, b, prepared, setup, state, "INSERT INTO t VALUES(11)", fixture.ExclusionReferenceConflict("t", "ex", "id", "11", "11"))
	})
}

func TestReferenceTypedTableExclusionCreate(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		typedConstraintUtility(t, p, c, prepared, run, "CREATE TABLE t OF pair(CONSTRAINT ex EXCLUDE USING btree(id WITH =))", fixture.Select("SELECT count(*) AS tables FROM pg_class WHERE relname='t'", []string{"tables"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("INSERT INTO t VALUES($1),($2),($3)", 3, int64(11), int64(12), nil))
		typedExclusionState(t, p, c, b.Recorder, b.PG, prepared, true)
		typedConstraintIdentities(t, p, c, "ex")
	})
}
