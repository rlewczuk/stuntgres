package reference_test

import (
	"database/sql"
	"fmt"
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

func cubeComparisonTemporaryIDs(t *testing.T, p *fixture.Pair, c *sql.Conn) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, part := range []struct{ table, name, filter string }{
		{"pg_class", "relname", "relnamespace=pg_my_temp_schema()"},
		{"pg_type", "typname", "typnamespace=pg_my_temp_schema()"},
		{"pg_namespace", "nspname", "(nspname LIKE 'pg_temp_%' OR nspname LIKE 'pg_toast_temp_%') AND NOT pg_is_other_temp_schema(oid)"},
	} {
		rows, err := c.QueryContext(p.Ctx, "SELECT "+part.name+",oid::bigint FROM "+part.table+" WHERE "+part.filter+" ORDER BY oid")
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
			if id <= 0 {
				rows.Close()
				t.Fatal("invalid retained identity", id)
			}
			out[part.table+"/"+name] = id
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	return out
}

func cubeComparisonForceCollisions(t *testing.T, p *fixture.Pair, c *sql.Conn, source map[string]int64) map[string]int64 {
	t.Helper()
	var maximum int64
	for _, id := range source {
		maximum = max(maximum, id)
	}
	create := func(i int) {
		fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE cube_compare_retained_%d(v integer)", i), 0))
	}
	create(0)
	retained := cubeComparisonTemporaryIDs(t, p, c)
	var start, last int64
	for _, id := range retained {
		if start == 0 || id < start {
			start = id
		}
		last = max(last, id)
	}
	if start == 0 || start > maximum {
		t.Fatalf("retained allocation starts beyond source graph: %d/%d", start, maximum)
	}
	// At least one newly observed identity per table is required below; this
	// bound follows from the observed interval, not a guessed objects-per-DDL.
	bound := maximum - start + 1
	for i := int64(1); last < maximum && i <= bound; i++ {
		create(int(i))
		retained = cubeComparisonTemporaryIDs(t, p, c)
		var next int64
		for _, id := range retained {
			next = max(next, id)
		}
		if next <= last {
			t.Fatal("retained allocator made no progress", last, next)
		}
		last = next
	}
	occupied := map[int64]bool{}
	for _, id := range retained {
		occupied[id] = true
	}
	for name, id := range source {
		if !occupied[id] {
			t.Fatalf("retained identity set has a hole at %s=%d: %v", name, id, retained)
		}
	}
	fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec("INSERT INTO pg_temp.cube_compare_retained_0 VALUES (73)", 1))
	return retained
}

func cubeComparisonCheckRemap(t *testing.T, p *fixture.Pair, c *sql.Conn, source, retained map[string]int64) {
	t.Helper()
	mapped := cubeComparisonMemberIDs(t, p, c)
	for name, id := range source {
		if mapped[name] <= 0 || mapped[name] == id {
			t.Fatal("selected member was not reminted", name, id, mapped[name])
		}
	}
	if got := cubeComparisonTemporaryIDs(t, p, c); !reflect.DeepEqual(got, retained) {
		t.Fatalf("retained identities changed: %v want %v", got, retained)
	}
	fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Select("SELECT v FROM pg_temp.cube_compare_retained_0", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
}
