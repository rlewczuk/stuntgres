package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgClassPartitions(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB,
			"CREATE TABLE pc18_bound_root(id integer PRIMARY KEY) PARTITION BY RANGE(id)",
			"CREATE TABLE pc18_bound_leaf PARTITION OF pc18_bound_root FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE pc18_bound_default PARTITION OF pc18_bound_root DEFAULT",
			"CREATE TABLE pc18_bound_multi(a integer,b integer) PARTITION BY RANGE(a,b)",
			"CREATE TABLE pc18_bound_min PARTITION OF pc18_bound_multi FOR VALUES FROM(MINVALUE,MINVALUE) TO(0,10)",
			"CREATE TABLE pc18_bound_max PARTITION OF pc18_bound_multi FOR VALUES FROM(0,10) TO(MAXVALUE,MAXVALUE)",
			"CREATE TABLE pc18_bound_nested(id integer) PARTITION BY RANGE(id)",
			"CREATE TABLE pc18_bound_branch PARTITION OF pc18_bound_nested FOR VALUES FROM(10) TO(20) PARTITION BY RANGE(id)",
			"CREATE TABLE pc18_bound_tip PARTITION OF pc18_bound_branch FOR VALUES FROM(10) TO(15)",
			"CREATE TABLE pc18_bound_dates(d date) PARTITION BY RANGE(d)",
			"CREATE TABLE pc18_bound_date PARTITION OF pc18_bound_dates FOR VALUES FROM('2026-01-01') TO('2027-01-01')")
		cases := []struct {
			name, kind, tree string
			subclass         bool
		}{
			{"pc18_bound_root", "p", "", true},
			{"pc18_bound_leaf", "r", `{PartitionBound strategy range default false lower ({type 23 typmod -1 collation 0 value "0"}) upper ({type 23 typmod -1 collation 0 value "10"})}`, false},
			{"pc18_bound_default", "r", `{PartitionBound strategy range default true}`, false},
			{"pc18_bound_min", "r", `{PartitionBound strategy range default false lower (MINVALUE MINVALUE) upper ({type 23 typmod -1 collation 0 value "0"} {type 23 typmod -1 collation 0 value "10"})}`, false},
			{"pc18_bound_max", "r", `{PartitionBound strategy range default false lower ({type 23 typmod -1 collation 0 value "0"} {type 23 typmod -1 collation 0 value "10"}) upper (MAXVALUE MAXVALUE)}`, false},
			{"pc18_bound_branch", "p", `{PartitionBound strategy range default false lower ({type 23 typmod -1 collation 0 value "10"}) upper ({type 23 typmod -1 collation 0 value "20"})}`, true},
			{"pc18_bound_date", "r", `{PartitionBound strategy range default false lower ({type 1082 typmod -1 collation 0 value "2026-01-01"}) upper ({type 1082 typmod -1 collation 0 value "2027-01-01"})}`, false},
		}
		for _, tc := range cases {
			q := "SELECT relkind,relispartition,relhassubclass,relpartbound IS NULL AS no_bound FROM pg_class WHERE relname='" + tc.name + "'"
			pgClassCheck(t, p, b, b.DB, fixture.Select(q, []string{"relkind", "relispartition", "relhassubclass", "no_bound"}, []string{"CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.kind, tc.tree != "", tc.subclass, tc.tree == ""}}))
			if tc.tree == "" {
				continue
			}
			q = "SELECT relpartbound FROM pg_class WHERE relname='" + tc.name + "'"
			if !b.PG {
				pgClassCheck(t, p, b, b.DB, fixture.Select(q, []string{"relpartbound"}, []string{"PG_NODE_TREE"}, [][]any{{tc.tree}}))
				continue
			}
			rows, err := b.DB.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "PG_NODE_TREE" {
				t.Fatal("bound type", types, err)
			}
			if !rows.Next() {
				t.Fatal("missing bound", rows.Err())
			}
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, true, "PG_NODE_TREE", raw)
			text := fixture.Normalize(raw).(string)
			if !strings.Contains(text, "PARTITIONBOUNDSPEC") {
				t.Fatal("release structural bound", text)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("bound multiplicity", rows.Err())
			}
			rows.Close()
		}
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT c.relkind,c.relispartition,c.relpartbound IS NULL AS no_bound,c.relhassubclass FROM pg_class c JOIN pg_index i ON i.indexrelid=c.oid WHERE i.indrelid='pc18_bound_root'::regclass::oid", []string{"relkind", "relispartition", "no_bound", "relhassubclass"}, []string{"CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"I", false, true, true}}))
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT c.relkind,c.relispartition,c.relpartbound IS NULL AS no_bound FROM pg_class c JOIN pg_index i ON i.indexrelid=c.oid WHERE i.indrelid='pc18_bound_leaf'::regclass::oid", []string{"relkind", "relispartition", "no_bound"}, []string{"CHAR", "BOOL", "BOOL"}, [][]any{{"i", true, true}}))
		// ATTACH uses the owner's private catalog overlay, including savepoints.
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc18_bound_attach(id integer)")
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		pgClassExec(t, p, b, a, "BEGIN", "SAVEPOINT pc18_bound_save", "ALTER TABLE pc18_bound_nested ATTACH PARTITION pc18_bound_attach FOR VALUES FROM(30) TO(40)")
		defer a.ExecContext(p.Ctx, "ROLLBACK")
		q := "SELECT relispartition,relpartbound IS NULL AS no_bound FROM pg_class WHERE relname='pc18_bound_attach'"
		check := func(db fixture.Executor, member bool) {
			pgClassCheck(t, p, b, db, fixture.Select(q, []string{"relispartition", "no_bound"}, []string{"BOOL", "BOOL"}, [][]any{{member, !member}}))
		}
		check(a, true)
		check(peer, false)
		pgClassExec(t, p, b, a, "ROLLBACK TO pc18_bound_save")
		check(a, false)
		pgClassExec(t, p, b, a, "COMMIT")
		check(peer, false)
		rename := "ALTER TABLE pc18_bound_leaf RENAME TO pc18_bound_renamed"
		if b.PG {
			var before string
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT relpartbound FROM pg_class WHERE relname='pc18_bound_leaf'").Scan(&before); err != nil {
				t.Fatal(err)
			}
			pgClassExec(t, p, b, b.DB, rename)
			pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relname,relpartbound FROM pg_class WHERE relname='pc18_bound_renamed'", []string{"relname", "relpartbound"}, []string{"NAME", "PG_NODE_TREE"}, [][]any{{"pc18_bound_renamed", before}}))
			pgClassExec(t, p, b, b.DB, "DROP TABLE pc18_bound_renamed")
		}
		if !b.PG {
			old, err := b.DB.QueryContext(p.Ctx, "SELECT relpartbound FROM pg_class WHERE relname='pc18_bound_leaf'")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			descriptions, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			unsupported := fixture.ImplicitError(rename, fixture.Diagnostic{Code: "0A000", Message: "altering partition hierarchies is unsupported"})
			unsupported.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, unsupported)
			probe := "SELECT relpartbound FROM pg_class WHERE relname='pc18_bound_leaf'"
			pgClassCheck(t, p, b, b.DB, fixture.Select(probe, []string{"relpartbound"}, []string{"PG_NODE_TREE"}, [][]any{{cases[1].tree}}))
			dump := fixture.ViewDump(t, p)
			pgClassExec(t, p, b, b.DB, "DROP TABLE pc18_bound_leaf")
			pgClassCheck(t, p, b, b.DB, fixture.Select(probe, []string{"relpartbound"}, []string{"PG_NODE_TREE"}, nil))
			fixture.ViewRestore(t, p, dump)
			pgClassCheck(t, p, b, b.DB, fixture.Select(probe, []string{"relpartbound"}, []string{"PG_NODE_TREE"}, [][]any{{cases[1].tree}}))
			pgClassRetained(t, old, descriptions, []string{"relpartbound"}, []string{"PG_NODE_TREE"}, []any{cases[1].tree})
		}
	})
}

func pgClassRetained(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, names, kinds []string, want []any) {
	t.Helper()
	defer rows.Close()
	for i, typ := range types {
		if typ.Name() != names[i] || typ.DatabaseTypeName() != kinds[i] {
			t.Fatal("retained metadata", typ)
		}
	}
	if !rows.Next() {
		t.Fatal("retained missing", rows.Err())
	}
	got, dest := make([]any, len(want)), make([]any, len(want))
	for i := range dest {
		dest[i] = &got[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		fixture.AssertRawValue(t, false, kinds[i], v)
		got[i] = fixture.Normalize(v)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("retained changed", got, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained cardinality", rows.Err())
	}
}

func TestReferencePgClassPhysicalBoundaries(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc18_phys_heap(id integer,note text)", "CREATE INDEX pc18_phys_idx ON pc18_phys_heap(id)", "CREATE SEQUENCE pc18_phys_seq", "CREATE VIEW pc18_phys_view AS SELECT id FROM pc18_phys_heap", "CREATE MATERIALIZED VIEW pc18_phys_mv AS SELECT id FROM pc18_phys_heap WITH NO DATA", "CREATE TYPE pc18_phys_pair AS(id integer)", "CREATE TABLE pc18_phys_root(id integer PRIMARY KEY) PARTITION BY RANGE(id)")
		q := "SELECT relfilenode,relpages,reltuples,relallvisible,relallfrozen,reltoastrelid,relrewrite,relfrozenxid,relminmxid FROM pg_class WHERE relname=$1"
		held := fixture.ViewHold(t, p, b, q)
		check := func() {
			for _, name := range []string{"pc18_phys_heap", "pc18_phys_idx", "pc18_phys_seq", "pc18_phys_view", "pc18_phys_mv", "pc18_phys_pair", "pc18_phys_root", "pc18_phys_root_pkey"} {
				if b.PG && (name == "pc18_phys_heap" || name == "pc18_phys_idx" || name == "pc18_phys_mv") {
					continue
				}
				t.Run(name, func(t *testing.T) {
					tuples, pages := float64(-1), int64(0)
					if name == "pc18_phys_seq" {
						tuples = 1
						if b.PG {
							pages = 1
						}
					}
					// Pinned PG18 initializes this partitioned backing index estimate
					// to zero (root tests-2); native has no statistics and stays unknown.
					if b.PG && name == "pc18_phys_root_pkey" {
						tuples = 0
					}
					s := fixture.Select(q, []string{"relfilenode", "relpages", "reltuples", "relallvisible", "relallfrozen", "reltoastrelid", "relrewrite", "relfrozenxid", "relminmxid"}, []string{"OID", "INT4", "FLOAT4", "INT4", "INT4", "OID", "OID", "XID", "XID"}, [][]any{{int64(0), pages, tuples, int64(0), int64(0), int64(0), int64(0), "0", "0"}}, name)
					// A real sequence has a physical file. Compare its other fields,
					// while independently asserting the file is actually present.
					if b.PG && name == "pc18_phys_seq" {
						s = fixture.Select("SELECT relpages,reltuples,relfrozenxid,relminmxid,relfilenode<>0::oid AS file FROM pg_class WHERE relname='pc18_phys_seq'", []string{"relpages", "reltuples", "relfrozenxid", "relminmxid", "file"}, []string{"INT4", "FLOAT4", "XID", "XID", "BOOL"}, [][]any{{int64(1), float64(1), "0", "0", true}})
						pgClassCheck(t, p, b, b.DB, s)
					} else {
						pgClassCheck(t, p, b, b.DB, s)
						pgClassCheck(t, p, b, held, s)
					}
				})
			}
		}
		check()
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pc18_phys_heap VALUES(1,'short'),(2,'wide')", 2))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE pc18_phys_heap SET note=$1 WHERE id=1", 1, strings.Repeat("wide datum ", 1000)))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM pc18_phys_heap WHERE id=2", 1))
		check()
		pgClassExec(t, p, b, b.DB, "DROP INDEX pc18_phys_idx", "REFRESH MATERIALIZED VIEW pc18_phys_mv WITH DATA")
		if !b.PG {
			pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT reltoastrelid,relpages,reltuples FROM pg_class WHERE relname='pc18_phys_heap'", []string{"reltoastrelid", "relpages", "reltuples"}, []string{"OID", "INT4", "FLOAT4"}, [][]any{{int64(0), int64(0), float64(-1)}}))
			pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relname FROM pg_class WHERE relkind='t' OR relkind='f'", []string{"relname"}, []string{"NAME"}, nil))
			pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relfilenode,relpages,reltuples,relispopulated FROM pg_class WHERE relname='pc18_phys_mv'", []string{"relfilenode", "relpages", "reltuples", "relispopulated"}, []string{"OID", "INT4", "FLOAT4", "BOOL"}, [][]any{{int64(0), int64(0), float64(-1), true}}))
		}
		if b.PG {
			pgClassExec(t, p, b, b.DB, "ANALYZE pc18_phys_heap")
			pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT reltuples FROM pg_class WHERE relname='pc18_phys_heap'", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(1)}}))
		}
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc18_phys_parent(id integer PRIMARY KEY)", "CREATE TABLE pc18_phys_child(id integer REFERENCES pc18_phys_parent(id))")
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relname,relhastriggers FROM pg_class WHERE relname='pc18_phys_child' OR relname='pc18_phys_parent' ORDER BY relname", []string{"relname", "relhastriggers"}, []string{"NAME", "BOOL"}, [][]any{{"pc18_phys_child", b.PG}, {"pc18_phys_parent", b.PG}}))
	})
}
