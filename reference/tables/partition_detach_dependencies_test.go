package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func partitionDetachCapture(t *testing.T, p *fixture.Pair, x fixture.Executor, q string) [][]any {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

// Compare identities within one allocator. Independently require every captured
// dependency except precisely the three top partition edge families to survive.
func partitionDetachGraph(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step), detach fixture.Step) {
	t.Helper()
	q := "SELECT classid::BIGINT,objid::BIGINT,objsubid,refclassid::BIGINT,refobjid::BIGINT,refobjsubid,deptype FROM pg_depend ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype"
	before := partitionDetachCapture(t, p, b.DB, q)
	ids := partitionDetachCapture(t, p, b.DB, "SELECT oid::BIGINT FROM pg_class WHERE relname='pd_child'")
	parent := partitionDetachCapture(t, p, b.DB, "SELECT oid::BIGINT FROM pg_class WHERE relname='pd_parent'")
	index := partitionDetachCapture(t, p, b.DB, "SELECT oid::BIGINT FROM pg_class WHERE relname='pd_child_key'")
	constraint := partitionDetachCapture(t, p, b.DB, "SELECT oid::BIGINT FROM pg_constraint WHERE conname='pd_child_key'")
	var expected [][]any
	removed := 0
	for _, edge := range before {
		membership := edge[0] == int64(1259) && edge[1] == ids[0][0] && edge[3] == int64(1259) && edge[4] == parent[0][0] && edge[6] == "a"
		top := (edge[6] == "P" || edge[6] == "S") && (edge[0] == int64(1259) || edge[0] == int64(2606))
		// The child is the only indexed attached member with a named reused
		// backing in this fixture. Identify its actual index/constraint OIDs.
		if top {
			top = (edge[0] == int64(1259) && edge[1] == index[0][0]) || (edge[0] == int64(2606) && edge[1] == constraint[0][0])
		}
		if membership || top {
			removed++
			continue
		}
		expected = append(expected, edge)
	}
	if removed != 5 {
		t.Fatalf("fixture must expose membership + index P/S + constraint P/S, got %d", removed)
	}
	run(detach)
	step := fixture.Select(q, []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, expected)
	run(step)
}

func TestReferencePartitionDetachDependencies(t *testing.T) {
	partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		// Local exclusion is an admitted standalone producer, not hierarchy ADD.
		partitionDetachSetupConstraints(run, "pd_parent", ",CONSTRAINT pd_exclusion EXCLUDE USING btree(payload WITH =)")
		run(fixture.Exec("CREATE VIEW pd_parent_view AS SELECT k,payload FROM pd_parent", 0))
		run(fixture.Exec("CREATE VIEW pd_child_view AS SELECT k,payload FROM pd_child", 0))
		run(fixture.Exec("CREATE TABLE pd_refs(ref REGCLASS DEFAULT 'pd_child'::regclass)", 0))
		run(fixture.Exec("INSERT INTO pd_refs VALUES('pd_parent'::regclass),('pd_child_key'::regclass)", 2))
		run(fixture.Exec("CREATE INDEX pd_expression ON pd_child((length(payload))) WHERE k>0", 0))
		identityQ := "SELECT oid::BIGINT,relname,relowner::BIGINT,reltype::BIGINT FROM pg_class WHERE relname='pd_child' OR relname='pd_child_key' OR relname='pd_seq' OR relname='pd_expression' ORDER BY relname"
		identities := partitionDetachCapture(t, p, b.DB, identityQ)
		constraintQ := "SELECT oid::BIGINT,conname,conindid::BIGINT FROM pg_constraint WHERE conrelid='pd_child'::regclass::OID ORDER BY conname"
		constraints := partitionDetachCapture(t, p, b.DB, constraintQ)
		partitionDetachGraph(t, p, b, run, fixture.Prepared(fixture.Exec("ALTER TABLE pd_parent DETACH PARTITION pd_child", 0)))
		run(fixture.Select(identityQ, []string{"oid", "relname", "relowner", "reltype"}, []string{"INT8", "NAME", "INT8", "INT8"}, identities))
		run(fixture.Select(constraintQ, []string{"oid", "conname", "conindid"}, []string{"INT8", "NAME", "INT8"}, constraints))
		run(fixture.Select("SELECT conname,contype,conparentid=0 AS no_parent,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pd_child'::regclass::OID ORDER BY conname", []string{"conname", "contype", "no_parent", "conislocal", "coninhcount"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "INT2"}, [][]any{{"pd_check", "c", true, true, int64(0)}, {"pd_child_key", "u", true, true, int64(0)}, {"pd_child_nn", "n", true, true, int64(0)}, {"pd_exclusion", "x", true, true, int64(0)}, {"pd_local", "c", true, true, int64(0)}}))
		run(referenceRangeRows("SELECT k FROM pd_parent_view", 3))
		run(referenceRangeRows("SELECT k FROM pd_child_view ORDER BY k", 1, 2))
		run(fixture.Select("SELECT ref='pd_parent'::regclass AS parent,ref='pd_child_key'::regclass AS backing FROM pd_refs ORDER BY parent DESC", []string{"parent", "backing"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {false, true}}))
		run(fixture.Exec("INSERT INTO pd_refs DEFAULT VALUES", 1))
		run(fixture.Select("SELECT count(*) FROM pd_refs WHERE ref='pd_child'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.ErrorDiagnostic("INSERT INTO pd_child(payload,k) VALUES('a',7)", fixture.ExclusionReferenceConflict("pd_child", "pd_exclusion", "payload", "a", "a")))
		run(fixture.ImplicitError("DROP TABLE pd_child RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table pd_child because other objects depend on it", Detail: "view pd_child_view depends on table pd_child\ndefault value for column ref of table pd_refs depends on table pd_child", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		// DROP rejection preserves identity and surviving independent references.
		if got := partitionDetachCapture(t, p, b.DB, identityQ); !reflect.DeepEqual(got, identities) {
			t.Fatalf("failed drop changed identities: %#v", got)
		}
		run(fixture.Exec("ALTER TABLE pd_parent ATTACH PARTITION pd_child FOR VALUES FROM(1) TO(3)", 0))
		run(partitionDetachMember("pd_child", "pd_parent", true))
		run(fixture.Exec("ALTER TABLE pd_parent DETACH PARTITION pd_child", 0))
		run(fixture.Exec("INSERT INTO pd_child(payload,k) VALUES('outside',7)", 1))
		run(fixture.ImplicitError("ALTER TABLE pd_parent ATTACH PARTITION pd_child FOR VALUES FROM(1) TO(3)", fixture.Diagnostic{Code: "23514", Message: `partition constraint of relation "pd_child" is violated by some row`, Schema: "public", Table: "pd_child"}))
		run(partitionDetachClass("pd_child", false))
		run(fixture.Exec("DROP VIEW pd_parent_view", 0))
		run(fixture.Exec("DROP TABLE pd_parent", 0))
		run(referenceRangeRows("SELECT k FROM pd_child_view ORDER BY k", 1, 2, 7))
		run(fixture.Exec("ALTER TABLE pd_child DROP CONSTRAINT pd_child_key", 0))
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='pd_child_key'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("INSERT INTO pd_child(payload) VALUES('sequence')", 1))
		run(referenceRangeRows("SELECT k FROM pd_child ORDER BY k", 1, 2, 4, 7))
	})
	partitionDetachRegclassBound(t)
	partitionDetachTypedDependencies(t)
}

func partitionDetachRegclassBound(t *testing.T) {
	t.Run("regclass_bound", func(t *testing.T) {
		partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pd_bound_object(k INT)", 0))
			run(fixture.Exec("CREATE TABLE pd_bound_parent(k REGCLASS) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pd_bound_child PARTITION OF pd_bound_parent FOR VALUES FROM('pd_bound_object') TO(MAXVALUE)", 0))
			run(fixture.Exec("INSERT INTO pd_bound_child VALUES('pd_bound_object')", 1))
			run(fixture.Exec("ALTER TABLE pd_bound_parent DETACH PARTITION pd_bound_child", 0))
			run(fixture.Select("SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass::OID AND objid='pd_bound_child'::regclass::OID AND refobjid='pd_bound_object'::regclass::OID", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("DROP TABLE pd_bound_object", 0)) // Stored REGCLASS cells are not dependency edges.
			run(fixture.Select("SELECT count(*) FROM pd_bound_child", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	})
}

func partitionDetachTypedDependencies(t *testing.T) {
	t.Run("type_collation_rowtype", func(t *testing.T) {
		partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec(`CREATE COLLATION pd_coll FROM pg_catalog."C"`, 0))
			run(fixture.Exec("CREATE DOMAIN pd_domain AS INT", 0))
			run(fixture.Exec("CREATE TABLE pd_typed(k INT,payload TEXT COLLATE pd_coll,n pd_domain DEFAULT 7) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pd_typed_child PARTITION OF pd_typed DEFAULT", 0))
			run(fixture.Exec("CREATE TABLE pd_rowrefs(value pd_typed_child)", 0))
			run(fixture.Exec("INSERT INTO pd_typed(k,payload) VALUES(1,'a')", 1))
			run(fixture.Exec("INSERT INTO pd_rowrefs VALUES($1::pd_typed_child)", 1, "(1,a,7)"))
			run(fixture.Exec("CREATE INDEX pd_typed_local ON pd_typed_child(payload)", 0))
			q := "SELECT classid::BIGINT,objid::BIGINT,objsubid,refclassid::BIGINT,refobjid::BIGINT,refobjsubid,deptype FROM pg_depend ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype"
			before := partitionDetachCapture(t, p, b.DB, q)
			child := partitionDetachCapture(t, p, b.DB, "SELECT oid::BIGINT,reltype::BIGINT FROM pg_class WHERE relname='pd_typed_child'")
			parent := partitionDetachCapture(t, p, b.DB, "SELECT oid::BIGINT FROM pg_class WHERE relname='pd_typed'")
			var expected [][]any
			removed := 0
			for _, edge := range before {
				if edge[0] == int64(1259) && edge[1] == child[0][0] && edge[3] == int64(1259) && edge[4] == parent[0][0] && edge[6] == "a" {
					removed++
					continue
				}
				expected = append(expected, edge)
			}
			if removed != 1 {
				t.Fatalf("expected exactly one membership edge, got %d", removed)
			}
			run(fixture.Prepared(fixture.Exec("ALTER TABLE pd_typed DETACH PARTITION pd_typed_child", 0)))
			run(fixture.Select(q, []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, expected))
			run(fixture.Select("SELECT oid::BIGINT,reltype::BIGINT FROM pg_class WHERE relname='pd_typed_child'", []string{"oid", "reltype"}, []string{"INT8", "INT8"}, child))
			run(fixture.Prepared(fixture.Select("SELECT (value).k AS k,(value).payload AS payload,(value).n::INT AS n FROM pd_rowrefs", []string{"k", "payload", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "a", int64(7)}})))
			run(fixture.Exec("INSERT INTO pd_typed_child(k,payload) VALUES(9,'z')", 1))
			run(fixture.Exec("DROP TABLE pd_typed", 0))
			run(fixture.Select("SELECT k,payload,n::INT AS n FROM pd_typed_child ORDER BY k", []string{"k", "payload", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "a", int64(7)}, {int64(9), "z", int64(7)}}))
			run(fixture.Select("SELECT (value).k AS k FROM pd_rowrefs", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT count(*) FROM pg_index WHERE indexrelid='pd_typed_local'::regclass::OID AND indisvalid", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	})
}
