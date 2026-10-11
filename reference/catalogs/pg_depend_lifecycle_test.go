package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependRenameStableAttributes(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgdep_rename")
	fixture.SchemaReferenceCleanup(t, "pgdep_moved")
	fixture.SchemaReferenceCleanup(t, "pgdep_final")
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE SCHEMA pgdep_rename", "CREATE SCHEMA pgdep_moved", "CREATE TABLE pgdep_rename.t(prefix INT,a INT,b INT)", "CREATE INDEX ix ON pgdep_rename.t(a)", "CREATE VIEW pgdep_rename.v AS SELECT a FROM pgdep_rename.t")
		table := fixture.PgDependRelation(t, b, "pgdep_rename.t")
		idx := fixture.PgDependRelation(t, b, "pgdep_rename.ix")
		v := fixture.PgDependRelation(t, b, "pgdep_rename.v")
		r := fixture.PgDependRule(t, b, v)
		fixture.PgDependExec(t, p, b, "ALTER TABLE pgdep_rename.t DROP prefix", "ALTER TABLE pgdep_rename.t RENAME a TO renamed", "ALTER TABLE pgdep_rename.t ADD fresh INT", "ALTER TABLE pgdep_rename.t RENAME TO renamed_table", "ALTER INDEX pgdep_rename.ix RENAME TO renamed_index", "ALTER VIEW pgdep_rename.v RENAME TO renamed_view", "ALTER TABLE pgdep_rename.renamed_table SET SCHEMA pgdep_moved", "ALTER SCHEMA pgdep_moved RENAME TO pgdep_final")
		if fixture.PgDependRelation(t, b, "pgdep_final.renamed_table") != table || fixture.PgDependRelation(t, b, "pgdep_final.renamed_index") != idx {
			t.Fatal("rename reminted identity")
		}
		ns := fixture.PgDependNamespace(t, b, table)
		fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
		fixture.PgDependBag(t, p, b, 1259, idx, fixture.PgDependEdge(1259, idx, 0, 1259, table, 2, "a"))
		fixture.PgDependBag(t, p, b, 2618, r, fixture.PgDependEdge(2618, r, 0, 1259, v, 0, "i"), fixture.PgDependEdge(2618, r, 0, 1259, table, 2, "n"))
		fixture.PgDependExec(t, p, b, "CREATE INDEX pgdep_fresh ON pgdep_final.renamed_table(fresh)")
		fresh := fixture.PgDependRelation(t, b, "pgdep_final.pgdep_fresh")
		fixture.PgDependBag(t, p, b, 1259, fresh, fixture.PgDependEdge(1259, fresh, 0, 1259, table, 4, "a"))
	})
}
func TestReferencePGDependLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_life(a INT,b INT DEFAULT 1,c INT GENERATED ALWAYS AS(a+1) STORED,id INT GENERATED ALWAYS AS IDENTITY)", "CREATE SEQUENCE pgdep_owned", "ALTER SEQUENCE pgdep_owned OWNED BY pgdep_life.a", "CREATE VIEW pgdep_life_v AS SELECT a FROM pgdep_life")
		table := fixture.PgDependRelation(t, b, "pgdep_life")
		seq := fixture.PgDependRelation(t, b, "pgdep_owned")
		d := fixture.PgDependDefinition(t, b, table, 2)
		v := fixture.PgDependRelation(t, b, "pgdep_life_v")
		r := fixture.PgDependRule(t, b, v)
		identity := fixture.PgDependRelation(t, b, "pgdep_life_id_seq")
		ns := fixture.PgDependNamespace(t, b, seq)
		fixture.PgDependExec(t, p, b, "ALTER SEQUENCE pgdep_owned OWNED BY pgdep_life.b")
		fixture.PgDependBag(t, p, b, 1259, seq, fixture.PgDependEdge(1259, seq, 0, 2615, ns, 0, "n"), fixture.PgDependEdge(1259, seq, 0, 1259, table, 2, "a"))
		fixture.PgDependExec(t, p, b, "ALTER SEQUENCE pgdep_owned OWNED BY NONE")
		fixture.PgDependBag(t, p, b, 1259, seq, fixture.PgDependEdge(1259, seq, 0, 2615, ns, 0, "n"))
		fixture.PgDependExec(t, p, b, "ALTER TABLE pgdep_life ALTER b SET DEFAULT 2")
		fresh := fixture.PgDependDefinition(t, b, table, 2)
		if fresh == d {
			t.Fatal("SET DEFAULT kept obsolete definition")
		}
		fixture.PgDependBag(t, p, b, 2604, d)
		fixture.PgDependBag(t, p, b, 2604, fresh, fixture.PgDependEdge(2604, fresh, 0, 1259, table, 2, "a"))
		fixture.PgDependExec(t, p, b, "ALTER TABLE pgdep_life ALTER b DROP DEFAULT", "CREATE OR REPLACE VIEW pgdep_life_v AS SELECT b AS a FROM pgdep_life")
		fixture.PgDependBag(t, p, b, 2604, fresh)
		if fixture.PgDependRule(t, b, v) != r {
			t.Fatal("replacement reminted rule")
		}
		fixture.PgDependBag(t, p, b, 2618, r, fixture.PgDependEdge(2618, r, 0, 1259, v, 0, "i"), fixture.PgDependEdge(2618, r, 0, 1259, table, 2, "n"))
		fixture.PgDependExec(t, p, b, "ALTER TABLE pgdep_life ALTER id DROP IDENTITY")
		fixture.PgDependBag(t, p, b, 1259, identity)
		fixture.PgDependError(t, p, b, "ALTER TABLE pgdep_life DROP a", fixture.PgDependRestrict("column a of table pgdep_life", "column c of table pgdep_life depends on column a of table pgdep_life"))
		fixture.PgDependExec(t, p, b, "ALTER TABLE pgdep_life DROP a CASCADE", "DROP VIEW pgdep_life_v", "DROP TABLE pgdep_life")
		fixture.PgDependBag(t, p, b, 2618, r)
		fixture.PgDependBag(t, p, b, 1259, table)
		fixture.PgDependBag(t, p, b, 1259, seq, fixture.PgDependEdge(1259, seq, 0, 2615, ns, 0, "n"))
	})
}
func TestReferencePGDependSchemaClosure(t *testing.T) {
	for _, owner := range []string{"default", "check", "generated", "index", "view"} {
		t.Run(owner, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "pgdep_provider")
			fixture.SchemaReferenceCleanup(t, "pgdep_consumer")
			p := fixture.NewScenario(t)
			fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
				fixture.PgDependExec(t, p, b, "CREATE SCHEMA pgdep_provider", "CREATE SCHEMA pgdep_consumer", "CREATE TABLE pgdep_provider.p(a INT)", "SET search_path TO pgdep_consumer,public")
				provider := fixture.PgDependRelation(t, b, "pgdep_provider.p")
				fixture.PgDependExec(t, p, b, pgDependOwnerStatements(owner, "pgdep_consumer.t", "'pgdep_provider.p'::regclass IS NOT NULL")...)
				table := fixture.PgDependRelation(t, b, "pgdep_consumer.t")
				class, id := pgDependOwnerIdentity(t, b, owner, table)
				fixture.PgDependExec(t, p, b, "DROP SCHEMA pgdep_provider CASCADE")
				fixture.PgDependBag(t, p, b, class, id)
				fixture.PgDependBag(t, p, b, 1259, provider)
				fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, fixture.PgDependNamespace(t, b, table), 0, "n"))
			})
		})
	}
}
