package reference_test

import "testing"

func TestReferencePGDependCoreEdges(t *testing.T) {
	schemaReferenceCleanup(t, "pgdep_core")
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) { pgDependCore(t, p, b, "pgdep_core") })
}
func TestReferencePGDependTypeEdges(t *testing.T) {
	for _, schema := range []string{"pgdep_enum", "pgdep_domain"} {
		schemaReferenceCleanup(t, schema)
	}
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE SCHEMA pgdep_enum", "CREATE TYPE pgdep_enum.e AS ENUM('one','two')", "CREATE TABLE pgdep_enum.enum_anchor(v pgdep_enum.e)", "CREATE SCHEMA pgdep_domain", "CREATE DOMAIN pgdep_domain.d1 AS INT", "CREATE DOMAIN pgdep_domain.d2 AS pgdep_domain.d1", "CREATE TABLE pgdep_domain.domain_anchor(a pgdep_domain.d1,b pgdep_domain.d2,c pgdep_domain.d1[])")
		enumTable := pgDependRelation(t, b, "pgdep_enum.enum_anchor")
		ens := pgDependNamespace(t, b, enumTable)
		enum := pgDependID(t, b, "SELECT objid FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=2615 AND refobjid=$1 AND deptype='n'", ens)
		pgDependBag(t, p, b, 1247, enum, pgDependEdge(1247, enum, 0, 2615, ens, 0, "n"))
		pgDependBag(t, p, b, 1259, enumTable, pgDependEdge(1259, enumTable, 0, 2615, ens, 0, "n"), pgDependEdge(1259, enumTable, 1, 1247, enum, 0, "n"))
		table := pgDependRelation(t, b, "pgdep_domain.domain_anchor")
		ns := pgDependNamespace(t, b, table)
		// Structural identity discovery is isolated to the declaration namespace.
		// Require exact counts/roles separately; do not learn expected edge tuples.
		d1 := pgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=1 AND refclassid=1247", table)
		d2 := pgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=2 AND refclassid=1247", table)
		array := pgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=3 AND refclassid=1247", table)
		array2 := pgDependID(t, b, "SELECT objid FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=1247 AND refobjid=$1 AND deptype='i'", d2)
		ids := []int64{d1, d2, array, array2}
		for i, id := range ids {
			for j := range i {
				if id == ids[j] {
					t.Fatal("type roles alias", ids)
				}
			}
		}
		pgDependBag(t, p, b, 1247, d1, pgDependEdge(1247, d1, 0, 2615, ns, 0, "n"))
		pgDependBag(t, p, b, 1247, d2, pgDependEdge(1247, d2, 0, 2615, ns, 0, "n"), pgDependEdge(1247, d2, 0, 1247, d1, 0, "n"))
		pgDependBag(t, p, b, 1247, array, pgDependEdge(1247, array, 0, 1247, d1, 0, "i"))
		pgDependBag(t, p, b, 1247, array2, pgDependEdge(1247, array2, 0, 1247, d2, 0, "i"))
		pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"), pgDependEdge(1259, table, 1, 1247, d1, 0, "n"), pgDependEdge(1259, table, 2, 1247, d2, 0, "n"), pgDependEdge(1259, table, 3, 1247, array, 0, "n"))
		s := referenceSelect("SELECT count(*) FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=2615 AND refobjid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, ns)
		referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		if b.pg { // Independent release-only naming control, not a native pg_type API.
			pgDescriptionExecutorRows(t, p, b, b.db, referenceSelect("SELECT oid,typname FROM pg_catalog.pg_type WHERE oid=$1 OR oid=$2 ORDER BY typname", []string{"oid", "typname"}, []string{"OID", "NAME"}, [][]any{{d1, "d1"}, {d2, "d2"}}, d1, d2))
		}
	})
}
func TestReferencePGDependNoSyntheticEdges(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE TABLE pgdep_nosynthetic(a INT,b TEXT,c BOOL)", "CREATE INDEX pgdep_nosynthetic_idx ON pgdep_nosynthetic(a)", "COMMENT ON TABLE pgdep_nosynthetic IS 'not an edge'")
		table := pgDependRelation(t, b, "pgdep_nosynthetic")
		idx := pgDependRelation(t, b, "pgdep_nosynthetic_idx")
		pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, pgDependNamespace(t, b, table), 0, "n"))
		pgDependBag(t, p, b, 1259, idx, pgDependEdge(1259, idx, 0, 1259, table, 1, "a"))
		for _, class := range []int64{1249, 1260, 2615} {
			pgDependBag(t, p, b, class, table)
		}
		pgDependExec(t, p, b, "CREATE VIEW pgdep_constant AS SELECT 1 AS a", "CREATE VIEW pgdep_count AS SELECT count(*) AS n FROM pgdep_nosynthetic")
		for _, name := range []string{"pgdep_constant", "pgdep_count"} {
			v := pgDependRelation(t, b, name)
			r := pgDependRule(t, b, v)
			want := [][]any{pgDependEdge(2618, r, 0, 1259, v, 0, "i")}
			if name == "pgdep_count" {
				want = append(want, pgDependEdge(2618, r, 0, 1259, table, 0, "n"))
			}
			pgDependBag(t, p, b, 2618, r, want...)
		}
		// public is unpinned; the PG fixture recreates it, so its OID is generated.
		pgDependExec(t, p, b, "CREATE TABLE public.pgdep_public_exception(a INT)")
		publicTable := pgDependRelation(t, b, "public.pgdep_public_exception")
		pgDependBag(t, p, b, 1259, publicTable, pgDependEdge(1259, publicTable, 0, 2615, pgDependNamespace(t, b, publicTable), 0, "n"))
	})
}
