package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgTSConfigMapColumns = []string{"mapcfg", "maptokentype", "mapseqno", "mapdict"}
var pgTSConfigMapTypes = []string{"OID", "INT4", "INT4", "OID"}

const pgTSConfigMapProbe = "SELECT * FROM pg_catalog.pg_ts_config_map WHERE mapcfg=$1 ORDER BY maptokentype,mapseqno"

func pgTSConfigMapStock() [][]any {
	rows := [][]any{}
	for _, token := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
		rows = append(rows, []any{int64(3748), token, int64(1), int64(3765)})
	}
	return rows
}

func pgTSConfigMapSavedMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 4 {
		t.Fatal("mapping descriptor width", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if pg {
			scan = reflect.TypeFor[any]()
			if i == 1 || i == 2 {
				scan = reflect.TypeFor[int32]()
			}
		}
		if typ.Name() != pgTSConfigMapColumns[i] || typ.DatabaseTypeName() != pgTSConfigMapTypes[i] || typ.ScanType() != scan {
			t.Fatalf("mapping column %d: %s/%s/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType())
		}
		if nullable, known := typ.Nullable(); nullable || known != !pg {
			t.Fatal("mapping nullability", i, nullable, known)
		}
		if length, known := typ.Length(); length != 0 || known {
			t.Fatal("mapping length", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("mapping decimal size", i, precision, scale, known)
		}
	}
}

func pgTSConfigMapMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgTSConfigMapColumns) {
		t.Fatal("mapping columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgTSConfigMapSavedMetadata(t, types, pg)
	return types
}

func pgTSConfigMapStep(b fixture.Backend, q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, pgTSConfigMapColumns, pgTSConfigMapTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgTSConfigMapMetadata(t, rows, b.PG) }
	return s
}

func pgTSConfigMapInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	for _, relation := range []string{"pg_catalog.pg_ts_config_map", "pg_ts_config_map"} {
		for _, projection := range []string{"*", "mapcfg,maptokentype,mapseqno,mapdict"} {
			q := "SELECT " + projection + " FROM " + relation + " WHERE mapcfg=3748"
			pgTSConfigBoth(t, p, b, e, pgTSConfigMapStep(b, q+" ORDER BY mapcfg,maptokentype,mapseqno", pgTSConfigMapStock()))
			s := pgTSConfigMapStep(b, q, pgTSConfigMapStock())
			s.Bag = true
			pgTSConfigBoth(t, p, b, e, s)
		}
	}
}

func TestReferencePGTSConfigMapInventory(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgTSConfigMapInventory(t, p, b, b.DB)
	})
}

func TestReferencePGTSConfigMapShape(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, relation := range []string{"pg_catalog.pg_ts_config_map", "pg_ts_config_map"} {
			for _, projection := range []string{"*", "mapcfg,maptokentype,mapseqno,mapdict"} {
				for _, predicate := range []string{"FALSE", "mapcfg=0", "mapcfg=3748"} {
					q := "SELECT " + projection + " FROM " + relation + " WHERE " + predicate + " ORDER BY maptokentype,mapseqno"
					want := [][]any{}
					if predicate == "mapcfg=3748" {
						want = pgTSConfigMapStock()
					}
					pgTSConfigBoth(t, p, b, b.DB, pgTSConfigMapStep(b, q, want))
					for _, prepared := range []bool{false, true} {
						func() {
							var rows *sql.Rows
							var err error
							if prepared {
								stmt, failure := b.DB.PrepareContext(p.Ctx, q)
								if failure != nil {
									t.Fatal(failure)
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
							saved := pgTSConfigMapMetadata(t, rows, b.PG)
							pgTSConfigMapConsume(t, rows, want)
							pgTSConfigMapSavedMetadata(t, saved, b.PG)
						}()
					}
				}
			}
		}
	})
}

func TestReferencePGTSConfigMapCatalogLinks(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		both := func(s fixture.Step) { pgTSConfigBoth(t, p, b, b.DB, s) }
		both(fixture.Select("SELECT oid,relname,relnamespace,relkind,relpersistence,relnatts,relhasindex FROM pg_class WHERE oid=3603", []string{"oid", "relname", "relnamespace", "relkind", "relpersistence", "relnatts", "relhasindex"}, []string{"OID", "NAME", "OID", "CHAR", "CHAR", "INT2", "BOOL"}, [][]any{{int64(3603), "pg_ts_config_map", int64(11), "r", "p", int64(4), true}}))
		attributes := [][]any{}
		for i, name := range pgTSConfigMapColumns {
			typeID := int64(26)
			if i == 1 || i == 2 {
				typeID = 23
			}
			attributes = append(attributes, []any{int64(i + 1), name, typeID, int64(-1), true, int64(0), int64(0)})
		}
		both(fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation,attndims FROM pg_attribute WHERE attrelid=3603 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attnotnull", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "INT4", "BOOL", "OID", "INT2"}, attributes))
		both(fixture.Select("SELECT reltype='pg_catalog.pg_ts_config_map'::regtype::oid AS row_type_link FROM pg_class WHERE oid=3603", []string{"row_type_link"}, []string{"BOOL"}, [][]any{{true}}))
		both(fixture.Select("SELECT t.typrelid,t.typname,a.typname AS array_name,t.typnamespace,a.typnamespace AS array_namespace,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=3603", []string{"typrelid", "typname", "array_name", "typnamespace", "array_namespace", "element_link", "array_link"}, []string{"OID", "NAME", "NAME", "OID", "OID", "BOOL", "BOOL"}, [][]any{{int64(3603), "pg_ts_config_map", "_pg_ts_config_map", int64(11), int64(11), true, true}}))
	})
}

func pgTSConfigMapReferences(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	want := [][]any{}
	aliases := []string{"asciiword", "word", "numword", "email", "url", "host", "sfloat", "version", "hword_numpart", "hword_part", "hword_asciipart", "numhword", "asciihword", "hword", "url_path", "file", "float", "int", "uint"}
	for i, row := range pgTSConfigMapStock() {
		want = append(want, []any{int64(3748), "simple", int64(3722), "default", row[1], aliases[i], int64(1), int64(3765), "simple"})
	}
	q := "SELECT c.oid,c.cfgname,p.oid AS parser,p.prsname,t.tokid,t.alias,m.mapseqno,d.oid AS dictionary,d.dictname FROM pg_ts_config_map m JOIN pg_ts_config c ON c.oid=m.mapcfg JOIN pg_ts_parser p ON p.oid=c.cfgparser JOIN LATERAL ts_token_type(c.cfgparser) t ON t.tokid=m.maptokentype JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE c.oid=3748 ORDER BY m.maptokentype,m.mapseqno"
	pgTSConfigBoth(t, p, b, e, fixture.Select(q, []string{"oid", "cfgname", "parser", "prsname", "tokid", "alias", "mapseqno", "dictionary", "dictname"}, []string{"OID", "NAME", "OID", "NAME", "INT4", "TEXT", "INT4", "OID", "NAME"}, want))
}

func TestReferencePGTSConfigMapReferences(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgTSConfigMapReferences(t, p, b, b.DB)
	})
}
