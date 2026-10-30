package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreComparisonIdentities(t *testing.T, p *fixture.Pair, c *sql.Conn) map[string]int64 {
	t.Helper()
	out := make(map[string]int64)
	for _, part := range []struct {
		table, name, namespace, filter string
		count                          int
	}{
		{"pg_type", "typname", "typnamespace", "typname='hstore'", 1},
		{"pg_proc", "proname", "pronamespace", "proname IN ('hstore_eq','hstore_ne','hstore_lt','hstore_le','hstore_gt','hstore_ge','hstore_cmp','hstore_hash','hstore_hash_extended')", 9},
		{"pg_operator", "oprname", "oprnamespace", "oprname IN ('=','<>','#<#','#<=#','#>#','#>=#') AND oprleft=('" + hstoreComparisonSchema + ".hstore'::regtype)::oid AND oprright=oprleft", 6},
		{"pg_opclass", "opcname", "opcnamespace", "opcname IN ('btree_hstore_ops','hash_hstore_ops') AND opcmethod IN (403,405)", 2},
		{"pg_opfamily", "opfname", "opfnamespace", "opfname IN ('btree_hstore_ops','hash_hstore_ops') AND opfmethod IN (403,405)", 2},
	} {
		q := "SELECT " + part.name + ",oid::bigint FROM " + part.table + " WHERE " + part.namespace + "='" + hstoreComparisonSchema + "'::regnamespace AND " + part.filter + " ORDER BY " + part.name
		rows, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var name string
			var id int64
			if err := rows.Scan(&name, &id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			key := part.table + "/" + name
			if id <= 0 || out[key] != 0 {
				rows.Close()
				t.Fatalf("invalid member identity %s=%d", key, id)
			}
			out[key] = id
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if count != part.count {
			t.Fatalf("%s selected identities=%d want %d", part.table, count, part.count)
		}
	}
	return out
}
