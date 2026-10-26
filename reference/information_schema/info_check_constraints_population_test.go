package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintsPopulation(t *testing.T) {
	t.Run("definitions", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			c.exec(
				"CREATE TABLE icc_defs(v integer CONSTRAINT icc_nn NOT NULL, CONSTRAINT icc_pos CHECK(v>0), CONSTRAINT icc_true CHECK(true), CONSTRAINT icc_null CHECK(NULL::boolean), CONSTRAINT icc_private CHECK(v<100) NO INHERIT)",
				`CREATE TABLE icc_odd(dead text, "Odd name" integer CONSTRAINT icc_odd_nn NOT NULL)`,
				"ALTER TABLE icc_odd DROP COLUMN dead",
				"CREATE DOMAIN icc_d AS integer CONSTRAINT icc_dom_nn NOT NULL CONSTRAINT icc_dom CHECK(VALUE>0)",
				"CREATE DOMAIN icc_derived AS icc_d",
				"CREATE DOMAIN icc_plain_d AS integer",
				"CREATE TABLE icc_plain(v integer)",
				"CREATE TABLE icc_unique(v integer CONSTRAINT icc_unique_key UNIQUE)",
				"CREATE TABLE icc_pending(v integer)",
				"ALTER TABLE icc_pending ADD CONSTRAINT icc_pending_nn NOT NULL v NOT VALID",
				"ALTER TABLE icc_pending ADD CONSTRAINT icc_pending_ck CHECK(v>0) NOT VALID")
			want := [][]any{
				c.row("public", "icc_nn", "v IS NOT NULL"), c.row("public", "icc_pos", "(v > 0)"),
				c.row("public", "icc_true", "true"), c.row("public", "icc_null", "NULL::boolean"),
				c.row("public", "icc_private", "(v < 100)"), c.row("public", "icc_odd_nn", "Odd name IS NOT NULL"),
				c.row("public", "icc_dom_nn", "VALUE IS NOT NULL"), c.row("public", "icc_dom", "(VALUE > 0)"),
				c.row("public", "icc_pending_nn", "v IS NOT NULL"), c.row("public", "icc_pending_ck", "(v > 0)"),
			}
			c.consumer()(want...)
			q := `SELECT c.conname,n.nspname,r.relname,t.typname,c.contype,c.conrelid<>0::oid AS relation,c.contypid<>0::oid AS domain,c.convalidated,c.conenforced,pg_get_expr(c.conbin,c.conrelid) AS expression FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace LEFT JOIN pg_class r ON r.oid=c.conrelid LEFT JOIN pg_type t ON t.oid=c.contypid WHERE c.conname LIKE 'icc_%' ORDER BY c.conname`
			facts := [][]any{
				{"icc_dom", "public", nil, "icc_d", "c", false, true, true, true, "(VALUE > 0)"},
				{"icc_dom_nn", "public", nil, "icc_d", "n", false, true, true, true, nil},
				{"icc_nn", "public", "icc_defs", nil, "n", true, false, true, true, nil},
				{"icc_null", "public", "icc_defs", nil, "c", true, false, true, true, "NULL::boolean"},
				{"icc_odd_nn", "public", "icc_odd", nil, "n", true, false, true, true, nil},
				{"icc_pending_ck", "public", "icc_pending", nil, "c", true, false, false, true, "(v > 0)"},
				{"icc_pending_nn", "public", "icc_pending", nil, "n", true, false, false, true, nil},
				{"icc_pos", "public", "icc_defs", nil, "c", true, false, true, true, "(v > 0)"},
				{"icc_private", "public", "icc_defs", nil, "c", true, false, true, true, "(v < 100)"},
				{"icc_true", "public", "icc_defs", nil, "c", true, false, true, true, "true"},
				{"icc_unique_key", "public", "icc_unique", nil, "u", true, false, true, true, nil},
			}
			s := fixture.Select(q, []string{"conname", "nspname", "relname", "typname", "contype", "relation", "domain", "convalidated", "conenforced", "expression"}, []string{"NAME", "NAME", "NAME", "NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT"}, facts)
			c.run(s)
			c.run(fixture.Prepared(s))
			c.exec("CREATE TABLE icc_nullable(v integer CONSTRAINT icc_nullable CHECK(v>0))")
			c.run(fixture.Exec("INSERT INTO icc_nullable VALUES(NULL)", 1))
			c.consumer()(append(want, c.row("public", "icc_nullable", "(v > 0)"))...)
			c.exec("CREATE TABLE icc_implicit(v integer NOT NULL)")
			var implicit string
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT conname FROM pg_constraint WHERE conrelid='icc_implicit'::regclass AND contype='n'").Scan(&implicit); err != nil || implicit == "" {
				c.t.Fatalf("actual implicit NOT NULL=%q error=%v", implicit, err)
			}
			s = c.step("SELECT * FROM information_schema.check_constraints WHERE constraint_name=$1 AND constraint_schema='public'", [][]any{c.row("public", implicit, "v IS NOT NULL")}, implicit)
			c.run(s)
			c.run(fixture.Prepared(s))
		})
	})
	t.Run("duplicate_bag", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			c.exec("CREATE SCHEMA icc_other", "CREATE TABLE icc_a(v integer CONSTRAINT icc_same CHECK(v>0))", "CREATE TABLE icc_b(v integer CONSTRAINT icc_same CHECK(v>0))", "CREATE TABLE icc_c(v integer CONSTRAINT icc_same CHECK(v<100))", "CREATE TABLE icc_other.icc_a(v integer CONSTRAINT icc_same CHECK(v>0))")
			a := c.row("public", "icc_same", "(v > 0)")
			other := c.row("icc_other", "icc_same", "(v > 0)")
			upper := c.row("public", "icc_same", "(v < 100)")
			check := c.consumer()
			check(a, a, upper, other)
			q := `SELECT n.nspname,r.relname,c.conname,pg_get_expr(c.conbin,c.conrelid) AS expression,c.oid>0::oid AS valid_oid,c.conrelid=r.oid AS owner,c.contypid=0::oid AS not_domain FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=c.connamespace WHERE c.conname='icc_same' ORDER BY n.nspname,r.relname`
			s := fixture.Select(q, []string{"nspname", "relname", "conname", "expression", "valid_oid", "owner", "not_domain"}, []string{"NAME", "NAME", "NAME", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"icc_other", "icc_a", "icc_same", "(v > 0)", true, true, true}, {"public", "icc_a", "icc_same", "(v > 0)", true, true, true}, {"public", "icc_b", "icc_same", "(v > 0)", true, true, true}, {"public", "icc_c", "icc_same", "(v < 100)", true, true, true}})
			c.run(s)
			c.run(fixture.Prepared(s))
			c.run(fixture.Select("SELECT count(DISTINCT oid) AS constraints,count(DISTINCT conrelid) AS relations FROM pg_constraint WHERE conname='icc_same'", []string{"constraints", "relations"}, []string{"INT8", "INT8"}, [][]any{{int64(4), int64(4)}}))
			c.exec("ALTER TABLE icc_a DROP CONSTRAINT icc_same")
			check(a, upper, other)
			c.exec("DROP TABLE icc_b")
			check(upper, other)
		})
	})
	t.Run("routine_witness", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			c.exec("CREATE FUNCTION icc_f(integer) RETURNS boolean RETURN $1>0", "CREATE TABLE icc_calls(v integer CONSTRAINT icc_calls CHECK(icc_f(v) AND icc_f(v)))", "CREATE DOMAIN icc_calls_d AS integer CONSTRAINT icc_calls_dom CHECK(icc_f(VALUE))")
			c.consumer()(c.row("public", "icc_calls", "(icc_f(v) AND icc_f(v))"), c.row("public", "icc_calls_dom", "icc_f(VALUE)"))
			iccRoutineWitness(c, "public", "icc_f")
		})
	})
	t.Run("scalar_array_coercions", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			c.exec("CREATE DOMAIN icc_choice AS character varying(3) CONSTRAINT icc_choice_in CHECK(VALUE IN ('ON','OFF')) CONSTRAINT icc_choice_not_in CHECK(VALUE NOT IN ('BAD','ERR'))")
			in := "((VALUE)::text = ANY ((ARRAY['ON'::character varying, 'OFF'::character varying])::text[]))"
			notIn := "((VALUE)::text <> ALL ((ARRAY['BAD'::character varying, 'ERR'::character varying])::text[]))"
			c.consumer()(c.row("public", "icc_choice_in", in), c.row("public", "icc_choice_not_in", notIn))
			// Exercise the shared expression renderer independently of the view.
			s := fixture.Select("SELECT conname,pg_get_expr(conbin,0) AS expression FROM pg_constraint WHERE contypid='icc_choice'::regtype ORDER BY conname", []string{"conname", "expression"}, []string{"NAME", "TEXT"}, [][]any{{"icc_choice_in", in}, {"icc_choice_not_in", notIn}})
			c.run(s)
			c.run(fixture.Prepared(s))
		})
	})
	t.Run("builtin_definitions", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			// REL_18_6 information_schema.sql declares real domain recipes, not synthetic rows.
			// The pinned PostgreSQL oracle independently confirms the ANY/array casts.
			s := c.step("SELECT * FROM information_schema.check_constraints WHERE constraint_schema='information_schema' AND constraint_name IN ('cardinal_number_domain_check','yes_or_no_check')", [][]any{c.row("information_schema", "cardinal_number_domain_check", "(VALUE >= 0)"), c.row("information_schema", "yes_or_no_check", "((VALUE)::text = ANY ((ARRAY['YES'::character varying, 'NO'::character varying])::text[]))")})
			c.run(s)
			c.run(fixture.Prepared(s))
		})
	})
}

func iccRoutineWitness(c *iccCase, schema, name string) {
	c.t.Helper()
	specific := rsuIdentity(c.t, c.p, c.conn, schema, name, "integer")
	q := "SELECT * FROM information_schema.check_constraint_routine_usage WHERE constraint_name IN ('icc_calls','icc_calls_dom')"
	s := fixture.Select(q, ccruColumns, ccruTypes, [][]any{{c.database, "public", "icc_calls", c.database, schema, specific}, {c.database, "public", "icc_calls_dom", c.database, schema, specific}})
	s.Bag = true
	c.run(s)
	c.run(fixture.Prepared(s))
	q = `SELECT c.conname,d.deptype,d.classid='pg_constraint'::regclass AS constraint_class,d.refclassid='pg_proc'::regclass AS routine_class,d.objsubid,d.refobjsubid FROM pg_constraint c JOIN pg_depend d ON d.objid=c.oid AND d.classid='pg_constraint'::regclass JOIN pg_proc p ON p.oid=d.refobjid AND d.refclassid='pg_proc'::regclass JOIN pg_namespace n ON n.oid=p.pronamespace WHERE c.conname IN ('icc_calls','icc_calls_dom') AND n.nspname=$1 AND p.proname=$2 ORDER BY c.conname`
	s = fixture.Select(q, []string{"conname", "deptype", "constraint_class", "routine_class", "objsubid", "refobjsubid"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "INT4", "INT4"}, [][]any{{"icc_calls", "n", true, true, int64(0), int64(0)}, {"icc_calls_dom", "n", true, true, int64(0), int64(0)}}, schema, name)
	c.run(s)
	c.run(fixture.Prepared(s))
}
