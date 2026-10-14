package reference_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type partitionDetachVirtualNames struct {
	midSchema, leafSchema, midName, leafName, midKey, leafKey string
}

func partitionDetachVirtualQualified(schema, name string) string {
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	return quote(schema) + "." + quote(name)
}

func (n partitionDetachVirtualNames) mid() string {
	return partitionDetachVirtualQualified(n.midSchema, n.midName)
}
func (n partitionDetachVirtualNames) leaf() string {
	return partitionDetachVirtualQualified(n.leafSchema, n.leafName)
}
func (n partitionDetachVirtualNames) midLocal() string {
	return partitionDetachVirtualQualified(n.midSchema, n.midKey)
}
func (n partitionDetachVirtualNames) leafLocal() string {
	return partitionDetachVirtualQualified(n.leafSchema, n.leafKey)
}
func (n partitionDetachVirtualNames) midPlain() string {
	return partitionDetachVirtualQualified(n.midSchema, "dv_mid_attached")
}
func (n partitionDetachVirtualNames) leafPlain() string {
	return partitionDetachVirtualQualified(n.leafSchema, "dv_leaf_attached")
}

func TestReferencePartitionDetachReplayVirtual(t *testing.T) {
	partitionDetachVirtualCases(t, partitionDetachVirtualNames{"public", "public", "dv_mid", "dv_leaf", "dv_mid_local", "dv_leaf_local"})
}

func TestReferencePartitionDetachReplayVirtualQualified(t *testing.T) {
	// Both table and backing qualified names collide if flattened with a dot.
	partitionDetachVirtualCases(t, partitionDetachVirtualNames{"a", "a.b", "b.c_table", "c_table", "b.c", "c"})
}

func partitionDetachVirtualCases(t *testing.T, n partitionDetachVirtualNames) {
	t.Helper()
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		for _, retain := range []string{"local", "attached"} {
			t.Run(kind+"/retain_"+retain, func(t *testing.T) {
				if n.midSchema != "public" {
					fixture.SchemaReferenceCleanup(t, n.midSchema, n.leafSchema)
				}
				partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					if n.midSchema != "public" {
						run(fixture.Exec(`CREATE SCHEMA "a"`, 0))
						run(fixture.Exec(`CREATE SCHEMA "a.b"`, 0))
					}
					partitionDetachVirtualTables(run, kind, n)
					run(fixture.Exec("ALTER TABLE dv_root ATTACH PARTITION "+n.mid()+" FOR VALUES FROM(1) TO(3)", 0))
					run(fixture.Exec("CREATE TABLE dv_gap PARTITION OF dv_root FOR VALUES FROM(3) TO(5)", 0))
					run(fixture.Exec("INSERT INTO dv_root VALUES(3,3,'gap')", 1))
					partitionDetachVirtualPlain(run, n)
					run(fixture.Prepared(fixture.Exec("ALTER TABLE dv_root DETACH PARTITION dv_gap", 0)))
					run(fixture.Exec("CREATE TABLE dv_refs(ref REGCLASS DEFAULT '"+n.midLocal()+"'::regclass)", 0))
					run(fixture.Exec("INSERT INTO dv_refs DEFAULT VALUES", 1))
					run(fixture.Exec("CREATE VIEW dv_provider_view AS SELECT ref='"+n.midLocal()+"'::regclass AS same FROM dv_refs", 0))
					run(fixture.Exec("CREATE TABLE dv_leaf_refs(ref REGCLASS DEFAULT '"+n.leafLocal()+"'::regclass)", 0))
					run(fixture.Exec("INSERT INTO dv_leaf_refs DEFAULT VALUES", 1))
					run(fixture.Exec("CREATE VIEW dv_leaf_provider_view AS SELECT ref='"+n.leafLocal()+"'::regclass AS same FROM dv_leaf_refs", 0))
					partitionDetachVirtualState(t, p, b.DB, b.Recorder, kind, n)
					if !b.PG {
						run(fixture.ImplicitError("ALTER TABLE "+n.mid()+" ADD CONSTRAINT dv_public_add UNIQUE(k,j)", fixture.Diagnostic{Code: "0A000", Message: "altering partition hierarchies is unsupported"}))
						partitionDetachVirtualState(t, p, b.DB, b.Recorder, kind, n)
					}
					if b.PG {
						partitionDetachVirtualFuture(t, p, b.DB, b.Recorder, retain, n)
						return
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					fresh := fixture.NewFixture(t, false, true)
					if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					partitionDetachVirtualState(t, fresh, fresh.Memory, fresh.MemoryRecord, kind, n)
					partitionDetachVirtualPublicBoundary(t, fresh, fresh.Memory, fresh.MemoryRecord, n)
					partitionDetachVirtualState(t, fresh, fresh.Memory, fresh.MemoryRecord, kind, n)
					partitionDetachVirtualFuture(t, fresh, fresh.Memory, fresh.MemoryRecord, retain, n)
					for range 2 {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						partitionDetachVirtualState(t, p, b.DB, b.Recorder, kind, n)
						partitionDetachVirtualPublicBoundary(t, p, b.DB, b.Recorder, n)
						partitionDetachVirtualState(t, p, b.DB, b.Recorder, kind, n)
						var again bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
							t.Fatalf("virtual-local replay changed canonical stream: %v\noriginal:\n%s\nrestored:\n%s", err, dump.String(), again.String())
						}
					}
					if p.Postgres != nil {
						partitionAttachReplay(t, p, dump.String(), func(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool) {
							partitionDetachVirtualState(t, p, x, r, kind, n)
							partitionDetachVirtualFuture(t, p, x, r, retain, n)
							if n.midSchema != "public" {
								fixture.RunStep(t, p.Ctx, x, r, fixture.Exec(`DROP SCHEMA "a" CASCADE`, 0))
								fixture.RunStep(t, p.Ctx, x, r, fixture.Exec(`DROP SCHEMA "a.b" CASCADE`, 0))
							}
						})
					}
					partitionDetachVirtualFuture(t, p, b.DB, b.Recorder, retain, n)
				})
			})
		}
	}
}

func partitionDetachVirtualTables(run func(fixture.Step), kind string, n partitionDetachVirtualNames) {
	run(fixture.Exec("CREATE TABLE dv_root(k INT NOT NULL,j INT NOT NULL,payload TEXT) PARTITION BY RANGE(k)", 0))
	local, leafLocal := "", ""
	if kind != "" {
		local = `,CONSTRAINT "` + n.midKey + `" ` + kind + "(k,j)"
		leafLocal = `,CONSTRAINT "` + n.leafKey + `" ` + kind + "(k,j)"
	}
	run(fixture.Exec("CREATE TABLE "+n.mid()+"(k INT CONSTRAINT dv_mid_k_nn NOT NULL,j INT CONSTRAINT dv_mid_j_nn NOT NULL,payload TEXT"+local+") PARTITION BY RANGE(j)", 0))
	run(fixture.Exec("CREATE TABLE "+n.leaf()+"(k INT CONSTRAINT dv_leaf_k_nn NOT NULL,j INT CONSTRAINT dv_leaf_j_nn NOT NULL,payload TEXT"+leafLocal+")", 0))
	run(fixture.Exec("ALTER TABLE "+n.mid()+" ATTACH PARTITION "+n.leaf()+" DEFAULT", 0))
	run(fixture.Exec("INSERT INTO "+n.mid()+" VALUES(1,1,'a')", 1))
}

func partitionDetachVirtualPlain(run func(fixture.Step), n partitionDetachVirtualNames) {
	run(fixture.Exec("CREATE UNIQUE INDEX dv_root_idx ON ONLY dv_root(k,j)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX dv_mid_attached ON ONLY "+n.mid()+"(k,j)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX dv_leaf_attached ON "+n.leaf()+"(k,j)", 0))
	run(fixture.Exec("ALTER INDEX "+n.midPlain()+" ATTACH PARTITION "+n.leafPlain(), 0))
	run(partitionConstraintIndexState(n.midPlain(), true))
	run(fixture.Exec("ALTER INDEX dv_root_idx ATTACH PARTITION "+n.midPlain(), 0))
	run(partitionConstraintIndexState("dv_root_idx", false))
}

func partitionDetachVirtualState(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, kind string, n partitionDetachVirtualNames) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	run(partitionDetachMember(n.mid(), "dv_root", true))
	run(partitionDetachMember(n.leaf(), n.mid(), true))
	run(partitionDetachMember("dv_gap", "dv_root", false))
	run(partitionConstraintEdge(n.midPlain(), "dv_root_idx", n.mid()))
	run(partitionConstraintEdge(n.leafPlain(), n.midPlain(), n.leaf()))
	run(partitionConstraintEdge(n.leafLocal(), n.midLocal(), n.leaf()))
	run(partitionDetachTopEdges(n.midLocal(), false))
	indexRows := [][]any{{"dv_leaf_attached", n.leafName, "i", true, true, false, true, true, true}, {n.leafKey, n.leafName, "i", true, true, kind == "PRIMARY KEY", true, true, true}, {"dv_mid_attached", n.midName, "I", true, true, false, true, true, true}, {n.midKey, n.midName, "I", false, true, kind == "PRIMARY KEY", true, true, true}, {"dv_root_idx", "dv_root", "I", false, true, false, false, true, true}}
	constraintRows := [][]any{{n.leafKey, n.leafName, n.leafKey, false}, {n.midKey, n.midName, n.midKey, true}}
	dependencyRows := [][]any{{n.leafKey, n.leafKey}, {n.midKey, n.midKey}}
	// Expected ordering is calculated from fixture input names, never catalog output.
	for _, rows := range [][][]any{indexRows, constraintRows, dependencyRows} {
		slices.SortFunc(rows, func(a, b []any) int { return strings.Compare(a[0].(string), b[0].(string)) })
	}
	run(fixture.Select("SELECT i.relname,t.relname AS table_name,i.relkind,i.relispartition,x.indisunique,x.indisprimary,x.indisvalid,x.indisready,x.indislive FROM pg_class i JOIN pg_index x ON x.indexrelid=i.oid JOIN pg_class t ON t.oid=x.indrelid WHERE t.oid='dv_root'::regclass::OID OR t.oid='"+n.mid()+"'::regclass::OID OR t.oid='"+n.leaf()+"'::regclass::OID ORDER BY i.relname", []string{"relname", "table_name", "relkind", "relispartition", "indisunique", "indisprimary", "indisvalid", "indisready", "indislive"}, []string{"NAME", "NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, indexRows))
	run(fixture.Select("SELECT c.conname,t.relname AS table_name,i.relname AS backing_name,c.conparentid=0 AS no_parent FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_class i ON i.oid=c.conindid WHERE (c.contype='u' OR c.contype='p') AND (t.oid='"+n.mid()+"'::regclass::OID OR t.oid='"+n.leaf()+"'::regclass::OID) ORDER BY c.conname", []string{"conname", "table_name", "backing_name", "no_parent"}, []string{"NAME", "NAME", "NAME", "BOOL"}, constraintRows))
	run(fixture.Select("SELECT child.conname,parent.conname AS parent_name FROM pg_constraint child JOIN pg_constraint parent ON parent.oid=child.conparentid WHERE (child.contype='u' OR child.contype='p') AND child.conrelid='"+n.leaf()+"'::regclass::OID", []string{"conname", "parent_name"}, []string{"NAME", "NAME"}, [][]any{{n.leafKey, n.midKey}}))
	run(fixture.Select("SELECT i.relname,c.conname FROM pg_depend d JOIN pg_class i ON i.oid=d.objid JOIN pg_constraint c ON c.oid=d.refobjid WHERE d.classid='pg_class'::regclass::OID AND d.refclassid='pg_constraint'::regclass::OID AND d.deptype='i' AND (i.oid='"+n.midLocal()+"'::regclass::OID OR i.oid='"+n.leafLocal()+"'::regclass::OID) ORDER BY i.relname", []string{"relname", "conname"}, []string{"NAME", "NAME"}, dependencyRows))
	run(fixture.Select("SELECT k,j,payload FROM dv_root", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "a"}}))
	run(fixture.Select("SELECT k,j,payload FROM dv_gap", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(3), int64(3), "gap"}}))
	for _, relation := range []string{"dv_provider_view", "dv_leaf_provider_view"} {
		run(fixture.Select("SELECT same FROM "+relation, []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
	}
	run(fixture.Select("SELECT ref='"+n.midLocal()+"'::regclass AS same FROM dv_refs", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
	run(fixture.Select("SELECT ref='"+n.leafLocal()+"'::regclass AS same FROM dv_leaf_refs", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
	run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname LIKE '__stuntgres_partition_%'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func partitionDetachVirtualFuture(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, retain string, n partitionDetachVirtualNames) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	// The local-key drop control below intentionally removes this provider.
	run(fixture.Exec("DROP VIEW dv_leaf_provider_view", 0))
	run(fixture.Exec("DROP TABLE dv_leaf_refs", 0))
	run(fixture.Exec("ALTER TABLE dv_root DETACH PARTITION "+n.mid(), 0))
	run(fixture.Exec("DROP TABLE dv_root", 0))
	run(fixture.Exec("INSERT INTO "+n.mid()+" VALUES(7,2,'outside')", 1))
	run(fixture.Exec("ALTER TABLE "+n.mid()+" DETACH PARTITION "+n.leaf(), 0))
	winner := n.leafKey
	if retain == "local" {
		run(fixture.Exec("DROP INDEX "+n.leafPlain(), 0))
	} else {
		run(fixture.Exec("ALTER TABLE "+n.leaf()+` DROP CONSTRAINT "`+n.leafKey+`"`, 0))
		winner = "dv_leaf_attached"
	}
	run(referenceRangeError("INSERT INTO "+n.leaf()+" VALUES(1,1,'duplicate')", "23505", `duplicate key value violates unique constraint "`+winner+`"`, fixture.Diagnostic{Detail: "Key (k, j)=(1, 1) already exists.", Schema: n.leafSchema, Table: n.leafName, Constraint: winner}))
	run(fixture.ImplicitError("INSERT INTO "+n.leaf()+" VALUES(8,NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "j" of relation "` + n.leafName + `" violates not-null constraint`, Detail: "Failing row contains (8, null, bad).", Schema: n.leafSchema, Table: n.leafName, Column: "j"}))
	run(fixture.Select("SELECT k,j,payload FROM "+n.leaf()+" ORDER BY k", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "a"}, {int64(7), int64(2), "outside"}}))
	run(fixture.Exec("INSERT INTO dv_refs DEFAULT VALUES", 1))
	run(fixture.Select("SELECT count(*) FROM dv_refs WHERE ref='"+n.midLocal()+"'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
}

func partitionDetachVirtualPublicBoundary(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, n partitionDetachVirtualNames) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, r, fixture.ImplicitError("ALTER TABLE "+n.mid()+" ADD CONSTRAINT dv_public_add UNIQUE(k,j)", fixture.Diagnostic{Code: "0A000", Message: "altering partition hierarchies is unsupported"}))
}
