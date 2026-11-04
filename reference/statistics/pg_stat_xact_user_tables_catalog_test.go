package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const xactUserDefinition = ` SELECT relid,
    schemaname,
    relname,
    seq_scan,
    seq_tup_read,
    idx_scan,
    idx_tup_fetch,
    n_tup_ins,
    n_tup_upd,
    n_tup_del,
    n_tup_hot_upd,
    n_tup_newpage_upd
   FROM pg_stat_xact_all_tables
  WHERE ((schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])) AND (schemaname !~ '^pg_toast'::text));`

func TestReferencePGStatXactUserTablesIdentityAndMetadata(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactUserCreate(t, p, b, c, "xut_identity")
		r := xactUserGet(t, p, c, "xut_identity")
		xactTableCounts(t, r, 0, 0, 0)
		if r.ID <= 0 || r.Schema != "public" || r.Name != "xut_identity" || r.Seq != 0 || r.Read != 0 || r.Index.Valid || r.Fetch.Valid || r.HOT != 0 || r.Newpage != 0 {
			t.Fatal("fresh USER row", r)
		}
		var id, namespace, rowType, array, element, owner int64
		var kind string
		err := c.QueryRowContext(p.Ctx, "SELECT c.oid,c.relnamespace,c.relkind,c.reltype,t.typarray,a.typelem,t.typrelid FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_xact_user_tables'::regclass").Scan(&id, &namespace, &kind, &rowType, &array, &element, &owner)
		if err != nil || id <= 0 || namespace != 11 || kind != "v" || rowType <= 0 || array <= 0 || element != rowType || owner != id {
			t.Fatal("USER row/array relationship", id, namespace, kind, rowType, array, element, owner, err)
		}
		if !b.PG && (id != 12170 || rowType != 12172 || array != 12171) {
			t.Fatal("native USER identity", id, rowType, array)
		}
		var regType int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.pg_stat_xact_user_tables'::regtype::oid").Scan(&regType); err != nil || regType != rowType {
			t.Fatal("regtype identity", regType, err)
		}
		rows, err := c.QueryContext(p.Ctx, "SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_xact_user_tables'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var number, typ, mod, collation int64
			var name string
			var notnull bool
			if err := rows.Scan(&number, &name, &typ, &mod, &notnull, &collation); err != nil {
				t.Fatal(err)
			}
			want, coll := int64(20), int64(0)
			if n == 0 {
				want = 26
			} else if n < 3 {
				want = 19
				coll = 950
			}
			if n >= 12 || number != int64(n+1) || name != xactTableNames[n] || typ != want || mod != -1 || notnull || collation != coll {
				t.Fatal("USER attribute", n, number, name, typ, mod, notnull, collation)
			}
			n++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if n != 12 {
			t.Fatal("attribute count", n)
		}
		var definition, information string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('pg_catalog.pg_stat_xact_user_tables'::regclass)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT view_definition FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='pg_stat_xact_user_tables'").Scan(&information); err != nil || information != definition {
			t.Fatal("owning display", information, definition, err)
		}
		if definition != xactUserDefinition {
			t.Fatal("exact released USER display", definition, xactUserDefinition)
		}
		for _, term := range []string{"FROM pg_stat_xact_all_tables", "schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])", "schemaname !~ '^pg_toast'::text"} {
			if !strings.Contains(definition, term) {
				t.Fatal("released USER definition", term, definition)
			}
		}
		for _, pretty := range []bool{false, true} {
			var rendered string
			want := xactUserDefinition
			if pretty {
				want = strings.Replace(want, "  WHERE ((schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])) AND (schemaname !~ '^pg_toast'::text));", "  WHERE (schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])) AND schemaname !~ '^pg_toast'::text;", 1)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('pg_catalog.pg_stat_xact_user_tables'::regclass,$1::boolean)", pretty).Scan(&rendered); err != nil || rendered != want {
				t.Fatal("pretty USER display", rendered, want, err)
			}
		}
		fixture.StatisticsExec(t, p, b, c, "SET quote_all_identifiers=on", 0)
		var quoted string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('pg_catalog.pg_stat_xact_user_tables'::regclass)").Scan(&quoted); err != nil || !strings.Contains(quoted, `FROM "pg_stat_xact_all_tables"`) || !strings.Contains(quoted, `"schemaname"`) {
			t.Fatal("quoted dependencies", quoted, err)
		}
		fixture.StatisticsExec(t, p, b, c, "SET quote_all_identifiers=off", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE public.pg_stat_xact_all_tables(n integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "SET search_path=public,pg_catalog", 0)
		var shadowed string
		wantShadow := strings.Replace(xactUserDefinition, "FROM pg_stat_xact_all_tables", "FROM pg_catalog.pg_stat_xact_all_tables", 1)
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_catalog.pg_get_viewdef('pg_catalog.pg_stat_xact_user_tables'::regclass)").Scan(&shadowed); err != nil || shadowed != wantShadow {
			t.Fatal("search-path dependency rendering", shadowed, wantShadow, err)
		}
		fixture.StatisticsExec(t, p, b, c, "DROP TABLE public.pg_stat_xact_all_tables", 0)
		fixture.StatisticsExec(t, p, b, c, "RESET search_path", 0)
		for _, qualified := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					q := "SELECT * FROM pg_stat_xact_user_tables WHERE relname=$1"
					if qualified {
						q = xactUserSelect
					}
					if empty {
						q += " AND FALSE"
					}
					var result *sql.Rows
					var stmt *sql.Stmt
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err == nil {
							result, err = stmt.QueryContext(p.Ctx, "xut_identity")
						}
					} else {
						result, err = c.QueryContext(p.Ctx, q, "xut_identity")
					}
					if err != nil {
						t.Fatal(err)
					}
					columns, err := result.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					xactTableMetadata(t, columns, b.PG)
					count := 0
					for result.Next() {
						raw := make([]any, 12)
						dest := make([]any, 12)
						for i := range raw {
							dest[i] = &raw[i]
						}
						if err := result.Scan(dest...); err != nil {
							t.Fatal(err)
						}
						for i, v := range raw {
							switch {
							case i == 5 || i == 6:
								if v != nil {
									t.Fatal("empty index SUM", i, v)
								}
							case i < 3:
								if b.PG {
									if _, ok := v.([]byte); !ok {
										t.Fatalf("pq identity %d=%T", i, v)
									}
								} else if i == 0 {
									if _, ok := v.(int64); !ok {
										t.Fatalf("native OID=%T", v)
									}
								} else {
									if _, ok := v.(string); !ok {
										t.Fatalf("native NAME=%T", v)
									}
								}
							default:
								if v != int64(0) {
									t.Fatal("typed zero", i, v)
								}
							}
						}
						count++
					}
					if err := result.Err(); err != nil {
						t.Fatal(err)
					}
					result.Close()
					xactTableMetadata(t, columns, b.PG)
					if stmt != nil {
						stmt.Close()
					}
					want := 1
					if empty {
						want = 0
					}
					if count != want {
						t.Fatal("USER cardinality", count, want)
					}
				}
			}
		}
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("SELECT missing FROM pg_catalog.pg_stat_xact_user_tables", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing" does not exist`, Position: "8"})
			step.Prepared = prepared
			step.SelectRows = true
			step.ErrorStage = "execute"
			if prepared {
				step.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
	})
}


