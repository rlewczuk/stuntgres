package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const xactSysDefinition = ` SELECT relid,
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
  WHERE ((schemaname = ANY (ARRAY['pg_catalog'::name, 'information_schema'::name])) OR (schemaname ~ '^pg_toast'::text));`

func TestReferencePGStatXactSysTablesIdentityAndMetadata(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactSysCreate(t, p, b, c, "xst_identity")
		r := xactSysGet(t, p, c, "xst_identity")
		xactTableCounts(t, r, 0, 0, 0)
		if r.ID <= 0 || r.Schema != "information_schema" || r.Name != "xst_identity" || r.Seq != 0 || r.Read != 0 || r.Index.Valid || r.Fetch.Valid || r.HOT != 0 || r.Newpage != 0 {
			t.Fatal("fresh SYS row", r)
		}
		var id, namespace, rowType, array, element, owner int64
		var kind string
		err := c.QueryRowContext(p.Ctx, "SELECT c.oid,c.relnamespace,c.relkind,c.reltype,t.typarray,a.typelem,t.typrelid FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_xact_sys_tables'::regclass").Scan(&id, &namespace, &kind, &rowType, &array, &element, &owner)
		if err != nil || id <= 0 || namespace != 11 || kind != "v" || rowType <= 0 || array <= 0 || element != rowType || owner != id {
			t.Fatal("SYS row/array relationship", id, namespace, kind, rowType, array, element, owner, err)
		}
		if !b.PG && (id != 12161 || rowType != 12163 || array != 12162) {
			t.Fatal("native SYS identity", id, rowType, array)
		}
		var regType int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.pg_stat_xact_sys_tables'::regtype::oid").Scan(&regType); err != nil || regType != rowType {
			t.Fatal("regtype identity", regType, err)
		}
		rows, err := c.QueryContext(p.Ctx, "SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_xact_sys_tables'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum")
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
				t.Fatal("SYS attribute", n, number, name, typ, mod, notnull, collation)
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
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('pg_catalog.pg_stat_xact_sys_tables'::regclass)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT view_definition FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='pg_stat_xact_sys_tables'").Scan(&information); err != nil || information != definition {
			t.Fatal("owning display", information, definition, err)
		}
		if definition != xactSysDefinition {
			t.Fatal("exact released SYS display", definition, xactSysDefinition)
		}
		for _, term := range []string{"FROM pg_stat_xact_all_tables", "schemaname = ANY (ARRAY['pg_catalog'::name, 'information_schema'::name])", "schemaname ~ '^pg_toast'::text"} {
			if !strings.Contains(definition, term) {
				t.Fatal("released SYS definition", term, definition)
			}
		}
		for _, pretty := range []bool{false, true} {
			var rendered string
			want := xactSysDefinition
			if pretty {
				want = strings.Replace(want, "  WHERE ((schemaname = ANY (ARRAY['pg_catalog'::name, 'information_schema'::name])) OR (schemaname ~ '^pg_toast'::text));", "  WHERE (schemaname = ANY (ARRAY['pg_catalog'::name, 'information_schema'::name])) OR schemaname ~ '^pg_toast'::text;", 1)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('pg_catalog.pg_stat_xact_sys_tables'::regclass,$1::boolean)", pretty).Scan(&rendered); err != nil || rendered != want {
				t.Fatal("pretty SYS display", rendered, want, err)
			}
		}
		fixture.StatisticsExec(t, p, b, c, "SET quote_all_identifiers=on", 0)
		var quoted string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('pg_catalog.pg_stat_xact_sys_tables'::regclass)").Scan(&quoted); err != nil || !strings.Contains(quoted, `FROM "pg_stat_xact_all_tables"`) || !strings.Contains(quoted, `"schemaname"`) {
			t.Fatal("quoted dependencies", quoted, err)
		}
		fixture.StatisticsExec(t, p, b, c, "SET quote_all_identifiers=off", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE public.pg_stat_xact_all_tables(n integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "SET search_path=public,pg_catalog", 0)
		var shadowed string
		wantShadow := strings.Replace(xactSysDefinition, "FROM pg_stat_xact_all_tables", "FROM pg_catalog.pg_stat_xact_all_tables", 1)
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_catalog.pg_get_viewdef('pg_catalog.pg_stat_xact_sys_tables'::regclass)").Scan(&shadowed); err != nil || shadowed != wantShadow {
			t.Fatal("search-path dependency rendering", shadowed, wantShadow, err)
		}
		fixture.StatisticsExec(t, p, b, c, "DROP TABLE public.pg_stat_xact_all_tables", 0)
		fixture.StatisticsExec(t, p, b, c, "RESET search_path", 0)
		for _, qualified := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					q := "SELECT * FROM pg_stat_xact_sys_tables WHERE relname=$1"
					if qualified {
						q = xactSysSelect
					}
					if empty {
						q += " AND FALSE"
					}
					var result *sql.Rows
					var stmt *sql.Stmt
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err == nil {
							result, err = stmt.QueryContext(p.Ctx, "xst_identity")
						}
					} else {
						result, err = c.QueryContext(p.Ctx, q, "xst_identity")
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
						t.Fatal("SYS cardinality", count, want)
					}
				}
			}
		}
	})
}

func TestReferencePGStatXactSysTablesMembership(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactSysCreate(t, p, b, c, "xst_members")
		tableStatCreate(t, p, b, c, "xst_public")
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, q := range []string{"DROP VIEW IF EXISTS information_schema.xst_view", "DROP SEQUENCE IF EXISTS information_schema.xst_sequence", "DROP SERVER IF EXISTS xst_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS xst_fdw"} {
				if _, err := b.DB.ExecContext(ctx, q); err != nil {
					t.Error(err)
				}
			}
		})
		for _, q := range []string{"CREATE VIEW information_schema.xst_view AS SELECT n FROM information_schema.xst_members", "CREATE SEQUENCE information_schema.xst_sequence", "CREATE INDEX xst_index ON information_schema.xst_members(n)", "CREATE FOREIGN DATA WRAPPER xst_fdw", "CREATE SERVER xst_server FOREIGN DATA WRAPPER xst_fdw", "CREATE FOREIGN TABLE information_schema.xst_foreign(n integer) SERVER xst_server"} {
			fixture.StatisticsExec(t, p, b, c, q, 0)
		}
		for _, tc := range []struct {
			name  string
			count int64
		}{{"xst_members", 1}, {"xst_public", 0}, {"pg_class", 1}, {"pg_attribute", 1}, {"xst_view", 0}, {"xst_sequence", 0}, {"xst_index", 0}, {"xst_foreign", 0}, {"pg_stat_xact_sys_tables", 0}} {
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT count(*) FROM pg_stat_xact_sys_tables WHERE relname=$1", []string{"count"}, []string{"INT8"}, [][]any{{tc.count}}, tc.name)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
		}
		for _, view := range []string{"pg_stat_xact_all_tables", "pg_stat_all_tables", "pg_stat_user_tables", "pg_stat_sys_tables"} {
			want := int64(1)
			if view == "pg_stat_sys_tables" {
				want = 0
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM "+view+" WHERE relname='xst_public'", []string{"count"}, []string{"INT8"}, [][]any{{want}}))
		}
		query := func(view, filter string) []int64 {
			rows, err := c.QueryContext(p.Ctx, "SELECT relid FROM "+view+" WHERE relname IN ('xst_members','xst_public','pg_class','pg_attribute','xst_view','xst_sequence','xst_index','xst_foreign')"+filter+" ORDER BY relid")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var ids []int64
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return ids
		}
		all := query("pg_stat_xact_all_tables", " AND (schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast')")
		sys := query("pg_stat_xact_sys_tables", "")
		if len(sys) != 3 || !reflect.DeepEqual(sys, all) {
			t.Fatal("exact represented SYS subset", sys, all)
		}
		var toast int64
		if err := c.QueryRowContext(p.Ctx, "SELECT reltoastrelid FROM pg_class WHERE oid='information_schema.xst_members'::regclass").Scan(&toast); err != nil {
			t.Fatal(err)
		}
		if toast > 0 {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_stat_xact_sys_tables WHERE relid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, toast))
		}
	})
}
