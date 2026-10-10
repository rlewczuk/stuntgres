package reference_test

import "testing"

func TestReferencePGDependRenameStableAttributes(t *testing.T) {
	schemaReferenceCleanup(t, "pgdep_rename")
	schemaReferenceCleanup(t, "pgdep_moved")
	schemaReferenceCleanup(t, "pgdep_final")
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE SCHEMA pgdep_rename", "CREATE SCHEMA pgdep_moved", "CREATE TABLE pgdep_rename.t(prefix INT,a INT,b INT)", "CREATE INDEX ix ON pgdep_rename.t(a)", "CREATE VIEW pgdep_rename.v AS SELECT a FROM pgdep_rename.t")
		table := pgDependRelation(t, b, "pgdep_rename.t")
		idx := pgDependRelation(t, b, "pgdep_rename.ix")
		v := pgDependRelation(t, b, "pgdep_rename.v")
		r := pgDependRule(t, b, v)
		pgDependExec(t, p, b, "ALTER TABLE pgdep_rename.t DROP prefix", "ALTER TABLE pgdep_rename.t RENAME a TO renamed", "ALTER TABLE pgdep_rename.t ADD fresh INT", "ALTER TABLE pgdep_rename.t RENAME TO renamed_table", "ALTER INDEX pgdep_rename.ix RENAME TO renamed_index", "ALTER VIEW pgdep_rename.v RENAME TO renamed_view", "ALTER TABLE pgdep_rename.renamed_table SET SCHEMA pgdep_moved", "ALTER SCHEMA pgdep_moved RENAME TO pgdep_final")
		if pgDependRelation(t, b, "pgdep_final.renamed_table") != table || pgDependRelation(t, b, "pgdep_final.renamed_index") != idx {
			t.Fatal("rename reminted identity")
		}
		ns := pgDependNamespace(t, b, table)
		pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
		pgDependBag(t, p, b, 1259, idx, pgDependEdge(1259, idx, 0, 1259, table, 2, "a"))
		pgDependBag(t, p, b, 2618, r, pgDependEdge(2618, r, 0, 1259, v, 0, "i"), pgDependEdge(2618, r, 0, 1259, table, 2, "n"))
		pgDependExec(t, p, b, "CREATE INDEX pgdep_fresh ON pgdep_final.renamed_table(fresh)")
		fresh := pgDependRelation(t, b, "pgdep_final.pgdep_fresh")
		pgDependBag(t, p, b, 1259, fresh, pgDependEdge(1259, fresh, 0, 1259, table, 4, "a"))
	})
}
func TestReferencePGDependLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE TABLE pgdep_life(a INT,b INT DEFAULT 1,c INT GENERATED ALWAYS AS(a+1) STORED,id INT GENERATED ALWAYS AS IDENTITY)", "CREATE SEQUENCE pgdep_owned", "ALTER SEQUENCE pgdep_owned OWNED BY pgdep_life.a", "CREATE VIEW pgdep_life_v AS SELECT a FROM pgdep_life")
		table := pgDependRelation(t, b, "pgdep_life")
		seq := pgDependRelation(t, b, "pgdep_owned")
		d := pgDependDefinition(t, b, table, 2)
		v := pgDependRelation(t, b, "pgdep_life_v")
		r := pgDependRule(t, b, v)
		identity := pgDependRelation(t, b, "pgdep_life_id_seq")
		ns := pgDependNamespace(t, b, seq)
		pgDependExec(t, p, b, "ALTER SEQUENCE pgdep_owned OWNED BY pgdep_life.b")
		pgDependBag(t, p, b, 1259, seq, pgDependEdge(1259, seq, 0, 2615, ns, 0, "n"), pgDependEdge(1259, seq, 0, 1259, table, 2, "a"))
		pgDependExec(t, p, b, "ALTER SEQUENCE pgdep_owned OWNED BY NONE")
		pgDependBag(t, p, b, 1259, seq, pgDependEdge(1259, seq, 0, 2615, ns, 0, "n"))
		pgDependExec(t, p, b, "ALTER TABLE pgdep_life ALTER b SET DEFAULT 2")
		fresh := pgDependDefinition(t, b, table, 2)
		if fresh == d {
			t.Fatal("SET DEFAULT kept obsolete definition")
		}
		pgDependBag(t, p, b, 2604, d)
		pgDependBag(t, p, b, 2604, fresh, pgDependEdge(2604, fresh, 0, 1259, table, 2, "a"))
		pgDependExec(t, p, b, "ALTER TABLE pgdep_life ALTER b DROP DEFAULT", "CREATE OR REPLACE VIEW pgdep_life_v AS SELECT b AS a FROM pgdep_life")
		pgDependBag(t, p, b, 2604, fresh)
		if pgDependRule(t, b, v) != r {
			t.Fatal("replacement reminted rule")
		}
		pgDependBag(t, p, b, 2618, r, pgDependEdge(2618, r, 0, 1259, v, 0, "i"), pgDependEdge(2618, r, 0, 1259, table, 2, "n"))
		pgDependExec(t, p, b, "ALTER TABLE pgdep_life ALTER id DROP IDENTITY")
		pgDependBag(t, p, b, 1259, identity)
		pgDependError(t, p, b, "ALTER TABLE pgdep_life DROP a", pgDependRestrict("column a of table pgdep_life", "column c of table pgdep_life depends on column a of table pgdep_life"))
		pgDependExec(t, p, b, "ALTER TABLE pgdep_life DROP a CASCADE", "DROP VIEW pgdep_life_v", "DROP TABLE pgdep_life")
		pgDependBag(t, p, b, 2618, r)
		pgDependBag(t, p, b, 1259, table)
		pgDependBag(t, p, b, 1259, seq, pgDependEdge(1259, seq, 0, 2615, ns, 0, "n"))
	})
}
func TestReferencePGDependSchemaClosure(t *testing.T) {
	for _, owner := range []string{"default", "check", "generated", "index", "view"} {
		t.Run(owner, func(t *testing.T) {
			schemaReferenceCleanup(t, "pgdep_provider")
			schemaReferenceCleanup(t, "pgdep_consumer")
			p := newReferenceScenario(t)
			pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
				pgDependExec(t, p, b, "CREATE SCHEMA pgdep_provider", "CREATE SCHEMA pgdep_consumer", "CREATE TABLE pgdep_provider.p(a INT)", "SET search_path TO pgdep_consumer,public")
				provider := pgDependRelation(t, b, "pgdep_provider.p")
				pgDependExec(t, p, b, pgDependOwnerStatements(owner, "pgdep_consumer.t", "'pgdep_provider.p'::regclass IS NOT NULL")...)
				table := pgDependRelation(t, b, "pgdep_consumer.t")
				class, id := pgDependOwnerIdentity(t, b, owner, table)
				pgDependExec(t, p, b, "DROP SCHEMA pgdep_provider CASCADE")
				pgDependBag(t, p, b, class, id)
				pgDependBag(t, p, b, 1259, provider)
				pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, pgDependNamespace(t, b, table), 0, "n"))
			})
		})
	}
}
