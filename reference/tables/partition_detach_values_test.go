package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func partitionDetachScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func partitionDetachSetup(run func(fixture.Step), parent string) {
	partitionDetachSetupConstraints(run, parent, "")
}

func partitionDetachSetupConstraints(run func(fixture.Step), parent, childConstraints string) {
	run(fixture.Exec("CREATE SEQUENCE pd_seq START 4", 0))
	run(fixture.Exec("CREATE TABLE "+parent+"(k INT CONSTRAINT pd_parent_nn NOT NULL,payload TEXT DEFAULT 'parent',CONSTRAINT pd_check CHECK(k>0),CONSTRAINT pd_key UNIQUE(k)) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pd_child(obsolete INT,payload TEXT DEFAULT 'child',k INT CONSTRAINT pd_child_nn NOT NULL DEFAULT nextval('pd_seq'),CONSTRAINT pd_check CHECK(k>0),CONSTRAINT pd_child_key UNIQUE(k),CONSTRAINT pd_local CHECK(length(payload)>0)"+childConstraints+")", 0))
	run(fixture.Exec("ALTER TABLE pd_child DROP COLUMN obsolete", 0))
	run(fixture.Exec("ALTER SEQUENCE pd_seq OWNED BY pd_child.k", 0))
	run(fixture.Exec("INSERT INTO pd_child(payload,k) VALUES('a',1),('b',2)", 2))
	run(fixture.Exec("ALTER TABLE "+parent+" ATTACH PARTITION pd_child FOR VALUES FROM(1) TO(3)", 0))
	run(fixture.Exec("CREATE TABLE pd_sibling PARTITION OF "+parent+" FOR VALUES FROM(3) TO(5)", 0))
	run(fixture.Exec("INSERT INTO pd_sibling VALUES(3,'c')", 1))
	run(fixture.Exec("CREATE TABLE pd_default PARTITION OF "+parent+" DEFAULT", 0))
}

func partitionDetachMember(child, parent string, attached bool) fixture.Step {
	count := int64(0)
	if attached {
		count = 1
	}
	return fixture.Select("SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass::OID AND objid='"+child+"'::regclass::OID AND refclassid='pg_class'::regclass::OID AND refobjid='"+parent+"'::regclass::OID AND objsubid=0 AND refobjsubid=0 AND deptype='a'", []string{"count"}, []string{"INT8"}, [][]any{{count}})
}

func partitionDetachTopEdges(index string, attached bool) fixture.Step {
	var rows [][]any
	if attached {
		rows = [][]any{{"P"}, {"S"}}
	}
	return fixture.Select("SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::OID AND objid='"+index+"'::regclass::OID AND (deptype='P' OR deptype='S') ORDER BY deptype", []string{"deptype"}, []string{"CHAR"}, rows)
}

func partitionDetachClass(name string, attached bool) fixture.Step {
	return fixture.Select("SELECT relispartition FROM pg_class WHERE relname='"+name+"'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{attached}})
}

func partitionDetachHold(t *testing.T, p *fixture.Pair, conn *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("Prepare must not execute utility or acquire execution locks", q, err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
}

func TestReferencePartitionDetachValues(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "direct"
		if held {
			name = "held"
		}
		t.Run(name, func(t *testing.T) {
			partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close() })
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				partitionDetachSetup(run, "pd_parent")
				parentQ, childQ := "SELECT k,payload FROM pd_parent ORDER BY k", "SELECT * FROM pd_child ORDER BY k"
				parent := partitionDetachHold(t, p, conn, parentQ)
				child := partitionDetachHold(t, p, conn, childQ)
				writer := partitionDetachHold(t, p, conn, "INSERT INTO pd_parent VALUES($1,$2)")
				q := "ALTER TABLE pd_parent DETACH PARTITION pd_child"
				utility := partitionDetachHold(t, p, conn, q)
				result, argumentErr := utility.Statement.ExecContext(p.Ctx, 1)
				if result != nil {
					t.Fatal("zero-parameter utility published a result for extra argument")
				}
				fixture.TransactionOrdinary(t, argumentErr, "sql: expected 0 arguments, got 1")
				fixture.RunStep(t, p.Ctx, parent, b.Recorder, fixture.Select(parentQ, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}}))
				run(partitionDetachClass("pd_child", true)) // Quiet Prepare cannot detach.
				if held {
					fixture.RunStep(t, p.Ctx, utility, b.Recorder, fixture.Exec(q, 0))
				} else {
					s := fixture.Exec(q, 0)
					s.NonContext = true
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
				want := fixture.Select(parentQ, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}})
				want.CheckMetadata = referenceRangeMetadata(b.PG, want.Columns, want.Types, []bool{false, true})
				fixture.RunStep(t, p.Ctx, parent, b.Recorder, want)
				want = fixture.Select(childQ, []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(1)}, {"b", int64(2)}})
				want.CheckMetadata = referenceRangeMetadata(b.PG, want.Columns, want.Types, []bool{true, false})
				fixture.RunStep(t, p.Ctx, child, b.Recorder, want)
				empty := fixture.Select("SELECT k,payload FROM pd_parent WHERE k=1", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil)
				empty.CheckMetadata = referenceRangeMetadata(b.PG, empty.Columns, empty.Types, []bool{false, true})
				run(fixture.Prepared(empty))
				run(partitionDetachClass("pd_child", false))
				run(partitionDetachMember("pd_child", "pd_parent", false))
				run(fixture.Exec("INSERT INTO pd_child(payload) VALUES('d')", 1))
				fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec(writer.SQL, 1, 2, "default"))
				run(referenceRangeRows("SELECT k FROM pd_default", 2))
				run(partitionConstraintDuplicate("INSERT INTO pd_child(payload,k) VALUES('duplicate',1)", "pd_child", "pd_child_key", "Key (k)=(1) already exists."))
				run(fixture.ImplicitError("INSERT INTO pd_child(payload,k) VALUES('zero',0)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "pd_child" violates check constraint "pd_check"`, Detail: "Failing row contains (zero, 0).", Schema: "public", Table: "pd_child", Constraint: "pd_check"}))
				run(fixture.ImplicitError("INSERT INTO pd_child(payload,k) VALUES('null',NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "k" of relation "pd_child" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "pd_child", Column: "k"}))
				run(referenceRangeRows("SELECT k FROM pd_child ORDER BY k", 1, 2, 4))
				run(referenceRangeRows("SELECT k FROM pd_parent ORDER BY k", 2, 3))
			})
		})
	}
}

func TestReferencePartitionDetachNestedDefault(t *testing.T) {
	partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE pd_nd(k INT,j INT,CONSTRAINT pd_nd_key PRIMARY KEY(k,j)) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pd_mid(j INT NOT NULL,k INT NOT NULL,CONSTRAINT pd_mid_key PRIMARY KEY(k,j)) PARTITION BY RANGE(j)", 0))
		run(fixture.Exec("CREATE TABLE pd_leaf PARTITION OF pd_mid DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pd_leaf VALUES(9,1)", 1))
		run(fixture.Exec("ALTER TABLE pd_nd ATTACH PARTITION pd_mid FOR VALUES FROM(1) TO(3)", 0))
		run(fixture.Exec("CREATE TABLE pd_def PARTITION OF pd_nd DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pd_def VALUES(8,8)", 1))
		identityQ := "SELECT oid::BIGINT,relname,relowner::BIGINT,reltype::BIGINT FROM pg_class WHERE relname='pd_mid' OR relname='pd_leaf' OR relname='pd_mid_key' OR relname='pd_leaf_pkey' ORDER BY relname"
		identities := partitionDetachCapture(t, p, b.DB, identityQ)
		constraintQ := "SELECT oid::BIGINT,conname,conindid::BIGINT FROM pg_constraint WHERE conrelid='pd_mid'::regclass::OID OR conrelid='pd_leaf'::regclass::OID ORDER BY oid"
		constraints := partitionDetachCapture(t, p, b.DB, constraintQ)
		run(fixture.Prepared(fixture.Exec("ALTER TABLE pd_nd DETACH PARTITION pd_def", 0)))
		run(fixture.Exec("INSERT INTO pd_def VALUES(1,1)", 1))
		run(referenceRangeMiss("INSERT INTO pd_nd VALUES(8,8)", "pd_nd", "k", "8"))
		run(fixture.Exec("ALTER TABLE pd_nd DETACH PARTITION pd_mid", 0))
		run(fixture.Select("SELECT k,j FROM pd_nd", []string{"k", "j"}, []string{"INT4", "INT4"}, nil))
		run(partitionDetachClass("pd_mid", false))
		run(partitionDetachClass("pd_leaf", true))
		run(partitionDetachTopEdges("pd_mid_key", false))
		run(partitionConstraintEdge("pd_leaf_pkey", "pd_mid_key", "pd_leaf"))
		run(fixture.Select(identityQ, []string{"oid", "relname", "relowner", "reltype"}, []string{"INT8", "NAME", "INT8", "INT8"}, identities))
		run(fixture.Select(constraintQ, []string{"oid", "conname", "conindid"}, []string{"INT8", "NAME", "INT8"}, constraints))
		run(fixture.Select("SELECT contype,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pd_leaf'::regclass::OID ORDER BY contype,conname", []string{"contype", "conislocal", "coninhcount"}, []string{"CHAR", "BOOL", "INT2"}, [][]any{{"n", false, int64(1)}, {"n", false, int64(1)}, {"p", false, int64(1)}}))
		run(fixture.Exec("INSERT INTO pd_mid VALUES(10,7)", 1))
		run(fixture.Select("SELECT k,j FROM pd_mid ORDER BY k", []string{"k", "j"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(7), int64(10)}}))
		run(partitionConstraintDuplicate("INSERT INTO pd_mid VALUES(9,1)", "pd_leaf", "pd_leaf_pkey", "Key (k, j)=(1, 9) already exists."))
		run(fixture.Exec("DROP TABLE pd_nd", 0))
		run(referenceRangeRows("SELECT k FROM pd_mid ORDER BY k", 1, 7))
	})
}
