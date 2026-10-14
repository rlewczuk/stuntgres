package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegCollationVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rc_reader"})
	regObjectScenario(t, []string{"rc_left", "rc_right", "rc_private"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c,
			"CREATE SCHEMA rc_left", "CREATE SCHEMA rc_right", "CREATE SCHEMA rc_private",
			`CREATE COLLATION rc_left.same FROM pg_catalog."C"`,
			`CREATE COLLATION rc_right.same FROM pg_catalog."C"`,
			`CREATE COLLATION rc_left."C" FROM pg_catalog."C"`,
			`CREATE COLLATION rc_private.private_coll FROM pg_catalog."C"`,
			"CREATE ROLE rc_reader", "SET search_path TO rc_left,rc_right,public")
		var left, right int64
		if err := c.QueryRowContext(p.Ctx, `SELECT 'rc_left.same'::regcollation::bigint,'rc_right.same'::regcollation::bigint`).Scan(&left, &right); err != nil {
			t.Fatal(err)
		}
		if left == right || left == 0 || right == 0 {
			t.Fatal("distinct collations require distinct nonzero identities", left, right)
		}
		for _, order := range []struct {
			path, first, second string
			selected            int64
		}{
			{"rc_left,rc_right,public", "same", "rc_right.same", left},
			{"rc_right,rc_left,public", "rc_left.same", "same", right},
		} {
			regObjectExec(t, p, b, c, "SET search_path TO "+order.path)
			regObjectBoth(t, p, b, c, regObjectSelect("SELECT $1::regcollation AS a,$2::regcollation AS b,'same'::regcollation::bigint AS selected,'rc_left'::regnamespace AS n", []string{"a", "b", "selected", "n"}, []string{"REGCOLLATION", "REGCOLLATION", "INT8", "REGNAMESPACE"}, [][]any{{order.first, order.second, order.selected, "rc_left"}}, strconv.FormatInt(left, 10), strconv.FormatInt(right, 10)))
		}
		regObjectExec(t, p, b, c, "SET search_path TO rc_left,public")
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT '"C"'::regcollation::bigint AS c`, []string{"c"}, []string{"INT8"}, [][]any{{int64(950)}}))
		regObjectExec(t, p, b, c, "SET search_path TO rc_left,pg_catalog")
		regObjectBoth(t, p, b, c, regObjectSelect(`SELECT 'pg_catalog."C"'::regcollation AS c,('"C"'::regcollation::oid='rc_left."C"'::regcollation::oid) AS shadow`, []string{"c", "shadow"}, []string{"REGCOLLATION", "BOOL"}, [][]any{{`pg_catalog."C"`, true}}))
		regObjectExec(t, p, b, c, "SET search_path TO rc_private,public", "SET ROLE rc_reader")
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regObjectError(`SELECT 'private_coll'::regcollation`, "42704", `collation "private_coll" for encoding "UTF8" does not exist`, "8", ""))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regObjectError(`SELECT 'rc_private.private_coll'::regcollation`, "42501", "permission denied for schema rc_private", "8", ""))
		regObjectBoth(t, p, b, c, regObjectSelect(`SELECT 'rc_private'::regnamespace AS n`, []string{"n"}, []string{"REGNAMESPACE"}, [][]any{{"rc_private"}}))
		regObjectExec(t, p, b, c, "RESET ROLE", "CREATE TEMP TABLE rc_temp_anchor(v integer)", `CREATE COLLATION pg_temp.rc_temp_coll FROM pg_catalog."C"`, "SET search_path TO pg_temp,public")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regObjectError(`SELECT 'rc_temp_coll'::regcollation`, "42704", `collation "rc_temp_coll" for encoding "UTF8" does not exist`, "8", ""))
		// Temporary schema allocator spelling is not portable; assert identity instead.
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT ('pg_temp.rc_temp_coll'::regcollation::oid=oid) AS identity FROM pg_catalog.pg_collation WHERE collname='rc_temp_coll'`, []string{"identity"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regObjectError(`SELECT 'pg_temp'::regnamespace`, "3F000", `schema "pg_temp" does not exist`, "8", ""))
	})
}
