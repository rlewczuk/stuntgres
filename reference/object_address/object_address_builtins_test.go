package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectAddressAuthorityBuiltins(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"addr_role"})
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		objectAddressRun(t, p, b, c, fixture.Exec("CREATE ROLE addr_role", 0))
		for _, tc := range []struct {
			class             int64
			kind, name, idSQL string
		}{
			{2615, "schema", "public", "SELECT oid FROM pg_namespace WHERE nspname='public'"}, {2615, "schema", "pg_catalog", "SELECT 11"},
			{1260, "role", "addr_role", "SELECT oid FROM pg_roles WHERE rolname='addr_role'"},
			{1260, "role", "pg_read_all_data", "SELECT oid FROM pg_roles WHERE rolname='pg_read_all_data'"},
			{1213, "tablespace", "pg_default", "SELECT 1663"}, {1213, "tablespace", "pg_global", "SELECT 1664"},
		} {
			objectAddressWitness(t, p, b, c, tc.class, tc.kind, []string{tc.name}, nil, nil, tc.name, tc.name, tc.kind+" "+tc.name, tc.idSQL)
		}
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		databaseIdentity := objectAddressIdent(database)
		objectAddressWitness(t, p, b, c, 1262, "database", []string{database}, nil, nil, databaseIdentity, databaseIdentity, "database "+databaseIdentity, "SELECT oid FROM pg_database WHERE datname=current_database()")
		for _, q := range []string{"CREATE COLLATION addr_collation FROM pg_catalog.\"C\"", "CREATE CONVERSION addr_conversion FOR 'UTF8' TO 'LATIN1' FROM utf8_to_iso8859_1", "CREATE EXTENSION unaccent"} {
			objectAddressRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, tc := range []struct {
			class                    int64
			kind, name, table, field string
		}{
			{3456, "collation", "addr_collation", "pg_collation", "collname"},
			{2607, "conversion", "addr_conversion", "pg_conversion", "conname"},
			{3764, "text search template", "unaccent", "pg_ts_template", "tmplname"},
			{3600, "text search dictionary", "unaccent", "pg_ts_dict", "dictname"},
		} {
			objectAddressWitness(t, p, b, c, tc.class, tc.kind, []string{"public", tc.name}, nil, "public", tc.name, "public."+tc.name, tc.kind+" "+tc.name, "SELECT oid FROM "+tc.table+" WHERE "+tc.field+"='"+tc.name+"'")
		}
		objectAddressWitness(t, p, b, c, 3079, "extension", []string{"unaccent"}, nil, nil, "unaccent", "unaccent", "extension unaccent", "SELECT oid FROM pg_extension WHERE extname='unaccent'")
		for _, tc := range []struct {
			name string
			args []string
		}{
			{"unaccent", []string{"pg_catalog.text"}},
			{"unaccent", []string{"pg_catalog.regdictionary", "pg_catalog.text"}},
			{"unaccent_init", []string{"pg_catalog.internal"}},
			{"unaccent_lexize", []string{"pg_catalog.internal", "pg_catalog.internal", "pg_catalog.internal", "pg_catalog.internal"}},
		} {
			human := make([]string, len(tc.args))
			for i, s := range tc.args {
				human[i] = strings.TrimPrefix(s, "pg_catalog.")
			}
			objectAddressWitness(t, p, b, c, 1255, "function", []string{"public", tc.name}, tc.args, "public", nil, "public."+tc.name+"("+strings.Join(tc.args, ",")+")", "function "+tc.name+"("+strings.Join(human, ",")+")", "SELECT '"+tc.name+"("+strings.Join(tc.args, ",")+")'::regprocedure::oid")
		}
		for _, tc := range []struct {
			id   int64
			name string
		}{{100, "default"}, {950, "C"}, {951, "POSIX"}, {962, "ucs_basic"}, {963, "unicode"}, {811, "pg_c_utf8"}, {6411, "pg_unicode_fast"}} {
			name := objectAddressIdent(tc.name)
			objectAddressWitness(t, p, b, c, 3456, "collation", []string{"pg_catalog", tc.name}, nil, "pg_catalog", name, "pg_catalog."+name, "collation "+name, fmt.Sprintf("SELECT %d", tc.id))
		}
		// Builtin conversion routines are also fixed identities in the exhaustive
		// routine proof. Conversion objects retain their separate class authority.
		var conversions []objectAddressInventoryRow
		for _, line := range strings.Split(objectAddressConversions, "\n") {
			f := strings.Fields(line)
			id, err := strconv.ParseInt(f[0], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			conversions = append(conversions, objectAddressInventoryRow{2607, id, "conversion", "pg_catalog", f[1], "pg_catalog." + f[1], "conversion " + f[1], []string{"pg_catalog", f[1]}, nil})
		}
		objectAddressInventory(t, p, b, c, conversions, true)
	})
}

func TestReferenceObjectAddressAuthorityCasts(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		_, types, _ := objectAddressTypeFacts(t)
		facts := objectAddressCastFacts()
		if len(facts) != 70 {
			t.Fatal("cast inventory", len(facts))
		}
		for _, r := range facts {
			t.Run(fmt.Sprint(r[0], "_", r[1]), func(t *testing.T) {
				source, target := types[fmt.Sprint(r[0])], types[fmt.Sprint(r[1])]
				objectAddressWitness(t, p, b, c, 2605, "cast", []string{source}, []string{target}, nil, nil, "("+source+" AS "+target+")", "cast from "+strings.TrimPrefix(source, "pg_catalog.")+" to "+strings.TrimPrefix(target, "pg_catalog."), fmt.Sprintf("SELECT oid FROM pg_cast WHERE castsource=%d AND casttarget=%d", r[0], r[1]))
			})
		}
	})
}

func TestReferenceObjectAddressAuthorityIndexClasses(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		facts := objectAddressClassFacts()
		if len(facts) != 58 {
			t.Fatal("opclass inventory", len(facts))
		}
		seen := map[int64]bool{}
		families := map[int64]string{
			424: "bool_ops", 428: "bytea_ops", 429: "char_ops", 1994: "text_ops", 1976: "integer_ops", 426: "bpchar_ops", 1989: "oid_ops", 5067: "xid8_ops", 1970: "float_ops", 434: "datetime_ops", 1996: "time_ops", 2000: "timetz_ops", 1982: "interval_ops", 1988: "numeric_ops", 2099: "money_ops", 2968: "uuid_ops", 1974: "network_ops", 4033: "jsonb_ops", 2095: "text_pattern_ops", 2097: "bpchar_pattern_ops", 3522: "enum_ops",
			2222: "bool_ops", 2223: "bytea_ops", 431: "char_ops", 1995: "text_ops", 1977: "integer_ops", 427: "bpchar_ops", 1990: "oid_ops", 5032: "xid8_ops", 2225: "xid_ops", 2226: "cid_ops", 1971: "float_ops", 435: "date_ops", 1997: "time_ops", 2040: "timestamp_ops", 1999: "timestamptz_ops", 2001: "timetz_ops", 1983: "interval_ops", 1998: "numeric_ops", 2969: "uuid_ops", 4034: "jsonb_ops", 2229: "text_pattern_ops", 2231: "bpchar_pattern_ops", 3523: "enum_ops",
		}
		for _, r := range facts {
			method := "btree"
			if r[1].(int64) == 405 {
				method = "hash"
			}
			name := r[2].(string)
			t.Run(method+"_"+name, func(t *testing.T) {
				objectAddressWitness(t, p, b, c, 2616, "operator class", []string{method, "pg_catalog", name}, nil, "pg_catalog", name, "pg_catalog."+name+" USING "+method, "operator class "+name+" for access method "+method, fmt.Sprintf("SELECT oid FROM pg_opclass WHERE opcmethod=%d AND opcname='%s'", r[1], name))
				family := r[5].(int64)
				if seen[family] {
					return
				}
				seen[family] = true
				fn, ok := families[family]
				if !ok {
					t.Fatal("missing independently released family", family)
				}
				a := objectAddressWitness(t, p, b, c, 2753, "operator family", []string{method, "pg_catalog", fn}, nil, "pg_catalog", fn, "pg_catalog."+fn+" USING "+method, "operator family "+fn+" for access method "+method, fmt.Sprintf("SELECT %d", family))
				if a.object != family {
					t.Fatal("fixed family", a, family)
				}
			})
		}
		if len(seen) != len(families) {
			t.Fatal("referenced family coverage", len(seen), len(families))
		}
	})
}
