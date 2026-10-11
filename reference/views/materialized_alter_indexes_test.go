package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func alterIndexSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE index_source(i INT,s TEXT)", 0))
	run(fixture.Exec("INSERT INTO index_source VALUES(1,'a'),(2,NULL)", 2))
	run(fixture.Exec("CREATE MATERIALIZED VIEW indexed AS SELECT i,s FROM index_source", 2))
	run(fixture.Exec("CREATE UNIQUE INDEX mv_u ON indexed(i) INCLUDE(s)", 0))
}
func TestReferenceMaterializedAlterIndexes(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_index_owner"})
	fixture.SchemaReferenceCleanup(t, "alter_index_s2")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			alterIndexSeed(run)
			var parent, index int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'indexed'::regclass::oid,'mv_u'::regclass::oid").Scan(&parent, &index); err != nil {
				t.Fatal(err)
			}
			if parent == 0 || index == 0 {
				t.Fatal(parent, index)
			}
			run(fixture.Exec("ALTER MATERIALIZED VIEW indexed RENAME COLUMN i TO key", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW indexed RENAME TO saved", 0))
			run(fixture.Exec("CREATE USER alter_index_owner", 0))
			run(fixture.Exec("CREATE SCHEMA alter_index_s2 AUTHORIZATION alter_index_owner", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW saved SET SCHEMA alter_index_s2", 0))
			run(fixture.Prepared(fixture.Exec("ALTER MATERIALIZED VIEW alter_index_s2.saved OWNER TO alter_index_owner", 0)))
			var gotParent, gotIndex int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'alter_index_s2.saved'::regclass::oid,'alter_index_s2.mv_u'::regclass::oid").Scan(&gotParent, &gotIndex); err != nil {
				t.Fatal(err)
			}
			if gotParent != parent || gotIndex != index {
				t.Fatal("attachment identity changed", gotParent, gotIndex)
			}
			run(fixture.Select("SELECT pg_get_indexdef('alter_index_s2.mv_u'::regclass) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX mv_u ON alter_index_s2.saved USING btree (key) INCLUDE (s)"}}))
			run(fixture.Select("SELECT c.relname,r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE (c.oid='alter_index_s2.saved'::regclass::oid OR c.oid='alter_index_s2.mv_u'::regclass::oid) ORDER BY c.relname", []string{"relname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"mv_u", "alter_index_owner"}, {"saved", "alter_index_owner"}}))
			run(fixture.Select("SELECT refobjsubid FROM pg_depend WHERE classid=1259 AND objid='alter_index_s2.mv_u'::regclass::oid AND refclassid=1259 AND refobjid='alter_index_s2.saved'::regclass::oid AND deptype='a' ORDER BY refobjsubid", []string{"refobjsubid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(alterBag("alter_index_s2.saved", [][]any{{int64(1), "a"}, {int64(2), nil}}))
		})
	}
}
func TestReferenceMaterializedAlterIndexRefreshAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			alterIndexSeed(run)
			run(fixture.Exec("ALTER MATERIALIZED VIEW indexed RENAME COLUMN i TO key", 0))
			run(fixture.Exec("INSERT INTO index_source VALUES(3,'c')", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW indexed", 0))
			bag := alterBag("indexed", [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}})
			run(bag)
			run(fixture.Exec("INSERT INTO index_source VALUES(3,'duplicate')", 1))
			var before bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
			}
			want := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "mv_u"`)
			want.Detail = "Key (key)=(3) is duplicated."
			want.Schema = "public"
			want.Table = "indexed"
			want.Constraint = "mv_u"
			alterError(t, p, c, "REFRESH MATERIALIZED VIEW indexed", want, "execute")
			run(bag)
			fixture.MaterializedViewPopulation(run, "indexed", true)
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed indexed refresh changed dump")
				}
			}
			run(fixture.Exec("REFRESH MATERIALIZED VIEW indexed WITH NO DATA", 0))
			fixture.MaterializedViewPopulation(run, "indexed", false)
			run(fixture.MaterializedViewUnpopulated("indexed"))
			alterError(t, p, c, "REFRESH MATERIALIZED VIEW indexed", want, "execute")
			fixture.MaterializedViewPopulation(run, "indexed", false)
			run(fixture.Exec("DELETE FROM index_source WHERE s='duplicate'", 1))
			run(fixture.Prepared(fixture.Exec("REFRESH MATERIALIZED VIEW indexed", 0)))
			run(bag)
		})
	}
}
func TestReferenceMaterializedAlterCollisions(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "alter_collision")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			alterIndexSeed(run)
			run(fixture.Exec("CREATE SCHEMA alter_collision", 0))
			run(fixture.Exec("CREATE TABLE alter_collision.mv_u(i INT)", 0))
			alterError(t, p, c, "ALTER MATERIALIZED VIEW indexed SET SCHEMA alter_collision", fixture.ControlsReferenceDiagnostic("42P07", `relation "mv_u" already exists in schema "alter_collision"`), "execute")
			run(fixture.Select("SELECT i,s FROM indexed ORDER BY i", []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}}))
			run(fixture.Exec("DROP TABLE alter_collision.mv_u", 0))
			run(fixture.Exec("CREATE TYPE alter_collision.indexed AS (x INT)", 0))
			alterError(t, p, c, "ALTER MATERIALIZED VIEW indexed SET SCHEMA alter_collision", fixture.ControlsReferenceDiagnostic("42P07", `relation "indexed" already exists in schema "alter_collision"`), "execute")
			run(fixture.Exec("DROP SCHEMA alter_collision CASCADE", 0))
			run(fixture.Exec("CREATE SCHEMA alter_collision", 0))
			// An enum is a genuine standalone type, not a composite's backing relation.
			run(fixture.Exec("CREATE TYPE alter_collision.indexed AS ENUM ('one')", 0))
			alterError(t, p, c, "ALTER MATERIALIZED VIEW indexed SET SCHEMA alter_collision", fixture.ControlsReferenceDiagnostic("42710", `type "indexed" already exists in schema "alter_collision"`), "execute")
			run(fixture.Exec("DROP SCHEMA alter_collision CASCADE", 0))
			run(fixture.Exec("CREATE SCHEMA alter_collision", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW indexed SET SCHEMA alter_collision", 0))
			run(fixture.Select("SELECT pg_get_indexdef('alter_collision.mv_u'::regclass) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX mv_u ON alter_collision.indexed USING btree (i) INCLUDE (s)"}}))
		})
	}
}
