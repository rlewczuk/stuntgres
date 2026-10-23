package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgLanguageColumns = []string{"oid", "lanname", "lanowner", "lanispl", "lanpltrusted", "lanplcallfoid", "laninline", "lanvalidator", "lanacl"}
var pgLanguageTypes = []string{"OID", "NAME", "OID", "BOOL", "BOOL", "OID", "OID", "OID", "_ACLITEM"}

const pgLanguageProbe = "SELECT * FROM pg_catalog.pg_language WHERE lanname=$1"
const pgLanguageAbsent = "pglang_absent_scenario"

func pgLanguageInternal() [][]any {
	return [][]any{{int64(12), "internal", int64(10), false, false, int64(0), int64(0), int64(2246), nil}}
}

func pgLanguageBackends(t *testing.T, p *fixture.Pair, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgLanguageScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	pgLanguageBackends(t, fixture.NewScenario(t), body)
}

func pgLanguageSavedMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != len(pgLanguageColumns) {
		t.Fatal("language descriptor width", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 1 || i == 8 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if i == 3 || i == 4 {
			scan = reflect.TypeFor[bool]()
		}
		if typ.Name() != pgLanguageColumns[i] || typ.DatabaseTypeName() != pgLanguageTypes[i] || typ.ScanType() != scan {
			t.Fatalf("language descriptor %d: %q/%q/%v, want %q/%q/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), pgLanguageColumns[i], pgLanguageTypes[i], scan)
		}
		if nullable, known := typ.Nullable(); known != !pg || nullable != (!pg && i == 8) {
			t.Fatal("language nullability", i, nullable, known)
		}
		if length, known := typ.Length(); length != 0 || known {
			t.Fatal("language length", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("language decimal size", i, precision, scale, known)
		}
	}
}

func pgLanguageMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgLanguageColumns) {
		t.Fatal("language columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgLanguageSavedMetadata(t, types, pg)
}

func pgLanguageStep(b fixture.Backend, query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, pgLanguageColumns, pgLanguageTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgLanguageMetadata(t, rows, b.PG) }
	return s
}

func pgLanguageBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
	fixture.PgDescriptionExecutorRows(t, p, b, e, fixture.Prepared(s))
}

func pgLanguageInventory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	query, want := "SELECT * FROM pg_catalog.pg_language WHERE lanname IN ('internal','c','sql') ORDER BY oid", pgLanguageInternal()
	want = append(want,
		[]any{int64(13), "c", int64(10), false, false, int64(0), int64(0), int64(2247), nil},
		[]any{int64(14), "sql", int64(10), false, true, int64(0), int64(0), int64(2248), nil})
	pgLanguageBoth(t, p, b, b.DB, pgLanguageStep(b, query, want))
	pgLanguageBoth(t, p, b, b.DB, pgLanguageStep(b, pgLanguageProbe, [][]any{}, pgLanguageAbsent))
}

func TestReferencePGLanguageInventory(t *testing.T) {
	pgLanguageScenario(t, pgLanguageInventory)
}

func TestReferencePGLanguageShape(t *testing.T) {
	pgLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, relation := range []string{"pg_catalog.pg_language", "pg_language"} {
			pgLanguageBoth(t, p, b, b.DB, pgLanguageStep(b, "SELECT * FROM "+relation+" WHERE lanname='internal'", pgLanguageInternal()))
			pgLanguageBoth(t, p, b, b.DB, pgLanguageStep(b, "SELECT * FROM "+relation+" WHERE FALSE", [][]any{}))
		}
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		pgLanguageBoth(t, p, b, b.DB, fixture.Select("SELECT l.oid,l.lanname,r.rolname FROM pg_language l JOIN pg_roles r ON r.oid=l.lanowner WHERE l.oid=12", []string{"oid", "lanname", "rolname"}, []string{"OID", "NAME", "NAME"}, [][]any{{int64(12), "internal", owner}}))
	})
}

func pgLanguageHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	for _, value := range []any{"internal", pgLanguageAbsent, nil, "internal"} {
		want := [][]any{}
		if value == "internal" {
			want = pgLanguageInternal()
		}
		fixture.PgDescriptionExecutorRows(t, p, b, e, pgLanguageStep(b, pgLanguageProbe, want, value))
	}
}

func TestReferencePGLanguageMetadata(t *testing.T) {
	pgLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgLanguageBoth(t, p, b, b.DB, pgLanguageStep(b, pgLanguageProbe, pgLanguageInternal(), "internal"))
		for _, value := range []any{pgLanguageAbsent, nil} {
			pgLanguageBoth(t, p, b, b.DB, pgLanguageStep(b, pgLanguageProbe, [][]any{}, value))
		}
		pgLanguageHeldReads(t, p, b, b.DB)
	})
}

func pgLanguageHeldReads(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	stmt, err := e.PrepareContext(p.Ctx, pgLanguageProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	pgLanguageHeld(t, p, b, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: pgLanguageProbe})
	query := "SELECT * FROM pg_catalog.pg_language WHERE oid=$1"
	stmtOID, err := e.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmtOID.Close()
	held := fixture.HeldStatement{Executor: e, Statement: stmtOID, SQL: query}
	for _, oid := range []int64{12, 0, 12} {
		want := [][]any{}
		if oid == 12 {
			want = pgLanguageInternal()
		}
		s := pgLanguageStep(b, query, want, oid)
		pgLanguageBoth(t, p, b, e, s)
		fixture.PgDescriptionExecutorRows(t, p, b, held, s)
	}
}

func pgLanguageCatalogLinks(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgLanguageBoth(t, p, b, b.DB, fixture.Select("SELECT oid,relname,relnamespace,relowner,relkind,relpersistence,relnatts,relhasindex FROM pg_class WHERE oid=2612", []string{"oid", "relname", "relnamespace", "relowner", "relkind", "relpersistence", "relnatts", "relhasindex"}, []string{"OID", "NAME", "OID", "OID", "CHAR", "CHAR", "INT2", "BOOL"}, [][]any{{int64(2612), "pg_language", int64(11), int64(10), "r", "p", int64(9), true}}))
	want := [][]any{
		{int64(1), "oid", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(2), "lanname", int64(19), int64(-1), true, int64(950), int64(0)},
		{int64(3), "lanowner", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(4), "lanispl", int64(16), int64(-1), true, int64(0), int64(0)},
		{int64(5), "lanpltrusted", int64(16), int64(-1), true, int64(0), int64(0)},
		{int64(6), "lanplcallfoid", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(7), "laninline", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(8), "lanvalidator", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(9), "lanacl", int64(1034), int64(-1), false, int64(0), int64(1)},
	}
	pgLanguageBoth(t, p, b, b.DB, fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation,attndims FROM pg_attribute WHERE attrelid=2612 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attnotnull", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "INT4", "BOOL", "OID", "INT2"}, want))
	pgLanguageBoth(t, p, b, b.DB, fixture.Select("SELECT reltype='pg_catalog.pg_language'::regtype::oid AS row_type_link FROM pg_class WHERE oid=2612", []string{"row_type_link"}, []string{"BOOL"}, [][]any{{true}}))
	pgLanguageBoth(t, p, b, b.DB, fixture.Select("SELECT t.typrelid,t.typname,a.typname AS array_name,t.typnamespace,a.typnamespace AS array_namespace,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=2612", []string{"typrelid", "typname", "array_name", "typnamespace", "array_namespace", "element_link", "array_link"}, []string{"OID", "NAME", "NAME", "OID", "OID", "BOOL", "BOOL"}, [][]any{{int64(2612), "pg_language", "_pg_language", int64(11), int64(11), true, true}}))
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	pgLanguageBoth(t, p, b, b.DB, fixture.Select("SELECT schemaname,tablename,tableowner,tablespace,hasindexes,hasrules,hastriggers,rowsecurity FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_language'", []string{"schemaname", "tablename", "tableowner", "tablespace", "hasindexes", "hasrules", "hastriggers", "rowsecurity"}, []string{"NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_catalog", "pg_language", owner, nil, true, false, false, false}}))
	pgLanguageBoth(t, p, b, b.DB, fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_language'", []string{"viewname"}, []string{"NAME"}, [][]any{}))
}

func TestReferencePGLanguageCatalogLinks(t *testing.T) {
	pgLanguageScenario(t, pgLanguageCatalogLinks)
}
