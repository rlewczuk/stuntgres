package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgTSConfigTypes = []string{"OID", "NAME", "OID", "OID", "OID"}

const pgTSConfigAbsent = "ptsc_absent_scenario"
const pgTSConfigNameProbe = "SELECT * FROM pg_catalog.pg_ts_config WHERE cfgname=$1"
const pgTSConfigOIDProbe = "SELECT * FROM pg_catalog.pg_ts_config WHERE oid=$1"

func pgTSConfigScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgTSConfigSimple(b fixture.Backend) [][]any {
	return [][]any{{int64(3748), "simple", int64(11), int64(10), int64(3722)}}
}

func pgTSConfigSavedMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != len(fixture.PGTSConfigColumns) {
		t.Fatal("configuration descriptor width", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 1 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if typ.Name() != fixture.PGTSConfigColumns[i] || typ.DatabaseTypeName() != pgTSConfigTypes[i] || typ.ScanType() != scan {
			t.Fatalf("configuration descriptor %d: %q/%q/%v, want %q/%q/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), fixture.PGTSConfigColumns[i], pgTSConfigTypes[i], scan)
		}
		if nullable, known := typ.Nullable(); nullable || known != !pg {
			t.Fatal("configuration nullability", i, nullable, known)
		}
		if length, known := typ.Length(); length != 0 || known {
			t.Fatal("configuration length", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("configuration decimal size", i, precision, scale, known)
		}
	}
}

func pgTSConfigMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, fixture.PGTSConfigColumns) {
		t.Fatal("configuration columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgTSConfigSavedMetadata(t, types, pg)
	return types
}

func pgTSConfigStep(b fixture.Backend, q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fixture.PGTSConfigColumns, pgTSConfigTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgTSConfigMetadata(t, rows, b.PG) }
	return s
}

func pgTSConfigBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
	fixture.PgDescriptionExecutorRows(t, p, b, e, fixture.Prepared(s))
}

func pgTSConfigInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	for _, relation := range []string{"pg_catalog.pg_ts_config", "pg_ts_config"} {
		q := "SELECT * FROM " + relation + " WHERE cfgname='simple'"
		s := pgTSConfigStep(b, q, pgTSConfigSimple(b))
		s.Bag = true
		pgTSConfigBoth(t, p, b, e, s)
	}
}

func TestReferencePGTSConfigInventory(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgTSConfigInventory(t, p, b, b.DB)
		for _, name := range []any{"simple", pgTSConfigAbsent, nil} {
			want := [][]any{}
			if name == "simple" {
				want = pgTSConfigSimple(b)
			}
			pgTSConfigBoth(t, p, b, b.DB, pgTSConfigStep(b, pgTSConfigNameProbe, want, name))
		}
		for _, oid := range []any{int64(3748), int64(0), int64(4294967295), nil} {
			want := [][]any{}
			if oid == int64(3748) {
				want = pgTSConfigSimple(b)
			}
			pgTSConfigBoth(t, p, b, b.DB, pgTSConfigStep(b, pgTSConfigOIDProbe, want, oid))
		}
		if !b.PG {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE EXTENSION unaccent VERSION '1.1'", 0))
			pgTSConfigInventory(t, p, b, b.DB)
		}
	})
}

func TestReferencePGTSConfigShape(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, relation := range []string{"pg_catalog.pg_ts_config", "pg_ts_config"} {
			q := "SELECT * FROM " + relation + " WHERE FALSE"
			pgTSConfigBoth(t, p, b, b.DB, pgTSConfigStep(b, q, [][]any{}))
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, prepareErr := b.DB.PrepareContext(p.Ctx, q)
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = b.DB.QueryContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				saved := pgTSConfigMetadata(t, rows, b.PG)
				if rows.Next() || rows.Err() != nil {
					t.Fatal("empty configuration cursor", rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				pgTSConfigSavedMetadata(t, saved, b.PG)
			}
		}
	})
}

func TestReferencePGTSConfigCatalogLinks(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		both := func(s fixture.Step) { pgTSConfigBoth(t, p, b, b.DB, s) }
		both(fixture.Select("SELECT oid,relname,relnamespace,relowner,relkind,relpersistence,relnatts,relhasindex FROM pg_class WHERE oid=3602", []string{"oid", "relname", "relnamespace", "relowner", "relkind", "relpersistence", "relnatts", "relhasindex"}, []string{"OID", "NAME", "OID", "OID", "CHAR", "CHAR", "INT2", "BOOL"}, [][]any{{int64(3602), "pg_ts_config", int64(11), int64(10), "r", "p", int64(5), true}}))
		both(fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation,attndims FROM pg_attribute WHERE attrelid=3602 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attnotnull", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "INT4", "BOOL", "OID", "INT2"}, [][]any{
			{int64(1), "oid", int64(26), int64(-1), true, int64(0), int64(0)},
			{int64(2), "cfgname", int64(19), int64(-1), true, int64(950), int64(0)},
			{int64(3), "cfgnamespace", int64(26), int64(-1), true, int64(0), int64(0)},
			{int64(4), "cfgowner", int64(26), int64(-1), true, int64(0), int64(0)},
			{int64(5), "cfgparser", int64(26), int64(-1), true, int64(0), int64(0)},
		}))
		both(fixture.Select("SELECT reltype='pg_catalog.pg_ts_config'::regtype::oid AS row_type_link FROM pg_class WHERE oid=3602", []string{"row_type_link"}, []string{"BOOL"}, [][]any{{true}}))
		both(fixture.Select("SELECT t.typrelid,t.typname,a.typname AS array_name,t.typnamespace,a.typnamespace AS array_namespace,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=3602", []string{"typrelid", "typname", "array_name", "typnamespace", "array_namespace", "element_link", "array_link"}, []string{"OID", "NAME", "NAME", "OID", "OID", "BOOL", "BOOL"}, [][]any{{int64(3602), "pg_ts_config", "_pg_ts_config", int64(11), int64(11), true, true}}))
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		both(fixture.Select("SELECT n.nspname,r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid=3602", []string{"nspname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"pg_catalog", owner}}))
		both(fixture.Select("SELECT schemaname,tablename,tableowner,tablespace,hasindexes,hasrules,hastriggers,rowsecurity FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_ts_config'", []string{"schemaname", "tablename", "tableowner", "tablespace", "hasindexes", "hasrules", "hastriggers", "rowsecurity"}, []string{"NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_catalog", "pg_ts_config", owner, nil, true, false, false, false}}))
		both(fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_ts_config'", []string{"viewname"}, []string{"NAME"}, [][]any{}))
		links := fixture.Select("SELECT fktable::oid,fkcols,pktable::oid,pkcols,is_array,is_opt FROM pg_get_catalog_foreign_keys() WHERE fktable::oid=3602", []string{"fktable", "fkcols", "pktable", "pkcols", "is_array", "is_opt"}, []string{"OID", "_TEXT", "OID", "_TEXT", "BOOL", "BOOL"}, [][]any{
			{int64(3602), "{cfgnamespace}", int64(2615), "{oid}", false, false},
			{int64(3602), "{cfgowner}", int64(1260), "{oid}", false, false},
			{int64(3602), "{cfgparser}", int64(3601), "{oid}", false, false},
		})
		links.Bag = true
		both(links)
		both(fixture.Select("SELECT c.oid,n.nspname,r.oid AS owner,p.oid AS parser,p.prsname FROM pg_ts_config c JOIN pg_namespace n ON n.oid=c.cfgnamespace JOIN pg_roles r ON r.oid=c.cfgowner JOIN pg_ts_parser p ON p.oid=c.cfgparser WHERE c.cfgname='simple'", []string{"oid", "nspname", "owner", "parser", "prsname"}, []string{"OID", "NAME", "OID", "OID", "NAME"}, [][]any{{int64(3748), "pg_catalog", int64(10), int64(3722), "default"}}))
	})
}
