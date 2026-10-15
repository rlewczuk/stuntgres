package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectAddressStale(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		a := objectAddressCore(t, p, b, c)
		objectAddressRun(t, p, b, c, fixture.Exec("ALTER TABLE addr_probe DROP COLUMN b", 0))
		dropped := func(t *testing.T, x objectAddressValue) {
			objectAddressNumeric(t, p, b, c, x, "table column", "public", "addr_probe", `public.addr_probe."........pg.dropped.2........"`, "{public,addr_probe,........pg.dropped.2........}", "{}", "column ........pg.dropped.2........ of table addr_probe")
		}
		t.Run("dropped_attribute", func(t *testing.T) { dropped(t, a) })
		objectAddressRun(t, p, b, c, fixture.Exec("ALTER TABLE addr_probe ADD COLUMN c TEXT", 0))
		fresh := objectAddressResolve(t, p, c, "table column", []string{"public", "addr_probe", "c"}, nil)
		if fresh.object != a.object || fresh.sub != 3 {
			t.Fatal("attribute high water", a, fresh)
		}
		for _, sub := range []int64{3, 4, 32767, 32768, 65537, 65538, -65535, 2147483647, -2147483648} {
			t.Run(fmt.Sprintf("sub_%d", sub), func(t *testing.T) {
				x := a
				x.sub = sub
				if sub == 3 {
					objectAddressNumeric(t, p, b, c, x, "table column", "public", "addr_probe", "public.addr_probe.c", "{public,addr_probe,c}", "{}", "column c of table addr_probe")
					return
				}
				if sub == 65537 || sub == -65535 {
					objectAddressNumeric(t, p, b, c, x, "table column", "public", "addr_probe", "public.addr_probe.a", "{public,addr_probe,a}", "{}", "column a of table addr_probe")
					return
				}
				if sub == 65538 {
					dropped(t, x)
					return
				}
				if sub == 2147483647 {
					objectAddressNumeric(t, p, b, c, x, "table column", "public", "addr_probe", "public.addr_probe.ctid", "{public,addr_probe,ctid}", "{}", "column ctid of table addr_probe")
					return
				}
				objectAddressNumeric(t, p, b, c, x, "table column", nil, nil, nil, nil, nil, nil)
			})
		}
		objectAddressRun(t, p, b, c, fixture.Exec("DROP TABLE addr_probe", 0))
		t.Run("dropped_relation", func(t *testing.T) {
			objectAddressNumeric(t, p, b, c, objectAddressValue{1259, a.object, 0}, "relation", nil, nil, nil, nil, nil, nil)
		})
		objectAddressRun(t, p, b, c, fixture.Exec("CREATE TABLE addr_probe(a INT,b TEXT)", 0))
		replacement := objectAddressResolve(t, p, c, "table column", []string{"public", "addr_probe", "b"}, nil)
		if replacement.object == a.object {
			t.Fatal("recreated object reused stale address", a, replacement)
		}
		for _, tc := range []struct {
			class int64
			kind  string
		}{
			{2615, "schema"}, {1259, "relation"}, {1247, "type"}, {1255, "routine"}, {3456, "collation"}, {2607, "conversion"}, {3079, "extension"}, {3764, "text search template"}, {3600, "text search dictionary"}, {1262, "database"}, {1260, "role"}, {2606, "constraint"}, {2604, "default value"}, {2618, "rule"}, {3256, "policy"}, {1213, "tablespace"}, {2605, "cast"}, {2616, "operator class"}, {2753, "operator family"}, {2617, "operator"},
			// Recognized PostgreSQL classes with no native address provider must
			// retain missing semantics, not fabricate instances or unknown classes.
			{2612, "language"}, {2601, "access method"}, {2328, "foreign-data wrapper"}, {1417, "server"}, {1418, "user mapping"}, {2620, "trigger"}, {3466, "event trigger"}, {6104, "publication"}, {6100, "subscription"}, {3381, "statistics object"}, {2613, "large object"}, {3601, "text search parser"}, {3602, "text search configuration"}, {826, "default acl"}, {1261, "role membership"},
		} {
			t.Run(fmt.Sprint("missing_", tc.class), func(t *testing.T) {
				objectAddressNumeric(t, p, b, c, objectAddressValue{tc.class, 4294967295, 0}, tc.kind, nil, nil, nil, nil, nil, nil)
			})
		}
		var publicID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&publicID); err != nil {
			t.Fatal(err)
		}
		for _, sub := range []int64{-1, 1, 65537, 2147483647} {
			t.Run(fmt.Sprint("schema_sub_", sub), func(t *testing.T) {
				objectAddressNumeric(t, p, b, c, objectAddressValue{2615, publicID, sub}, "schema", nil, "public", "public", "{public}", "{}", "schema public")
			})
		}
		for _, fn := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_describe_object"} {
			t.Run(fn+"_zero", func(t *testing.T) {
				q := "SELECT " + fn + "(0,0,0) AS v"
				if fn == "pg_describe_object" {
					objectAddressRun(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
					return
				}
				objectAddressDiagnostics(t, p, b, c, []objectAddressDiagnosticCase{{"zero", q, fixture.Diagnostic{Code: "XX000", Message: "unsupported object class: 0"}, true}})
			})
		}
	})
}

func TestReferenceObjectAddressQuotingVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"addr_reader", "addr_schema_reader"})
	fixture.SchemaReferenceCleanup(t, "Odd.Schema")
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{`CREATE SCHEMA "Odd.Schema"`, `CREATE TABLE "Odd.Schema"."Odd Table"("a.b""c" INT,"select" TEXT,"é,\\" TEXT)`, `CREATE ROLE addr_reader`, `CREATE ROLE addr_schema_reader`} {
			objectAddressRun(t, p, b, c, fixture.Exec(q, 0))
		}
		a := objectAddressWitness(t, p, b, c, 1259, "table column", []string{"Odd.Schema", "Odd Table", `a.b"c`}, nil, `"Odd.Schema"`, `"Odd Table"`, `"Odd.Schema"."Odd Table"."a.b""c"`, `column a.b"c of table "Odd.Schema"."Odd Table"`, `SELECT '"Odd.Schema"."Odd Table"'::regclass::oid`)
		objectAddressRun(t, p, b, c, fixture.Exec(`SET search_path TO "Odd.Schema",public`, 0))
		objectAddressNumeric(t, p, b, c, a, "table column", `"Odd.Schema"`, `"Odd Table"`, `"Odd.Schema"."Odd Table"."a.b""c"`, objectAddressArrayText(t, []string{"Odd.Schema", "Odd Table", `a.b"c`}), "{}", `column a.b"c of table "Odd Table"`)
		for _, col := range []string{"select", `é,\\`} {
			objectAddressWitness(t, p, b, c, 1259, "table column", []string{"Odd.Schema", "Odd Table", col}, nil, `"Odd.Schema"`, `"Odd Table"`, `"Odd.Schema"."Odd Table".`+objectAddressQuote(col), `column `+col+` of table "Odd Table"`, "")
		}
		objectAddressRun(t, p, b, c, fixture.Exec("SET ROLE addr_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		// Numeric inquiries do not invent ownership or table SELECT checks.
		objectAddressNumeric(t, p, b, c, a, "table column", `"Odd.Schema"`, `"Odd Table"`, `"Odd.Schema"."Odd Table"."a.b""c"`, objectAddressArrayText(t, []string{"Odd.Schema", "Odd Table", `a.b"c`}), "{}", `column a.b"c of table "Odd.Schema"."Odd Table"`)
		objectAddressDiagnostics(t, p, b, c, []objectAddressDiagnosticCase{
			{"no_schema_usage", `SELECT pg_get_object_address('table column',ARRAY['Odd.Schema','Odd Table','a.b"c'],ARRAY[]::text[])`, fixture.Diagnostic{Code: "42501", Message: `permission denied for schema Odd.Schema`}, true},
			{"select_denied", `SELECT * FROM "Odd.Schema"."Odd Table"`, fixture.Diagnostic{Code: "42501", Message: `permission denied for schema Odd.Schema`, Position: "15"}, false},
		})
		objectAddressRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
		// Schema ownership is the existing shared positive-USAGE setup; do not
		// introduce schema GRANT as an adjacent implementation prerequisite.
		objectAddressRun(t, p, b, c, fixture.Exec(`ALTER SCHEMA "Odd.Schema" OWNER TO addr_schema_reader`, 0))
		objectAddressRun(t, p, b, c, fixture.Exec("SET ROLE addr_schema_reader", 0))
		resolved := objectAddressResolve(t, p, c, "table column", []string{"Odd.Schema", "Odd Table", `a.b"c`}, nil)
		if resolved != a {
			t.Fatal("qualified nonowner symbolic address", resolved, a)
		}
		objectAddressDiagnostics(t, p, b, c, []objectAddressDiagnosticCase{{"no_table_select", `SELECT * FROM "Odd.Schema"."Odd Table"`, fixture.Diagnostic{Code: "42501", Message: `permission denied for table Odd Table`}, true}})
		objectAddressRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
	})
}
