package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependCoreEdges(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgdep_core")
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) { fixture.PgDependCore(t, p, b, "pgdep_core") })
}
func TestReferencePGDependTypeEdges(t *testing.T) {
	for _, schema := range []string{"pgdep_enum", "pgdep_domain"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE SCHEMA pgdep_enum", "CREATE TYPE pgdep_enum.e AS ENUM('one','two')", "CREATE TABLE pgdep_enum.enum_anchor(v pgdep_enum.e)", "CREATE SCHEMA pgdep_domain", "CREATE DOMAIN pgdep_domain.d1 AS INT", "CREATE DOMAIN pgdep_domain.d2 AS pgdep_domain.d1", "CREATE TABLE pgdep_domain.domain_anchor(a pgdep_domain.d1,b pgdep_domain.d2,c pgdep_domain.d1[])")
		enumTable := fixture.PgDependRelation(t, b, "pgdep_enum.enum_anchor")
		ens := fixture.PgDependNamespace(t, b, enumTable)
		enum := fixture.PgDependID(t, b, "SELECT objid FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=2615 AND refobjid=$1 AND deptype='n'", ens)
		fixture.PgDependBag(t, p, b, 1247, enum, fixture.PgDependEdge(1247, enum, 0, 2615, ens, 0, "n"))
		fixture.PgDependBag(t, p, b, 1259, enumTable, fixture.PgDependEdge(1259, enumTable, 0, 2615, ens, 0, "n"), fixture.PgDependEdge(1259, enumTable, 1, 1247, enum, 0, "n"))
		table := fixture.PgDependRelation(t, b, "pgdep_domain.domain_anchor")
		ns := fixture.PgDependNamespace(t, b, table)
		// Structural identity discovery is isolated to the declaration namespace.
		// Require exact counts/roles separately; do not learn expected edge tuples.
		d1 := fixture.PgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=1 AND refclassid=1247", table)
		d2 := fixture.PgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=2 AND refclassid=1247", table)
		array := fixture.PgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=3 AND refclassid=1247", table)
		array2 := fixture.PgDependID(t, b, "SELECT objid FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=1247 AND refobjid=$1 AND deptype='i'", d2)
		ids := []int64{d1, d2, array, array2}
		for i, id := range ids {
			for j := range i {
				if id == ids[j] {
					t.Fatal("type roles alias", ids)
				}
			}
		}
		fixture.PgDependBag(t, p, b, 1247, d1, fixture.PgDependEdge(1247, d1, 0, 2615, ns, 0, "n"))
		fixture.PgDependBag(t, p, b, 1247, d2, fixture.PgDependEdge(1247, d2, 0, 2615, ns, 0, "n"), fixture.PgDependEdge(1247, d2, 0, 1247, d1, 0, "n"))
		fixture.PgDependBag(t, p, b, 1247, array, fixture.PgDependEdge(1247, array, 0, 1247, d1, 0, "i"))
		fixture.PgDependBag(t, p, b, 1247, array2, fixture.PgDependEdge(1247, array2, 0, 1247, d2, 0, "i"))
		fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n"), fixture.PgDependEdge(1259, table, 1, 1247, d1, 0, "n"), fixture.PgDependEdge(1259, table, 2, 1247, d2, 0, "n"), fixture.PgDependEdge(1259, table, 3, 1247, array, 0, "n"))
		s := fixture.Select("SELECT count(*) FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=2615 AND refobjid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, ns)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		if b.PG { // Independent release-only naming control, not a native pg_type API.
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Select("SELECT oid,typname FROM pg_catalog.pg_type WHERE oid=$1 OR oid=$2 ORDER BY typname", []string{"oid", "typname"}, []string{"OID", "NAME"}, [][]any{{d1, "d1"}, {d2, "d2"}}, d1, d2))
		}
	})
}
func TestReferencePGDependNoSyntheticEdges(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_nosynthetic(a INT,b TEXT,c BOOL)", "CREATE INDEX pgdep_nosynthetic_idx ON pgdep_nosynthetic(a)", "COMMENT ON TABLE pgdep_nosynthetic IS 'not an edge'")
		table := fixture.PgDependRelation(t, b, "pgdep_nosynthetic")
		idx := fixture.PgDependRelation(t, b, "pgdep_nosynthetic_idx")
		fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, fixture.PgDependNamespace(t, b, table), 0, "n"))
		fixture.PgDependBag(t, p, b, 1259, idx, fixture.PgDependEdge(1259, idx, 0, 1259, table, 1, "a"))
		for _, class := range []int64{1249, 1260, 2615} {
			fixture.PgDependBag(t, p, b, class, table)
		}
		fixture.PgDependExec(t, p, b, "CREATE VIEW pgdep_constant AS SELECT 1 AS a", "CREATE VIEW pgdep_count AS SELECT count(*) AS n FROM pgdep_nosynthetic")
		for _, name := range []string{"pgdep_constant", "pgdep_count"} {
			v := fixture.PgDependRelation(t, b, name)
			r := fixture.PgDependRule(t, b, v)
			want := [][]any{fixture.PgDependEdge(2618, r, 0, 1259, v, 0, "i")}
			if name == "pgdep_count" {
				want = append(want, fixture.PgDependEdge(2618, r, 0, 1259, table, 0, "n"))
			}
			fixture.PgDependBag(t, p, b, 2618, r, want...)
		}
		// public is unpinned; the PG fixture recreates it, so its OID is generated.
		fixture.PgDependExec(t, p, b, "CREATE TABLE public.pgdep_public_exception(a INT)")
		publicTable := fixture.PgDependRelation(t, b, "public.pgdep_public_exception")
		fixture.PgDependBag(t, p, b, 1259, publicTable, fixture.PgDependEdge(1259, publicTable, 0, 2615, fixture.PgDependNamespace(t, b, publicTable), 0, "n"))
	})
}
