package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

var shColumns = []string{"dbid", "classid", "objid", "objsubid", "refclassid", "refobjid", "deptype"}
var shTypes = []string{"OID", "OID", "OID", "INT4", "OID", "OID", "CHAR"}

func shExec(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
	}
}

func shOID(t *testing.T, p *fixture.Pair, executor fixture.Executor, q string, args ...any) int64 {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if !rows.Next() {
		t.Fatal("missing identity", q, rows.Err())
	}
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("nonunique identity", q, rows.Err())
	}
	return oid
}

func shEdge(db, class, object, sub, role int64, kind string) []any {
	return []any{db, class, object, sub, int64(1260), role, kind}
}

func shSelect(q string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, shColumns, shTypes, rows, args...)
	s.Bag = true
	return s
}

func shBag(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, class, object int64, expected ...[]any) {
	t.Helper()
	s := shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE classid=$1 AND objid=$2", expected, class, object)
	fixture.PgDescriptionExecutorRows(t, p, b, executor, s)
}

func shDatabaseBag(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, database, class, object int64, expected ...[]any) {
	t.Helper()
	fixture.PgDescriptionExecutorRows(t, p, b, executor, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=$2 AND objid=$3", expected, database, class, object))
}

func shSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) (db, table, owner, reader, grantor int64) {
	t.Helper()
	shExec(t, p, b, b.DB, "CREATE ROLE sh_owner", "CREATE ROLE sh_reader", "CREATE ROLE sh_grantor", "CREATE TABLE sh_table(a INT,b INT)", "ALTER TABLE sh_table OWNER TO sh_owner")
	db = shOID(t, p, b.DB, "SELECT oid FROM pg_database WHERE datname=current_database()")
	table = shOID(t, p, b.DB, "SELECT oid FROM pg_class WHERE relname='sh_table' AND relnamespace='public'::regnamespace")
	owner = shOID(t, p, b.DB, "SELECT oid FROM pg_roles WHERE rolname='sh_owner'")
	reader = shOID(t, p, b.DB, "SELECT oid FROM pg_roles WHERE rolname='sh_reader'")
	grantor = shOID(t, p, b.DB, "SELECT oid FROM pg_roles WHERE rolname='sh_grantor'")
	return
}

func shScenario(t *testing.T) *fixture.Pair {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"sh_group", "sh_member", "sh_renamed", "sh_grantor", "sh_reader", "sh_owner"})
	fixture.SchemaReferenceCleanup(t, "sh_schema")
	return fixture.NewScenario(t)
}

func shRetained(t *testing.T, rows *sql.Rows, expected [][]any) {
	t.Helper()
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(shColumns) {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		if typ.Name() != shColumns[i] || typ.DatabaseTypeName() != shTypes[i] {
			t.Fatal("retained metadata", i, typ)
		}
	}
	var got [][]any
	for rows.Next() {
		var db, class, object, sub, refclass, role int64
		var kind string
		if err := rows.Scan(&db, &class, &object, &sub, &refclass, &role, &kind); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{db, class, object, sub, refclass, role, kind})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	order := func(v [][]any) { sort.Slice(v, func(i, j int) bool { return fmt.Sprint(v[i]) < fmt.Sprint(v[j]) }) }
	order(got)
	order(expected)
	if len(got) != len(expected) || len(got) > 0 && !reflect.DeepEqual(got, expected) {
		t.Fatalf("edges got %v want %v", got, expected)
	}
}
