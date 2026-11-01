package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func regLookupIdentity(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) int64 {
	t.Helper()
	var id int64
	if err := c.QueryRowContext(p.Ctx, query).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatal("catalog identity must be positive", query, id)
	}
	return id
}

func TestReferenceRegLookupCollRoleLifecycle(t *testing.T) {
	regLookupScenario(t, []string{"cl_left", "cl_right"}, []string{"cl_live", "cl_renamed"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c, "CREATE SCHEMA cl_left", "CREATE SCHEMA cl_right",
			`CREATE COLLATION cl_left.same FROM pg_catalog."C"`,
			`CREATE COLLATION cl_right.same FROM pg_catalog."C"`,
			`CREATE COLLATION cl_left."C" FROM pg_catalog."C"`,
			"CREATE ROLE cl_live", "SET search_path TO cl_left,cl_right,public")
		left := regLookupIdentity(t, p, c, `SELECT 'cl_left.same'::regcollation::oid::bigint`)
		right := regLookupIdentity(t, p, c, `SELECT 'cl_right.same'::regcollation::oid::bigint`)
		role := regLookupIdentity(t, p, c, `SELECT oid::bigint FROM pg_catalog.pg_roles WHERE rolname='cl_live'`)
		if left == right {
			t.Fatal("two collation definitions share an identity")
		}
		constantSQL := `SELECT to_regcollation('same') AS c,to_regcollation('same')::oid::bigint AS id,to_regcollation('cl_left.same')::oid::bigint AS qualified,to_regrole('cl_live')::oid::bigint AS role`
		parameterSQL := `SELECT to_regcollation($1::text) AS c,to_regcollation($1::text)::oid::bigint AS id,to_regcollation($2::text)::oid::bigint AS qualified,to_regrole($3::text)::oid::bigint AS role`
		constant := regLookupHeld(t, p, c, constantSQL)
		defer constant.Statement.Close()
		parameter := regLookupHeld(t, p, c, parameterSQL)
		defer parameter.Statement.Close()
		checkpoint := func(spelling any, selected, qualified any) {
			t.Helper()
			want := [][]any{{spelling, selected, qualified, role}}
			for _, route := range []struct {
				held fixture.HeldStatement
				args []any
			}{{constant, nil}, {parameter, []any{"same", "cl_left.same", "cl_live"}}} {
				s := regLookupSelect(b, route.held.SQL, []string{"c", "id", "qualified", "role"}, []string{"REGCOLLATION", "INT8", "INT8", "INT8"}, want, route.args...)
				regObjectBoth(t, p, b, c, s)
				regObjectRunStep(t, p, b, route.held, s)
			}
		}
		checkpoint("same", left, left)
		regObjectExec(t, p, b, c, "SET search_path TO cl_right,cl_left,public")
		checkpoint("same", right, left)
		regObjectExec(t, p, b, c, "SET search_path TO cl_left,public", "ALTER COLLATION cl_left.same RENAME TO changed")
		checkpoint(nil, nil, nil)
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regcollation('cl_left.changed')::oid::bigint AS id`, []string{"id"}, []string{"INT8"}, [][]any{{left}}))
		regObjectExec(t, p, b, c, "DROP COLLATION cl_left.changed")
		checkpoint(nil, nil, nil)
		regObjectExec(t, p, b, c, `CREATE COLLATION cl_left.same FROM pg_catalog."C"`)
		replacement := regLookupIdentity(t, p, c, `SELECT 'cl_left.same'::regcollation::oid::bigint`)
		if replacement == left {
			t.Fatal("recreated collation retained its dropped identity")
		}
		checkpoint("same", replacement, replacement)

		catalogSQL := `SELECT to_regcollation('"C"')::oid::bigint AS selected,to_regcollation('pg_catalog."C"')::oid::bigint AS qualified`
		catalog := regLookupHeld(t, p, c, catalogSQL)
		defer catalog.Statement.Close()
		shadow := regLookupIdentity(t, p, c, `SELECT 'cl_left."C"'::regcollation::oid::bigint`)
		for _, tc := range []struct {
			path string
			id int64
		}{{"cl_left,public", 950}, {"cl_left,pg_catalog", shadow}, {"pg_catalog,cl_left", 950}} {
			regObjectExec(t, p, b, c, "SET search_path TO "+tc.path)
			s := fixture.Select(catalogSQL, []string{"selected", "qualified"}, []string{"INT8", "INT8"}, [][]any{{tc.id, int64(950)}})
			regObjectBoth(t, p, b, c, s)
			regObjectRunStep(t, p, b, catalog, s)
		}
		regObjectExec(t, p, b, c, "CREATE TEMP TABLE cl_temp_anchor(v integer)", `CREATE COLLATION pg_temp.cl_temp_coll FROM pg_catalog."C"`, "SET search_path TO pg_temp,cl_left,public")
		regObjectBoth(t, p, b, c, regLookupSelect(b, `SELECT to_regcollation('cl_temp_coll') AS c`, []string{"c"}, []string{"REGCOLLATION"}, [][]any{{nil}}))
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regcollation('pg_temp.cl_temp_coll')::oid=oid AS identity FROM pg_catalog.pg_collation WHERE collname='cl_temp_coll'`, []string{"identity"}, []string{"BOOL"}, [][]any{{true}}))

		roleConstantSQL := `SELECT to_regrole('cl_live') AS old,to_regrole('cl_live')::oid::bigint AS old_id,to_regrole('cl_renamed') AS new,to_regrole('cl_renamed')::oid::bigint AS new_id`
		roleParameterSQL := `SELECT to_regrole($1::text) AS old,to_regrole($1::text)::oid::bigint AS old_id,to_regrole($2::text) AS new,to_regrole($2::text)::oid::bigint AS new_id`
		roleConstant := regLookupHeld(t, p, c, roleConstantSQL)
		defer roleConstant.Statement.Close()
		roleParameter := regLookupHeld(t, p, c, roleParameterSQL)
		defer roleParameter.Statement.Close()
		roleCheckpoint := func(oldName, oldID, newName, newID any) {
			t.Helper()
			for _, route := range []struct {
				held fixture.HeldStatement
				args []any
			}{{roleConstant, nil}, {roleParameter, []any{"cl_live", "cl_renamed"}}} {
				s := regLookupSelect(b, route.held.SQL, []string{"old", "old_id", "new", "new_id"}, []string{"REGROLE", "INT8", "REGROLE", "INT8"}, [][]any{{oldName, oldID, newName, newID}}, route.args...)
				regObjectBoth(t, p, b, c, s)
				regObjectRunStep(t, p, b, route.held, s)
			}
		}
		roleCheckpoint("cl_live", role, nil, nil)
		regObjectExec(t, p, b, c, "SET search_path TO pg_catalog")
		roleCheckpoint("cl_live", role, nil, nil)
		regObjectExec(t, p, b, c, "ALTER ROLE cl_live RENAME TO cl_renamed")
		if renamed := regLookupIdentity(t, p, c, `SELECT oid::bigint FROM pg_catalog.pg_roles WHERE rolname='cl_renamed'`); renamed != role {
			t.Fatal("role rename changed identity", renamed, role)
		}
		roleCheckpoint(nil, nil, "cl_renamed", role)
		regObjectExec(t, p, b, c, "DROP ROLE cl_renamed")
		roleCheckpoint(nil, nil, nil, nil)
		regObjectExec(t, p, b, c, "CREATE ROLE cl_live")
		newRole := regLookupIdentity(t, p, c, `SELECT oid::bigint FROM pg_catalog.pg_roles WHERE rolname='cl_live'`)
		if newRole == role {
			t.Fatal("recreated role retained its dropped identity")
		}
		roleCheckpoint("cl_live", newRole, nil, nil)
	})
}
