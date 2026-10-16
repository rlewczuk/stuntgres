package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Released INTERNAL strict Stable scalar functions, not SQL-body wrappers.
// https://github.com/postgres/postgres/blob/REL_18_0/src/include/catalog/pg_proc.dat
// Expectations for new ACL/demand behavior require the pinned oracle first.
type objectGrantAccessCase struct {
	label, target, name string
	query, runtime      string
	args, falseArgs     []any
	nullQuery           string
	nullArgs            []any
}

func objectGrantAccessCases(t *testing.T, p *fixture.Pair, c *sql.Conn, run func(fixture.Step)) []objectGrantAccessCase {
	t.Helper()
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
	run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH ADMIN FALSE, INHERIT FALSE", 0))
	// The actor is neither owner nor BYPASSRLS/SUPERUSER. These are actual
	// introspection targets: no policy execution or fake RLS row filtering.
	run(fixture.Exec("CREATE TABLE og_access_active(id INT)", 0))
	run(fixture.Exec("CREATE TABLE og_access_inactive(id INT)", 0))
	run(fixture.Exec("ALTER TABLE og_access_active OWNER TO og_object_other", 0))
	run(fixture.Exec("ALTER TABLE og_access_inactive OWNER TO og_object_other", 0))
	run(fixture.Exec("ALTER TABLE og_access_active ENABLE ROW LEVEL SECURITY", 0))
	var actor, target, active, inactive int64
	for _, q := range []struct {
		sql string
		out *int64
	}{
		{"SELECT oid FROM pg_roles WHERE rolname='og_object_reader'", &actor},
		{"SELECT oid FROM pg_roles WHERE rolname='og_object_other'", &target},
		{"SELECT oid FROM pg_class WHERE relname='og_access_active'", &active},
		{"SELECT oid FROM pg_class WHERE relname='og_access_inactive'", &inactive},
	} {
		if err := c.QueryRowContext(p.Ctx, q.sql).Scan(q.out); err != nil {
			t.Fatal(err)
		}
	}
	run(fixture.Exec("CREATE TABLE og_access_inputs(id INT,an name,ao oid,tn name,toid oid,rn text,ro oid)", 0))
	run(fixture.Exec("INSERT INTO og_access_inputs VALUES(1,$1::name,$2::oid,$3::name,$4::oid,$5::text,$6::oid),(2,NULL,NULL,NULL,NULL,NULL,NULL)", 2,
		"og_object_reader", actor, "og_object_other", target, "og_access_active", active))
	run(fixture.Exec("GRANT SELECT ON og_access_inputs TO og_object_reader", 0))
	return []objectGrantAccessCase{
		{"name-name", "pg_catalog.pg_has_role(name,name,text)", "pg_has_role",
			"SELECT pg_has_role($1::name,$2::name,$3::text) AS v", "pg_has_role(an,tn,'MEMBER')",
			[]any{"og_object_reader", "og_object_other", "MEMBER"}, []any{"og_object_reader", "og_object_other", "MEMBER WITH ADMIN OPTION"},
			"SELECT pg_has_role(NULL::name,'missing'::name,'bad') AS v", []any{nil, "og_object_other", "MEMBER"}},
		{"name-oid", "pg_catalog.pg_has_role(name,oid,text)", "pg_has_role",
			"SELECT pg_has_role($1::name,$2::oid,$3::text) AS v", "pg_has_role(an,toid,'MEMBER')",
			[]any{"og_object_reader", target, "MEMBER"}, []any{"og_object_reader", target, "MEMBER WITH ADMIN OPTION"},
			"SELECT pg_has_role(NULL::name,0::oid,'bad') AS v", []any{nil, target, "MEMBER"}},
		{"oid-name", "pg_catalog.pg_has_role(oid,name,text)", "pg_has_role",
			"SELECT pg_has_role($1::oid,$2::name,$3::text) AS v", "pg_has_role(ao,tn,'MEMBER')",
			[]any{actor, "og_object_other", "MEMBER"}, []any{actor, "og_object_other", "MEMBER WITH ADMIN OPTION"},
			"SELECT pg_has_role(NULL::oid,'missing'::name,'bad') AS v", []any{nil, "og_object_other", "MEMBER"}},
		{"oid-oid", "pg_catalog.pg_has_role(oid,oid,text)", "pg_has_role",
			"SELECT pg_has_role($1::oid,$2::oid,$3::text) AS v", "pg_has_role(ao,toid,'MEMBER')",
			[]any{actor, target, "MEMBER"}, []any{actor, target, "MEMBER WITH ADMIN OPTION"},
			"SELECT pg_has_role(NULL::oid,0::oid,'bad') AS v", []any{nil, target, "MEMBER"}},
		{"implicit-name", "pg_catalog.pg_has_role(name,text)", "pg_has_role",
			"SELECT pg_has_role($1::name,$2::text) AS v", "pg_has_role(tn,'MEMBER')",
			[]any{"og_object_other", "MEMBER"}, []any{"og_object_other", "MEMBER WITH ADMIN OPTION"},
			"SELECT pg_has_role(NULL::name,'bad') AS v", []any{nil, "MEMBER"}},
		{"implicit-oid", "pg_catalog.pg_has_role(oid,text)", "pg_has_role",
			"SELECT pg_has_role($1::oid,$2::text) AS v", "pg_has_role(toid,'MEMBER')",
			[]any{target, "MEMBER"}, []any{target, "MEMBER WITH ADMIN OPTION"},
			"SELECT pg_has_role(NULL::oid,'bad') AS v", []any{nil, "MEMBER"}},
		{"rls-oid", "pg_catalog.row_security_active(oid)", "row_security_active",
			"SELECT row_security_active($1::oid) AS v", "row_security_active(ro)",
			[]any{active}, []any{inactive},
			"SELECT row_security_active(NULL::oid) AS v", []any{nil}},
		{"rls-text", "pg_catalog.row_security_active(text)", "row_security_active",
			"SELECT row_security_active($1::text) AS v", "row_security_active(rn)",
			[]any{"og_access_active"}, []any{"og_access_inactive"},
			"SELECT row_security_active(NULL::text) AS v", []any{nil}},
	}
}

func objectGrantAccessValue(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{value}}, args...)
}

func objectGrantAccessDenied(q, name string, query bool, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name}, args...)
	s.SelectRows, s.ErrorStage = query, "execute"
	return s
}

func objectGrantAccessFamily(t *testing.T, rls bool) {
	t.Helper()
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		cases := objectGrantAccessCases(t, p, c, run)
		for i, tc := range cases {
			if (tc.name == "row_security_active") != rls {
				continue
			}
			if !t.Run(tc.label, func(t *testing.T) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				defer objectGrantRoutineCleanup(t, p, c, []string{tc.target})
				stmt, err := c.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
				runHeld := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
				// Nearest sibling differs on exactly one formal axis or arity.
				sibling := cases[i^1]
				control := func() {
					run(objectGrantAccessValue(sibling.query, true, sibling.args...))
					// This implicit-user UNKNOWN input must still select NAME,TEXT.
					if tc.target != "pg_catalog.pg_has_role(name,text)" {
						run(objectGrantAccessValue("SELECT pg_has_role('og_object_other','MEMBER') AS v", true))
					}
				}
				allowed := func() {
					run(objectGrantAccessValue(tc.query, true, tc.args...))
					run(objectGrantAccessValue(tc.query, false, tc.falseArgs...))
					runHeld(objectGrantAccessValue(tc.query, true, tc.args...))
					runHeld(objectGrantAccessValue(tc.query, false, tc.falseArgs...))
					if tc.label == "implicit-name" {
						run(objectGrantAccessValue("SELECT pg_has_role('og_object_other','MEMBER') AS v", true))
						run(objectGrantAccessValue("SELECT pg_has_role($1,$2) AS v", true, "og_object_other", "MEMBER"))
					}
					control()
				}
				denied := func() {
					for _, args := range [][]any{tc.args, tc.falseArgs} {
						run(objectGrantAccessDenied(tc.query, tc.name, true, args...))
						run(objectGrantAccessDenied(tc.query, tc.name, false, args...))
						runHeld(objectGrantAccessDenied(tc.query, tc.name, true, args...))
					}
					// Quiet Prepare with no X is itself a required public boundary.
					fresh, err := c.PrepareContext(p.Ctx, tc.query)
					if err != nil {
						t.Fatal("Prepare while exact EXECUTE denied", err)
					}
					defer fresh.Close()
					freshHeld := fixture.HeldStatement{Executor: c, Statement: fresh, SQL: tc.query}
					fixture.RunStep(t, p.Ctx, freshHeld, b.Recorder, objectGrantAccessDenied(tc.query, tc.name, true, tc.args...))
					if tc.label == "implicit-name" {
						run(objectGrantAccessDenied("SELECT pg_has_role('og_object_other','MEMBER') AS v", tc.name, true))
						run(objectGrantAccessDenied("SELECT pg_has_role($1,$2) AS v", tc.name, true, "og_object_other", "MEMBER"))
					}
					control()
				}
				fixture.RoleGrantActor(run, "og_object_reader", allowed)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM PUBLIC", 0))
				fixture.RoleGrantActor(run, "og_object_reader", denied)
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", allowed)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", denied)
			}) {
				t.FailNow()
			}
		}
	})
}

func TestReferenceObjectGrantAccessInquiryRoleAuthority(t *testing.T) {
	objectGrantAccessFamily(t, false)
}

func TestReferenceObjectGrantAccessInquiryRLSAuthority(t *testing.T) {
	objectGrantAccessFamily(t, true)
}

func TestReferenceObjectGrantAccessInquiryDemand(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		cases := objectGrantAccessCases(t, p, c, run)
		for _, tc := range cases {
			if !t.Run(tc.label, func(t *testing.T) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				defer objectGrantRoutineCleanup(t, p, c, []string{tc.target})
				// Calls surviving planning must initialize even if the argument
				// column is NULL or no tuple will be returned. Static FALSE is
				// included separately, not inferred from the empty data source.
				queries := []string{
					"SELECT " + tc.runtime + " AS v FROM og_access_inputs WHERE id=1",
					"SELECT " + tc.runtime + " AS v FROM og_access_inputs WHERE id=2",
					"SELECT " + tc.runtime + " AS v FROM og_access_inputs WHERE id=99",
					"SELECT " + tc.runtime + " AS v FROM og_access_inputs WHERE FALSE",
				}
				wants := [][][]any{{{true}}, {{nil}}, nil, nil}
				held := make([]fixture.HeldStatement, len(queries))
				for i, q := range queries {
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				phase := func(deny bool) {
					for i, q := range queries {
						s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, wants[i])
						if deny {
							s = objectGrantAccessDenied(q, tc.name, true)
						}
						run(s)
						// Held reuse must not turn NULL/empty success into an ACL cache.
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
					}
					// Strict literal NULL is simplified before provider validation.
					run(objectGrantAccessValue(tc.nullQuery, nil))
					// Fresh custom plans may simplify a bound NULL; no generic-plan
					// promise is asserted by retaining a sixth execution here.
					run(objectGrantAccessValue(tc.query, nil, tc.nullArgs...))
					dead := "SELECT CASE WHEN FALSE THEN " + tc.runtime + " ELSE FALSE END AS v FROM og_access_inputs WHERE id=1"
					run(objectGrantAccessValue(dead, false))
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() { phase(false) })
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM PUBLIC", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { phase(true) })
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { phase(false) })
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { phase(true) })
			}) {
				t.FailNow()
			}
		}
	})
}
