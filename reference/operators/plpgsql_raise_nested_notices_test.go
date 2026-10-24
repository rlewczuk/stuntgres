package reference_test

import (
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLRaiseNestedNotices(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var records []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t,p,b.PG,&records)
			run := func(step fixture.Step) { fixture.RunStep(t,p.Ctx,c,recorder,step) }
			for _, query := range []string{
				"CREATE FUNCTION rn_inner(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RAISE NOTICE 'notice=%',x USING ERRCODE='ZX987',DETAIL='notice detail',HINT='notice hint',SCHEMA='sch',TABLE='tab',COLUMN='col',DATATYPE='dtype',CONSTRAINT='con'; RAISE WARNING 'warning=%',x USING DETAIL='warning detail',HINT='warning hint'; RETURN x; END$$",
				"CREATE FUNCTION rn_outer(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN rn_inner(x); END$$",
				"CREATE FUNCTION rn_sql(x integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1; SELECT rn_outer(x)$$",
				"CREATE FUNCTION rn_for_source(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN SELECT rn_inner(x) AS v LOOP RETURN r.v; END LOOP; RETURN 0; END$$",
				"CREATE FUNCTION rn_for_body(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN SELECT x AS v LOOP RETURN rn_inner(x); END LOOP; RETURN 0; END$$",
				"CREATE DOMAIN rn_domain AS integer CHECK(VALUE IS NULL OR rn_inner(VALUE)>0)",
				"CREATE FUNCTION rn_domain_assignment(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE checked rn_domain := x; BEGIN checked := x; RETURN checked; END$$",
			} { run(fixture.Exec(query,0)) }
			want := func(value int, suffix string) []fixture.Diagnostic {
				notice := raiseAssertNotice("NOTICE","ZX987","notice="+strconv.Itoa(value),"rn_inner(integer)")
				notice.Detail, notice.Hint = "notice detail","notice hint"
				notice.Schema, notice.Table, notice.Column, notice.DataType, notice.Constraint = "sch","tab","col","dtype","con"
				notice.Context += suffix
				warning := raiseAssertNotice("WARNING","01000","warning="+strconv.Itoa(value),"rn_inner(integer)")
				warning.Detail, warning.Hint = "warning detail","warning hint"
				warning.Context += suffix
				return []fixture.Diagnostic{notice,warning}
			}
			const plCaller = "\nPL/pgSQL function rn_outer(integer) line 1 at RETURN"
			const sqlCaller = "\nSQL function \"rn_sql\" statement 2"
			for _, tc := range []struct{ name, query, suffix string }{
				{"inner","SELECT rn_inner($1) AS v",""},
				{"pl_caller","SELECT rn_outer($1) AS v",plCaller},
				{"sql_wrapper","SELECT rn_sql($1) AS v",plCaller+sqlCaller},
				{"for_source","SELECT rn_for_source($1) AS v","\nPL/pgSQL function rn_for_source(integer) line 1 at FOR over SELECT rows"},
				{"for_body","SELECT rn_for_body($1) AS v","\nPL/pgSQL function rn_for_body(integer) line 1 at RETURN"},
				{"domain_assignment","SELECT rn_domain_assignment($1) AS v","\nPL/pgSQL function rn_domain_assignment(integer) line 1 at assignment"},
			} {
				t.Run(tc.name,func(t *testing.T) {
					original, err := c.PrepareContext(p.Ctx,tc.query)
					if err != nil { t.Fatal(err) }
					defer original.Close()
					held := fixture.HeldStatement{Executor:c,Statement:original,SQL:tc.query}
					for _, route := range []string{"direct","fresh_prepared","original_held","original_held_again"} {
						t.Run(route,func(t *testing.T) {
							records = nil
							step := fixture.Select(tc.query,[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}},7)
							if route == "fresh_prepared" { step.Prepared = true }
							if route == "original_held" || route == "original_held_again" {
								fixture.RunStep(t,p.Ctx,held,recorder,step)
							} else { run(step) }
							expected := want(7,tc.suffix)
							if tc.name == "domain_assignment" {
								initialization := "\nPL/pgSQL function rn_domain_assignment(integer) line 1 during statement block local variable initialization"
								expected = append(want(7,initialization),expected...)
							}
							raiseAssertNotices(t,records,expected)
							// Reaching a top-level callee between held invocations must
							// not inherit either the PL caller or SQL wrapper context.
							records = nil
							run(fixture.Select("SELECT rn_inner(8) AS v",[]string{"v"},[]string{"INT4"},[][]any{{int64(8)}}))
							raiseAssertNotices(t,records,want(8,""))
							records = nil
							run(fixture.Exec(raiseAssertDO("RAISE NOTICE 'top';"),0))
							raiseAssertNotices(t,records,[]fixture.Diagnostic{raiseAssertNotice("NOTICE","00000","top","inline_code_block")})
						})
					}
				})
			}
		})
	}
}
