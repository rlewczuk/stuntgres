package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Called only by the native control phase. Every identity comes from actual
// public catalog rows, not an assumed allocation sequence or fixed stride.
func cubeComparisonMemberIDs(t *testing.T, p *fixture.Pair, c *sql.Conn) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, part := range []struct{ table, name, namespace, filter string }{
		{"pg_type", "typname", "typnamespace", "typname='cube'"},
		{"pg_proc", "proname", "pronamespace", "proname IN ('cube_in','cube_out','cube_eq','cube_ne','cube_lt','cube_le','cube_gt','cube_ge','cube_cmp')"},
		{"pg_operator", "oprname", "oprnamespace", "oprname IN ('=','<>','<','<=','>','>=') AND oprleft=('" + cubeComparisonSchema + ".cube'::regtype)::oid AND oprright=oprleft"},
		{"pg_opclass", "opcname", "opcnamespace", "opcname='cube_ops' AND opcmethod=403"},
		{"pg_opfamily", "opfname", "opfnamespace", "opfname='cube_ops' AND opfmethod=403"},
	} {
		q := "SELECT " + part.name + ",oid::bigint FROM " + part.table + " WHERE " + part.namespace + "='" + cubeComparisonSchema + "'::regnamespace AND " + part.filter + " ORDER BY " + part.name
		rows, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
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
				t.Fatal("invalid member identity", key, id)
			}
			out[key] = id
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	if len(out) != 18 {
		t.Fatalf("selected direct member identities=%v", out)
	}
	return out
}

func cubeComparisonCheckRemap(t *testing.T, p *fixture.Pair, c *sql.Conn, source, retained map[string]int64) {
	t.Helper()
	mapped := cubeComparisonMemberIDs(t, p, c)
	for name, id := range source {
		if mapped[name] <= 0 || mapped[name] == id {
			t.Fatal("selected member was not reminted", name, id, mapped[name])
		}
	}
	if got := fixture.CubeComparisonTemporaryIDs(t, p, c); !reflect.DeepEqual(got, retained) {
		t.Fatalf("retained identities changed: %v want %v", got, retained)
	}
	fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Select("SELECT v FROM pg_temp.cube_compare_retained_0", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
}
