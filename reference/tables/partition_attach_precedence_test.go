package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func partitionAttachBoundPrecedence(t *testing.T) {
	cases := []struct {
		name, candidate, lower, upper string
		setup                         []string
		want                          fixture.Diagnostic
		token                         string
		drawn                         bool
	}{
		{name: "missing_candidate_null", candidate: "pa_absent", lower: "NULL,0", upper: "20,0", want: fixture.Diagnostic{Code: "42P17", Message: "cannot specify NULL in range bound"}},
		{name: "missing_candidate_draw", candidate: "pa_absent", lower: "nextval('pa_order_seq'),0", upper: "20,0", want: fixture.Diagnostic{Code: "42P01", Message: `relation "pa_absent" does not exist`}, drawn: true},
		{name: "schema_after_draw", setup: []string{"CREATE TABLE pa_child(k BIGINT,j TEXT)"}, lower: "nextval('pa_order_seq'),0", upper: "20,0", want: fixture.Diagnostic{Code: "42804", Message: `child table "pa_child" has different type for column "j"`}, drawn: true},
		{name: "schema_vs_null", setup: []string{"CREATE TABLE pa_child(k BIGINT,j TEXT)"}, lower: "NULL,0", upper: "20,0", want: fixture.Diagnostic{Code: "42P17", Message: "cannot specify NULL in range bound"}},
		{name: "from_arity", lower: "nextval('pa_order_seq')", upper: "k,0", want: fixture.Diagnostic{Code: "42P16", Message: "FROM must specify exactly one value per partitioning column"}},
		{name: "to_arity", lower: "nextval('pa_order_seq'),0", upper: "k", want: fixture.Diagnostic{Code: "42P16", Message: "TO must specify exactly one value per partitioning column"}},
		{name: "lower_null_before_upper_column", lower: "NULL,0", upper: "k,0", want: fixture.Diagnostic{Code: "42P17", Message: "cannot specify NULL in range bound"}},
		{name: "lower_draw_before_upper_column", lower: "nextval('pa_order_seq'),0", upper: "k,0", want: fixture.Diagnostic{Code: "0A000", Message: "cannot use column reference in partition bound expression"}, token: "k,0", drawn: true},
		{name: "sentinel_suffix_after_draw", lower: "MINVALUE,nextval('pa_order_seq')", upper: "20,0", want: fixture.Diagnostic{Code: "42804", Message: "every bound following MINVALUE must also be MINVALUE"}, token: "nextval", drawn: true},
		{name: "null_before_suffix", lower: "MINVALUE,NULL", upper: "20,0", want: fixture.Diagnostic{Code: "42P17", Message: "cannot specify NULL in range bound"}},
		{name: "overlap_after_draw", setup: []string{"CREATE TABLE pa_old PARTITION OF pa_parent FOR VALUES FROM(10,0) TO(20,0)"}, lower: "nextval('pa_order_seq'),0", upper: "20,0", want: fixture.Diagnostic{Code: "42P17", Message: `partition "pa_child" would overlap partition "pa_old"`}, token: "nextval", drawn: true},
		{name: "ordinary_parent_before_draw", setup: []string{"CREATE TABLE pa_parent(k BIGINT,j BIGINT)"}, lower: "nextval('pa_order_seq'),0", upper: "20,0", want: fixture.Diagnostic{Code: "42809", Message: `ALTER action ATTACH PARTITION cannot be performed on relation "pa_parent"`, Detail: "This operation is not supported for tables."}},
		{name: "attached_candidate_after_draw", setup: []string{"CREATE TABLE pa_other(k BIGINT,j BIGINT) PARTITION BY RANGE(k)", "CREATE TABLE pa_child PARTITION OF pa_other DEFAULT"}, lower: "nextval('pa_order_seq'),0", upper: "20,0", want: fixture.Diagnostic{Code: "42809", Message: `"pa_child" is already a partition`}, drawn: true},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared {
				route = "prepared"
			}
			t.Run("precedence/"+c.name+"/"+route, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE SEQUENCE pa_order_seq START 10", 0))
						hasParent, hasChild := false, false
						for _, q := range c.setup {
							if len(q) >= 22 && q[:22] == "CREATE TABLE pa_parent" {
								hasParent = true
							}
							if len(q) >= 21 && q[:21] == "CREATE TABLE pa_child" {
								hasChild = true
							}
						}
						if !hasParent {
							run(fixture.Exec("CREATE TABLE pa_parent(k BIGINT,j BIGINT) PARTITION BY RANGE(k,j)", 0))
						}
						if !hasChild {
							run(fixture.Exec("CREATE TABLE pa_child(k BIGINT,j BIGINT)", 0))
						}
						for _, q := range c.setup {
							run(fixture.Exec(q, 0))
						}
						candidate := c.candidate
						if candidate == "" {
							candidate = "pa_child"
						}
						q := "ALTER TABLE pa_parent ATTACH PARTITION " + candidate + " FOR VALUES FROM(" + c.lower + ") TO(" + c.upper + ")"
						want := c.want
						if c.token != "" {
							want.Position = referenceRangePosition(q, c.token)
						}
						s := fixture.ImplicitError(q, want)
						s.Prepared = prepared
						t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) })
						t.Run("bound_effects", func(t *testing.T) {
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT last_value,is_called FROM pa_order_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(10), c.drawn}}))
							next := int64(10)
							if c.drawn {
								next = 11
							}
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT nextval('pa_order_seq') AS next", []string{"next"}, []string{"INT8"}, [][]any{{next}}))
						})
						run(fixture.Select("SELECT COUNT(*) FROM pg_depend d JOIN pg_class c ON c.oid=d.objid JOIN pg_class p ON p.oid=d.refobjid WHERE c.relname='pa_child' AND p.relname='pa_parent' AND d.classid=1259 AND d.refclassid=1259 AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='a'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					})
				}
			})
		}
	}
}

func partitionAttachOwnerDiagnostics(t *testing.T) {
	for _, candidateDenied := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			name := "parent"
			if candidateDenied {
				name = "candidate"
			}
			if prepared {
				name += "/prepared"
			} else {
				name += "/direct"
			}
			t.Run("ownership/"+name, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"pa_owner"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
						run(fixture.Exec("CREATE ROLE pa_owner", 0))
						run(fixture.Exec("CREATE TABLE pa_parent(k INT) PARTITION BY RANGE(k)", 0))
						run(fixture.Exec("CREATE TABLE pa_child(k INT)", 0))
						if b.PG {
							run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pa_owner", 0))
						} else {
							// Native schema GRANT is an existing boundary. Transfer
							// only schema ownership; bootstrap still owns both tables.
							run(fixture.Exec("ALTER SCHEMA public OWNER TO pa_owner", 0))
						}
						denied := "pa_parent"
						if candidateDenied {
							run(fixture.Exec("ALTER TABLE pa_parent OWNER TO pa_owner", 0))
							denied = "pa_child"
						} else {
							run(fixture.Exec("ALTER TABLE pa_child OWNER TO pa_owner", 0))
						}
						run(fixture.Exec("CREATE SEQUENCE pa_owner_seq START 10", 0))
						run(fixture.Exec("ALTER SEQUENCE pa_owner_seq OWNER TO pa_owner", 0))
						run(fixture.Exec("SET ROLE pa_owner", 0))
						defer conn.ExecContext(p.Ctx, "RESET ROLE")
						nullQuery := "ALTER TABLE pa_parent ATTACH PARTITION pa_child FOR VALUES FROM(NULL) TO(3)"
						nullWant := fixture.Diagnostic{Code: "42501", Message: "must be owner of table " + denied}
						if candidateDenied {
							nullWant = fixture.Diagnostic{Code: "42P17", Message: "cannot specify NULL in range bound"}
						}
						nullStep := fixture.ImplicitError(nullQuery, nullWant)
						nullStep.Prepared = prepared
						t.Run("null_vs_owner", func(t *testing.T) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, nullStep) })
						q := "ALTER TABLE pa_parent ATTACH PARTITION pa_child FOR VALUES FROM(nextval('pa_owner_seq')) TO(20)"
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of table " + denied})
						s.Prepared = prepared
						t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) })
						run(fixture.Exec("RESET ROLE", 0))
						t.Run("owner_bound_effect", func(t *testing.T) {
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT last_value,is_called FROM pa_owner_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(10), candidateDenied}}))
						})
						run(fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_child'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
						run(fixture.Exec(q, 0))
					})
				}
			})
		}
	}
}
