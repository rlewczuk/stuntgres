package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSysTablesIdentityAndMetadata(t *testing.T) {
	tableStatIdentityAndMetadata(t, tableStatFamily{system: true})
}
func TestReferencePGStatSysTablesSequentialAccess(t *testing.T) {
	tableStatSequentialAccess(t, tableStatFamily{system: true})
}
func TestReferencePGStatSysTablesIndexAggregation(t *testing.T) {
	tableStatIndexAggregation(t, tableStatFamily{system: true})
}
func TestReferencePGStatSysTablesEventsAndRollback(t *testing.T) {
	tableStatEventsAndRollback(t, tableStatFamily{system: true})
}
func TestReferencePGStatSysTablesMaintenance(t *testing.T) {
	tableStatMaintenance(t, tableStatFamily{system: true})
}
func TestReferencePGStatSysTablesReadOnly(t *testing.T) {
	tableStatReadOnly(t, tableStatFamily{system: true})
}
func TestReferencePGStatSysTablesSnapshotsAndLifetime(t *testing.T) {
	tableStatSnapshotsAndLifetime(t, tableStatFamily{system: true})
}
func TestReferencePGStatSysTablesNativeControls(t *testing.T) {
	tableStatNativeControls(t, tableStatFamily{system: true})
}

func tableStatAttributes(t *testing.T, p *fixture.Pair, c *sql.Conn, view string) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation FROM pg_attribute WHERE attrelid=('pg_catalog.' || $1)::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", view)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types := map[string]int64{"OID": 26, "NAME": 19, "INT8": 20, "TIMESTAMPTZ": 1184, "FLOAT8": 701}
	i := 0
	for rows.Next() {
		var ordinal, kind, typmod, collation int64
		var name string
		var notnull bool
		if err := rows.Scan(&ordinal, &name, &kind, &typmod, &notnull, &collation); err != nil {
			t.Fatal(err)
		}
		if i >= 30 {
			t.Fatal("extra attribute", name)
		}
		wantCollation := int64(0)
		if i == 1 || i == 2 {
			wantCollation = 950
		}
		if ordinal != int64(i+1) || name != tableStatNames[i] || kind != types[tableStatTypes[i]] || typmod != -1 || notnull || collation != wantCollation {
			t.Fatal("exact catalog column", i, ordinal, name, kind, typmod, notnull, collation)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != 30 {
		t.Fatal("missing attributes", i)
	}
}

func TestReferencePGStatSysTablesMembership(t *testing.T) {
	f := tableStatFamily{system: true}
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		f.create(t, p, b, c, "sst_members")
		tableStatCreate(t, p, b, c, "public.sst_public")
		type identity struct {
			ID           int64
			Schema, Name string
		}
		bag := func(view string) map[identity]int {
			t.Helper()
			rows, err := c.QueryContext(p.Ctx, "SELECT relid,schemaname,relname FROM pg_catalog."+view)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			out := make(map[identity]int)
			for rows.Next() {
				var id identity
				if err := rows.Scan(&id.ID, &id.Schema, &id.Name); err != nil {
					t.Fatal(err)
				}
				out[id]++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return out
		}
		all, sys, user := bag("pg_stat_all_tables"), bag("pg_stat_sys_tables"), bag("pg_stat_user_tables")
		wantSys, wantUser := make(map[identity]int), make(map[identity]int)
		for id, n := range all {
			if id.Schema == "pg_catalog" || id.Schema == "information_schema" || strings.HasPrefix(id.Schema, "pg_toast") {
				wantSys[id] = n
			} else {
				wantUser[id] = n
			}
		}
		if !reflect.DeepEqual(sys, wantSys) || !reflect.DeepEqual(user, wantUser) {
			t.Fatal("SYS/USER must partition ALL identity bags", sys, wantSys, user, wantUser)
		}
		for _, name := range []string{"sst_members", "pg_class", "pg_attribute"} {
			r := f.get(t, p, c, name)
			if name == "sst_members" && r.Schema != "information_schema" || name == "pg_class" && r.ID != 1259 || name == "pg_attribute" && r.ID != 1249 {
				t.Fatal("system positive", name, r)
			}
		}
		if _, err := f.read(p.Ctx, c, "sst_public"); err != sql.ErrNoRows {
			t.Fatal("public table entered SYS", err)
		}
		for _, tc := range []struct {
			view, name string
			want       int64
		}{{"pg_stat_all_tables", "sst_members", 1}, {"pg_stat_user_tables", "sst_members", 0}, {"pg_stat_all_tables", "sst_public", 1}, {"pg_stat_user_tables", "sst_public", 1}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM "+tc.view+" WHERE relname='"+tc.name+"'", []string{"count"}, []string{"INT8"}, [][]any{{tc.want}}))
		}
		var toast int64
		if err := c.QueryRowContext(p.Ctx, "SELECT reltoastrelid FROM pg_class WHERE oid='public.sst_public'::regclass").Scan(&toast); err != nil {
			t.Fatal(err)
		}
		if toast > 0 {
			var n int
			if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_sys_tables WHERE relid=$1", toast).Scan(&n); err != nil || n != 1 {
				t.Fatal("actual TOAST is system", toast, n, err)
			}
		}
	})
}

// The released SYS definition selects the same thirty columns from ALL. Pretty
// display removes only the redundant outer WHERE parentheses; quote/path
// presentation must still render every explicit identifier dependency.
func TestReferencePGStatSysTablesViewDefinition(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE public.pg_stat_all_tables(n integer)", 0)
		for _, shadow := range []bool{false, true} {
			path := "pg_catalog,public"
			if shadow {
				path = "public,pg_catalog"
			}
			fixture.StatisticsExec(t, p, b, c, "SET search_path="+path, 0)
			for _, quote := range []bool{false, true} {
				setting := "off"
				if quote {
					setting = "on"
				}
				fixture.StatisticsExec(t, p, b, c, "SET quote_all_identifiers="+setting, 0)
				identifier := func(name string) string {
					if quote {
						return `"` + name + `"`
					}
					return name
				}
				columns := make([]string, len(tableStatNames))
				for i, name := range tableStatNames {
					columns[i] = identifier(name)
				}
				base := identifier("pg_stat_all_tables")
				if shadow {
					base = identifier("pg_catalog") + "." + base
				}
				projection := " SELECT " + strings.Join(columns, ",\n    ") + "\n   FROM " + base + "\n"
				membership := identifier("schemaname") + " = ANY (ARRAY['pg_catalog'::" + identifier("name") + ", 'information_schema'::" + identifier("name") + "])"
				toast := identifier("schemaname") + " ~ '^pg_toast'::" + identifier("text")
				for _, pretty := range []bool{false, true} {
					want := projection + "  WHERE ((" + membership + ") OR (" + toast + "));"
					if pretty {
						want = projection + "  WHERE (" + membership + ") OR " + toast + ";"
					}
					for _, prepared := range []bool{false, true} {
						step := fixture.Select("SELECT pg_catalog.pg_get_viewdef('pg_catalog.pg_stat_sys_tables'::regclass,$1::boolean) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{want}}, pretty)
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					}
				}
			}
		}
	})
}
