package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var executionVisibilityNames = []string{"pg_function_is_visible", "pg_operator_is_visible", "pg_opclass_is_visible", "pg_opfamily_is_visible"}

type executionVisibilityObject struct {
	fn, identity, drop string
}

func executionVisibilityObjects() []executionVisibilityObject {
	var objects []executionVisibilityObject
	for _, ns := range []string{"vex_a", "vex_b"} {
		objects = append(objects,
			executionVisibilityObject{executionVisibilityNames[0], "'"+ns+".vis_fn(integer)'::regprocedure::oid", "DROP FUNCTION "+ns+".vis_fn(integer)"},
			executionVisibilityObject{executionVisibilityNames[1], "'"+ns+".##(integer,integer)'::regoperator::oid", "DROP OPERATOR "+ns+".##(integer,integer)"},
			executionVisibilityObject{executionVisibilityNames[2], "(SELECT c.oid FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace WHERE n.nspname='"+ns+"' AND c.opcname='vis_class' AND c.opcmethod=403)", "DROP OPERATOR CLASS "+ns+".vis_class USING btree"},
			executionVisibilityObject{executionVisibilityNames[3], "(SELECT f.oid FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE n.nspname='"+ns+"' AND f.opfname='vis_family' AND f.opfmethod=403)", "DROP OPERATOR FAMILY "+ns+".vis_family USING btree"})
	}
	return append(objects,
		executionVisibilityObject{executionVisibilityNames[0], "'vex_b.vis_fn(text)'::regprocedure::oid", ""},
		executionVisibilityObject{executionVisibilityNames[1], "'vex_b.##(bigint,bigint)'::regoperator::oid", ""},
		executionVisibilityObject{executionVisibilityNames[2], "(SELECT c.oid FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace WHERE n.nspname='vex_b' AND c.opcname='vis_class' AND c.opcmethod=405)", ""},
		executionVisibilityObject{executionVisibilityNames[3], "(SELECT f.oid FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE n.nspname='vex_b' AND f.opfname='vis_family' AND f.opfmethod=405)", ""})
}

func executionVisibilityInstall(run func(fixture.Step)) {
	for _, ns := range []string{"vex_a", "vex_b"} {
		result := "integer"
		classItems := "FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)"
		if ns == "vex_b" {
			result = "text"
			classItems = "FOR TYPE bigint USING btree AS OPERATOR 3 pg_catalog.="
		}
		for _, q := range []string{
			"CREATE SCHEMA "+ns,
			"CREATE FUNCTION "+ns+".vis_fn(integer) RETURNS "+result+" LANGUAGE SQL AS 'SELECT $1::"+result+"'",
			"CREATE FUNCTION "+ns+".op(integer,integer) RETURNS boolean LANGUAGE SQL AS 'SELECT $1=$2'",
			"CREATE OPERATOR "+ns+".## (LEFTARG=integer, RIGHTARG=integer, FUNCTION="+ns+".op)",
			"CREATE OPERATOR CLASS "+ns+".vis_class "+classItems,
			"CREATE OPERATOR FAMILY "+ns+".vis_family USING btree",
			"CREATE PROCEDURE "+ns+".vis_proc(integer) LANGUAGE SQL AS 'SELECT $1'",
			"CREATE AGGREGATE "+ns+".vis_agg(integer) (SFUNC=pg_catalog.int4pl, STYPE=integer, INITCOND='0')",
		} { run(fixture.Exec(q, 0)) }
	}
	for _, q := range []string{
		"CREATE FUNCTION vex_b.vis_fn(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'",
		"CREATE FUNCTION vex_b.op(bigint,bigint) RETURNS boolean LANGUAGE SQL AS 'SELECT $1=$2'",
		"CREATE OPERATOR vex_b.## (LEFTARG=bigint, RIGHTARG=bigint, FUNCTION=vex_b.op)",
		"CREATE OPERATOR CLASS vex_b.vis_class FOR TYPE integer USING hash AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer), FUNCTION 2 pg_catalog.hashint4extended(integer,bigint)",
		"CREATE OPERATOR FAMILY vex_b.vis_family USING hash",
		"ALTER OPERATOR FAMILY vex_b.vis_family USING btree ADD FUNCTION 1 (integer,integer) pg_catalog.btint4cmp(integer,integer)",
		"CREATE PROCEDURE vex_b.vis_proc(text) LANGUAGE SQL AS 'SELECT $1'",
		"CREATE AGGREGATE vex_b.vis_agg(bigint) (SFUNC=pg_catalog.int8pl, STYPE=bigint, INITCOND='0')",
	} { run(fixture.Exec(q, 0)) }
}

func executionVisibilityScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "vex_a", "vex_b")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer conn.Close()
			run := func(s fixture.Step) { t.Helper(); s.CheckMetadata = fixture.VisibilityBoolMetadata; fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			executionVisibilityInstall(run)
			body(t, p, backend, conn, run)
		})
	}
}

func executionVisibilityDecision(fn, arg string, want any) fixture.Step {
	return fixture.Select("SELECT pg_catalog."+fn+"("+arg+")", []string{fn}, []string{"BOOL"}, [][]any{{want}})
}

func executionVisibilityOrders(run func(fixture.Step), kinds ...int) {
	objects := executionVisibilityObjects()
	for _, path := range []string{"vex_a,vex_b", "vex_b,vex_a", "public"} {
		run(fixture.Exec("SET search_path="+path, 0))
		for _, kind := range kinds {
			for i, index := range []int{kind, kind+4, kind+8} {
				want := path != "public" && (i == 2 || (i == 0 && path == "vex_a,vex_b") || (i == 1 && path == "vex_b,vex_a"))
				s := executionVisibilityDecision(objects[index].fn, objects[index].identity, want)
				run(s); run(fixture.Prepared(s))
			}
		}
	}
}

func TestReferenceVisibilityExecutionTyped(t *testing.T) {
	executionVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("SET search_path=vex_a,vex_b", 0))
		for kind, fn := range executionVisibilityNames {
			facts := fixture.Select("SELECT oid::bigint,pronargs,proargtypes::text,prorettype::bigint,proisstrict,provolatile::text,proparallel::text,prosrc,procost,prolang::bigint FROM pg_proc WHERE pronamespace=11 AND proname='"+fn+"' ORDER BY oid",
				[]string{"oid", "pronargs", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "prosrc", "procost", "prolang"},
				[]string{"INT8", "INT2", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "TEXT", "FLOAT4", "INT8"},
				[][]any{{[]int64{2081, 2082, 2083, 3829}[kind], int64(1), "26", int64(16), true, "s", "s", fn, float64(10), int64(12)}})
			run(facts); run(fixture.Prepared(facts))
			obj := executionVisibilityObjects()[kind]
			var id int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT ("+obj.identity+")::bigint").Scan(&id); err != nil || id == 0 { t.Fatal(id, err) }
			table, column := []string{"pg_proc", "pg_operator", "pg_opclass", "pg_opfamily"}[kind], "oid"
			run(fixture.Select("SELECT count(*) FROM "+table+" WHERE "+column+"=4294967295::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			query := "SELECT pg_catalog."+fn+"($1)"
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil { t.Fatal(err) }
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET search_path=vex_a,vex_b", 0))
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil { t.Fatal(err) }
			for _, value := range []any{nil, int64(0), int64(4294967295), id} {
				var want any
				if value == id { want = true }
				s := executionVisibilityDecision(fn, "$1", want); s.Args = []any{value}
				run(s); run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}, b.Recorder, s)
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(s))
				run(executionVisibilityDecision(fn, "NULL::integer", nil))
				rows, err := stmt.QueryContext(p.Ctx, value)
				if err != nil { t.Fatal(err) }
				fixture.VisibilityBoolMetadata(t, rows)
				if !rows.Next() { t.Fatal("missing nullable row") }
				var nullable sql.NullBool
				if err := rows.Scan(&nullable); err != nil || nullable.Valid != (want != nil) { t.Fatal(nullable, err) }
				if err := rows.Close(); err != nil { t.Fatal(err) }
			}
			if err := stmt.Close(); err != nil { t.Fatal(err) }
			if err := tx.Rollback(); err != nil { t.Fatal(err) }
			run(executionVisibilityDecision(fn, fmt.Sprint(id)+"::oid", true))
			run(executionVisibilityDecision(fn, fmt.Sprint(id)+"::integer", true))
			run(fixture.Exec("CREATE TABLE public.vex_inputs(id OID)", 0))
			run(fixture.Select("SELECT "+fn+"(id) AS visible FROM public.vex_inputs", []string{"visible"}, []string{"BOOL"}, [][]any{}))
			run(fixture.Exec("INSERT INTO public.vex_inputs VALUES($1),($1),(0),(NULL)", 4, id))
			s := fixture.Select("SELECT "+fn+"(id) AS visible FROM public.vex_inputs", []string{"visible"}, []string{"BOOL"}, [][]any{{true},{true},{nil},{nil}}); s.Bag = true
			run(s); run(fixture.Prepared(s))
			s = fixture.Select("SELECT "+fn+"(id) AS visible FROM public.vex_inputs WHERE "+fn+"(id)", []string{"visible"}, []string{"BOOL"}, [][]any{{true},{true}}); s.Bag = true
			run(s); run(fixture.Prepared(s))
			run(fixture.Exec("DROP TABLE public.vex_inputs", 0))
			dropped, err := conn.PrepareContext(p.Ctx, query)
			if err != nil { t.Fatal(err) }
			run(fixture.Exec(obj.drop, 0))
			run(executionVisibilityDecision(fn, fmt.Sprint(id)+"::oid", nil))
			missing := executionVisibilityDecision(fn, "$1", nil)
			missing.Args, missing.CheckMetadata = []any{id}, fixture.VisibilityBoolMetadata
			run(missing); run(fixture.Prepared(missing))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: dropped, SQL: query}, b.Recorder, missing)
			if err := dropped.Close(); err != nil { t.Fatal(err) }
		}
	})
}

func TestReferenceVisibilityExecutionRoutines(t *testing.T) {
	executionVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		executionVisibilityOrders(run, 0)
		for _, path := range []string{"vex_a,vex_b", "vex_b,vex_a"} {
			run(fixture.Exec("SET search_path="+path, 0))
			for _, name := range []string{"vis_proc", "vis_agg"} {
				for _, ns := range []string{"vex_a", "vex_b"} {
					run(executionVisibilityDecision(executionVisibilityNames[0], "'"+ns+"."+name+"(integer)'::regprocedure", (ns == "vex_a") == (path == "vex_a,vex_b")))
				}
				typ := "text"; if name == "vis_agg" { typ = "bigint" }
				run(executionVisibilityDecision(executionVisibilityNames[0], "'vex_b."+name+"("+typ+")'::regprocedure", true))
			}
		}
		run(fixture.Exec("CREATE FUNCTION vex_a.abs(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", 0))
		for _, path := range []string{"vex_a", "vex_a,pg_catalog"} {
			run(fixture.Exec("SET search_path="+path, 0))
			run(executionVisibilityDecision(executionVisibilityNames[0], "'pg_catalog.abs(integer)'::regprocedure", path == "vex_a"))
			run(executionVisibilityDecision(executionVisibilityNames[0], "'vex_a.abs(integer)'::regprocedure", path != "vex_a"))
			run(executionVisibilityDecision(executionVisibilityNames[0], "'pg_catalog.abs(bigint)'::regprocedure", true))
			run(executionVisibilityDecision(executionVisibilityNames[0], "'pg_catalog.pg_get_aios()'::regprocedure", true))
			run(executionVisibilityDecision(executionVisibilityNames[0], "'pg_catalog.sum(integer)'::regprocedure", true))
		}
		var id int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'vex_a.vis_fn(integer)'::regprocedure::oid::bigint").Scan(&id); err != nil { t.Fatal(err) }
		run(fixture.Exec("ALTER FUNCTION vex_a.vis_fn(integer) RENAME TO renamed_fn", 0))
		run(fixture.Exec("ALTER FUNCTION vex_a.renamed_fn(integer) SET SCHEMA vex_b", 0))
		run(executionVisibilityDecision(executionVisibilityNames[0], fmt.Sprint(id)+"::oid", false))
		run(fixture.Exec("SET search_path=vex_b", 0))
		run(executionVisibilityDecision(executionVisibilityNames[0], fmt.Sprint(id)+"::oid", true))
	})
}

func TestReferenceVisibilityExecutionOperators(t *testing.T) {
	executionVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{
			"CREATE FUNCTION vex_a.asym(integer,bigint) RETURNS boolean LANGUAGE SQL AS 'SELECT true'",
			"CREATE FUNCTION vex_b.asym(bigint,integer) RETURNS boolean LANGUAGE SQL AS 'SELECT true'",
			"CREATE FUNCTION vex_b.unary(integer) RETURNS boolean LANGUAGE SQL AS 'SELECT true'",
			"CREATE OPERATOR vex_a.## (LEFTARG=integer, RIGHTARG=bigint, FUNCTION=vex_a.asym)",
			"CREATE OPERATOR vex_b.## (LEFTARG=bigint, RIGHTARG=integer, FUNCTION=vex_b.asym)",
			"CREATE OPERATOR vex_b.## (RIGHTARG=integer, FUNCTION=vex_b.unary)",
		} { run(fixture.Exec(q, 0)) }
		executionVisibilityOrders(run, 1)
		for _, path := range []string{"vex_a,vex_b", "vex_b,vex_a"} {
			run(fixture.Exec("SET search_path="+path, 0))
			for _, identity := range []string{"vex_a.##(integer,bigint)", "vex_b.##(bigint,integer)", "vex_b.##(NONE,integer)"} {
				run(executionVisibilityDecision(executionVisibilityNames[1], "'"+identity+"'::regoperator", true))
			}
		}
	})
}

func TestReferenceVisibilityExecutionClassesFamilies(t *testing.T) {
	executionVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		executionVisibilityOrders(run, 2, 3)
	})
}
