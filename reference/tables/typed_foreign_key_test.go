package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func typedForeignKeyState(t *testing.T, p *fixture.Pair, x fixture.Executor, recorder *fixture.Recorder, postgres, prepared, attached bool) {
	t.Helper()
	run := func(s fixture.Step) {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, x, recorder, s)
	}
	rows := fixture.Select("SELECT id FROM t ORDER BY id NULLS LAST", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}, {nil}})
	parentRows := fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}})
	run(rows)
	run(parentRows)
	fixture.TableDDLMetadata(t, p.Ctx, x, postgres, prepared, "SELECT id FROM t ORDER BY id NULLS LAST", []string{"id"}, []string{"INT4"}, []bool{true}, 3)
	fixture.TableDDLMetadata(t, p.Ctx, x, postgres, prepared, "SELECT id FROM t WHERE id=0", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
	fixture.TableDDLMetadata(t, p.Ctx, x, postgres, prepared, "SELECT id FROM p", []string{"id"}, []string{"INT4"}, []bool{false}, 2)
	run(fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='t'::regclass::oid ORDER BY conname", []string{"conname", "contype", "definition"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{"fk", "f", "FOREIGN KEY (id) REFERENCES p(id)"}}))
	typedConstraintAttachmentState(run, attached)
	run(typedTableAttachment("p", "pair", false))
	run(fixture.Select("SELECT 'p'::regclass::oid<>'t'::regclass::oid AS distinct_relation,'p'::regtype::oid<>'t'::regtype::oid AS distinct_row,'p'::regtype::oid<>'pair'::regtype::oid AS distinct_source", []string{"distinct_relation", "distinct_row", "distinct_source"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
	// The FK keeps actual parent-column and supporting-index references,
	// independent of the child table's new standalone composite attachment.
	owner := "classid=2606::oid AND objid=(SELECT oid FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname='fk') AND objsubid=0 AND refclassid='pg_class'::regclass::oid"
	for _, edge := range []struct{ target, kind string }{
		{"refobjid='t'::regclass::oid AND refobjsubid=1", "a"},
		{"refobjid='p'::regclass::oid AND refobjsubid=1", "n"},
		{"refobjid='p_pkey'::regclass::oid AND refobjsubid=0", "n"},
	} {
		run(fixture.Select("SELECT deptype FROM pg_depend WHERE "+owner+" AND "+edge.target, []string{"deptype"}, []string{"CHAR"}, [][]any{{edge.kind}}))
	}
	run(foreignKeyFail("INSERT INTO t VALUES(13)", foreignKeyChild("t", "fk", `Key (id)=(13) is not present in table "p".`), prepared))
	run(foreignKeyFail("DELETE FROM p WHERE id=11", foreignKeyParent("p", "t", "fk", `Key (id)=(11) is still referenced from table "t".`), prepared))
	run(rows)
	run(parentRows)
}

func TestReferenceTypedTableForeignKeyControls(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		setup := []fixture.Step{
			fixture.Exec("CREATE TYPE pair AS (id integer)", 0),
			fixture.Exec("CREATE TABLE p(id integer PRIMARY KEY)", 0),
			fixture.Exec("INSERT INTO p VALUES($1),($2)", 2, int64(11), int64(12)),
			fixture.Exec("CREATE TABLE t(id integer,CONSTRAINT fk FOREIGN KEY(id) REFERENCES p(id))", 0),
			fixture.Exec("INSERT INTO t VALUES($1),($2),($3)", 3, int64(11), int64(12), nil),
		}
		for _, s := range setup {
			run(s)
		}
		typedForeignKeyState(t, p, c, b.Recorder, b.PG, prepared, false)
		before := typedConstraintIdentities(t, p, c, "fk")
		var parent, parentRow, parentIndex int64
		identities := "SELECT 'p'::regclass::oid,'p'::regtype::oid,'p_pkey'::regclass::oid"
		if err := c.QueryRowContext(p.Ctx, identities).Scan(&parent, &parentRow, &parentIndex); err != nil {
			t.Fatal(err)
		}
		attach := fixture.Exec("ALTER TABLE t OF pair", 0)
		typedConstraintUtility(t, p, c, prepared, run, attach.Query, typedTableAttachment("t", "pair", false))
		setup = append(setup, attach)
		typedForeignKeyState(t, p, c, b.Recorder, b.PG, prepared, true)
		if after := typedConstraintIdentities(t, p, c, "fk"); after != before {
			t.Fatal("attachment changed relation/source/own-row/FK identity", before, after)
		}
		var afterParent, afterRow, afterIndex int64
		if err := c.QueryRowContext(p.Ctx, identities).Scan(&afterParent, &afterRow, &afterIndex); err != nil || afterParent != parent || afterRow != parentRow || afterIndex != parentIndex {
			t.Fatal("attachment changed parent/key identities", err)
		}
		state := func(x fixture.Executor, recorder *fixture.Recorder, postgres bool) {
			typedForeignKeyState(t, p, x, recorder, postgres, prepared, true)
		}
		typedConstraintControls(t, p, b, prepared, setup, state, "INSERT INTO t VALUES(13)", foreignKeyChild("t", "fk", `Key (id)=(13) is not present in table "p".`))
	})
}

func TestReferenceTypedTableForeignKeyCreate(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE p(id integer PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO p VALUES($1),($2)", 2, int64(11), int64(12)))
		typedConstraintUtility(t, p, c, prepared, run, "CREATE TABLE t OF pair(CONSTRAINT fk FOREIGN KEY(id) REFERENCES p(id))", fixture.Select("SELECT count(*) AS tables FROM pg_class WHERE relname='t'", []string{"tables"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("INSERT INTO t VALUES($1),($2),($3)", 3, int64(11), int64(12), nil))
		typedForeignKeyState(t, p, c, b.Recorder, b.PG, prepared, true)
		typedConstraintIdentities(t, p, c, "fk")
	})
}
