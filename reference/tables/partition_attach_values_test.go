package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionAttachValues(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		t.Run(route, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE pa_values(k INT,payload TEXT DEFAULT 'parent') PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pa_values_child(payload TEXT DEFAULT 'child',k INT)", 0))
					run(fixture.Exec("CREATE TABLE pa_values_identity AS SELECT oid FROM pg_class WHERE relname='pa_values_child'", 1))
					run(fixture.Exec("INSERT INTO pa_values_child VALUES('one',1),('two',2)", 2))
					run(fixture.Exec("CREATE TABLE pa_values_sibling PARTITION OF pa_values FOR VALUES FROM(3) TO(5)", 0))
					run(fixture.Exec("INSERT INTO pa_values_sibling VALUES(3,'three')", 1))
					readQuery := "SELECT k,payload FROM pa_values ORDER BY k NULLS LAST"
					read, err := conn.PrepareContext(p.Ctx, readQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer read.Close()
					insertQuery := "INSERT INTO pa_values(k) VALUES($1)"
					insert, err := conn.PrepareContext(p.Ctx, insertQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer insert.Close()
					reader := fixture.HeldStatement{Executor: conn, Statement: read, SQL: readQuery}
					writer := fixture.HeldStatement{Executor: conn, Statement: insert, SQL: insertQuery}
					fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select(readQuery, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "three"}}))
					run(fixture.Exec("ALTER TABLE pa_values ATTACH PARTITION pa_values_child FOR VALUES FROM(1) TO(3)", 0))
					run(fixture.Select("SELECT c.oid=s.oid AS unchanged FROM pg_class c JOIN pa_values_identity s ON true WHERE c.relname='pa_values_child'", []string{"unchanged"}, []string{"BOOL"}, [][]any{{true}}))
					want := fixture.Select(readQuery, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}})
					want.CheckMetadata = referenceRangeMetadata(b.PG, want.Columns, want.Types, []bool{true, true})
					fixture.RunStep(t, p.Ctx, reader, b.Recorder, want)
					child := fixture.Select("SELECT * FROM pa_values_child ORDER BY k", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"one", int64(1)}, {"two", int64(2)}})
					child.CheckMetadata = referenceRangeMetadata(b.PG, child.Columns, child.Types, []bool{true, true})
					run(child)
					run(fixture.Select("SELECT k,payload FROM ONLY pa_values", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil))
					fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec(insertQuery, 1, 4))
					run(fixture.Exec("INSERT INTO pa_values_child(k) VALUES(2)", 1))
					run(fixture.Select("SELECT payload FROM pa_values WHERE k=2 ORDER BY payload", []string{"payload"}, []string{"TEXT"}, [][]any{{"child"}, {"two"}}))
					run(referenceRangeLeafMiss("INSERT INTO pa_values_child VALUES('outside',3)", "pa_values_child", "outside, 3"))
					physical := fixture.Select("SELECT tableoid='pa_values_child'::regclass::OID AS physical FROM pa_values WHERE k=1", []string{"physical"}, []string{"BOOL"}, [][]any{{true}})
					if !b.PG {
						physical = fixture.ImplicitError(physical.Query, fixture.Diagnostic{Code: "42703", Message: `column "tableoid" does not exist`, Position: "8"})
					}
					run(physical)
					run(fixture.Exec("CREATE TABLE pa_values_default(payload TEXT,k INT)", 0))
					run(fixture.Exec("INSERT INTO pa_values_default VALUES('null',NULL),('ten',10)", 2))
					run(fixture.Exec("ALTER TABLE pa_values ATTACH PARTITION pa_values_default DEFAULT", 0))
					want.Rows = [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(2), "child"}, {int64(3), "three"}, {int64(4), "parent"}, {int64(10), "ten"}, {nil, "null"}}
					want.Bag = true
					fixture.RunStep(t, p.Ctx, reader, b.Recorder, want)
					run(fixture.Exec("CREATE TABLE pa_values_empty(k INT,payload TEXT)", 0))
					run(fixture.Exec("ALTER TABLE pa_values ATTACH PARTITION pa_values_empty FOR VALUES FROM(5) TO(7)", 0))
					empty := fixture.Select("SELECT * FROM pa_values_empty", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil)
					empty.CheckMetadata = referenceRangeMetadata(b.PG, empty.Columns, empty.Types, []bool{true, true})
					run(empty)
					fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec(insertQuery, 1, 6))
					run(fixture.Select("SELECT k,payload FROM pa_values_empty", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(6), "parent"}}))
				})
			}
		})
	}
	partitionAttachTypedValues(t)
	partitionAttachNestedValues(t)
	partitionAttachMappedConsumers(t)
}

func partitionAttachTypedValues(t *testing.T) {
	cases := []struct{ name, setup, typ, key, lower, upper, value, want string }{
		{"bigint", "", "BIGINT", "k", "9007199254740993", "9007199254740995", "9007199254740994", "9007199254740994"},
		{"numeric", "", "NUMERIC(8,2)", "k", "'1.20'", "'2.30'", "'1.25'", "1.25"},
		{"text", "", `TEXT COLLATE "C"`, "k", "'a'", "'z'", "'m'", "m"},
		{"enum", "CREATE TYPE pa_value_type AS ENUM('red','green','blue')", "pa_value_type", "k", "'red'", "'blue'", "'green'", "green"},
		{"domain", "CREATE DOMAIN pa_value_type AS BIGINT", "pa_value_type", "k", "1", "3", "2", "2"},
		{"array", "", "INT[]", "k", "ARRAY[1]", "ARRAY[3]", "ARRAY[2]", "{2}"},
		{"expression", "", "INT", "(k+1)", "2", "4", "2", "2"},
		{"sentinels", "", "INT", "k", "MINVALUE", "MAXVALUE", "-99", "-99"},
	}
	for _, c := range cases {
		t.Run("typed/"+c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					if c.setup != "" {
						run(fixture.Exec(c.setup, 0))
					}
					if c.name == "array" && !b.PG {
						// DEFINE has no native integer[] btree provider. Retain the
						// genuine PostgreSQL ATTACH values below without adding one.
						run(fixture.ImplicitError("CREATE TABLE pa_typed_parent(k "+c.typ+") PARTITION BY RANGE("+c.key+")", fixture.Diagnostic{Code: "42704", Message: `data type integer[] has no default operator class for access method "btree"`, Hint: "You must specify an operator class for the index or define a default operator class for the data type."}))
						return
					}
					run(fixture.Exec("CREATE TABLE pa_typed_parent(k "+c.typ+") PARTITION BY RANGE("+c.key+")", 0))
					run(fixture.Exec("CREATE TABLE pa_typed_child(k "+c.typ+")", 0))
					run(fixture.Exec("INSERT INTO pa_typed_child VALUES("+c.value+")", 1))
					run(fixture.Prepared(fixture.Exec("ALTER TABLE pa_typed_parent ATTACH PARTITION pa_typed_child FOR VALUES FROM("+c.lower+") TO("+c.upper+")", 0)))
					for _, prepared := range []bool{false, true} {
						s := fixture.Select("SELECT k::TEXT AS k FROM pa_typed_parent", []string{"k"}, []string{"TEXT"}, [][]any{{c.want}})
						if c.name == "numeric" {
							// Exercise the admitted numeric output/metadata directly;
							// numeric-to-TEXT conversion is not ATTACH functionality.
							s = fixture.Select("SELECT k FROM pa_typed_parent", []string{"k"}, []string{"NUMERIC"}, [][]any{{c.want}})
						}
						s.Prepared = prepared
						run(s)
					}
				})
			}
		})
	}
}

func partitionAttachNestedValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run("nested/"+b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pa_nested(k INT,j INT,payload TEXT) PARTITION BY RANGE(k,j)", 0))
			run(fixture.Exec("CREATE TABLE pa_nested_mid(payload TEXT,j INT,k INT) PARTITION BY RANGE((j+1))", 0))
			run(fixture.Exec("CREATE TABLE pa_nested_leaf(j INT,k INT,payload TEXT)", 0))
			run(fixture.Exec("INSERT INTO pa_nested_leaf VALUES(9,1,'lexicographic')", 1))
			run(fixture.Exec("ALTER TABLE pa_nested_mid ATTACH PARTITION pa_nested_leaf DEFAULT", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE pa_nested ATTACH PARTITION pa_nested_mid FOR VALUES FROM(1,5) TO(2,0)", 0)))
			run(fixture.Select("SELECT * FROM pa_nested", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(9), "lexicographic"}}))
			run(fixture.Exec("INSERT INTO pa_nested VALUES(1,8,'routed')", 1))
			run(fixture.Select("SELECT * FROM pa_nested_leaf ORDER BY j", []string{"j", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(8), int64(1), "routed"}, {int64(9), int64(1), "lexicographic"}}))
			run(fixture.Exec("CREATE TABLE pa_childless(k INT,j INT,payload TEXT) PARTITION BY RANGE(j)", 0))
			run(fixture.Exec("ALTER TABLE pa_nested ATTACH PARTITION pa_childless FOR VALUES FROM(3,MINVALUE) TO(4,MINVALUE)", 0))
			run(fixture.Select("SELECT * FROM pa_childless", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "TEXT"}, nil))
		})
	}
}
