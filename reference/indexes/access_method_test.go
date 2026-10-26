package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

// Methods are global: the fixture's public-schema reset cannot retire them.
// Register before NewScenario so all case borrowers close before admin cleanup.
func amBackends(t *testing.T, names []string, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	clean := fixture.AccessMethodReferenceCleanup(t, names)
	p := fixture.NewScenario(t)
	clean()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func amError(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, query string, want fixture.Diagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(query, want)
		s.Prepared, s.ErrorStage = prepared, "execute"
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
	}
}

func amCount(query string, n int64, args ...any) fixture.Step {
	return fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{n}}, args...)
}

func amCreateClass(method, family, class string) string {
	return `CREATE OPERATOR CLASS public.` + class + ` DEFAULT FOR TYPE integer USING ` + method + ` FAMILY public.` + family + ` AS ` + classItems
}

func amProperties(query string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(query, []string{"can_order", "can_unique", "can_multi_col", "can_exclude", "can_include", "unknown"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, rows, args...)
}

const amPropertySQL = `SELECT pg_indexam_has_property(oid,'can_order') AS can_order,pg_indexam_has_property(oid,'can_unique') AS can_unique,pg_indexam_has_property(oid,'can_multi_col') AS can_multi_col,pg_indexam_has_property(oid,'can_exclude') AS can_exclude,pg_indexam_has_property(oid,'can_include') AS can_include,pg_indexam_has_property(oid,'unknown') AS unknown FROM pg_am WHERE amname=$1`

func TestReferenceAccessMethodDefinitions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"am_def_reader"})
	amBackends(t, []string{"am_def_index", "am_def_table", "Am Déf"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		const meta = `SELECT oid,amname,amhandler,amtype FROM pg_am WHERE amname=$1`
		held := classHeld(t, p, conn, meta)
		checkAbsent := func(name string) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(meta, []string{"oid", "amname", "amhandler", "amtype"}, []string{"OID", "NAME", "REGPROC", "CHAR"}, nil, name))
		}
		for i, tc := range []struct {
			name, token, kind, handler string
			handlerOID                 int64
		}{
			{"am_def_index", "AM_DEF_INDEX", "INDEX", "bthandler", 330},
			{"am_def_table", "am_def_table", "TABLE", "heap_tableam_handler", 3},
			{"Am Déf", `"Am Déf"`, "INDEX", "bthandler", 330},
		} {
			q := `CREATE ACCESS METHOD ` + tc.token + ` TYPE ` + tc.kind + ` HANDLER pg_catalog.` + tc.handler
			ddl := classHeld(t, p, conn, q)
			checkAbsent(tc.name) // quiet description must not publish a method
			s := fixture.Exec(q, 0)
			if i == 1 {
				fixture.RunStep(t, p.Ctx, ddl, b.Recorder, s)
			} else {
				run(s)
			}
			oid := classRoundtripIdentity(t, p.Ctx, conn, `SELECT oid::bigint FROM pg_am WHERE amname=$1`, tc.name)
			if oid == 403 || oid == 2 {
				t.Fatalf("custom method reused builtin identity %d", oid)
			}
			kind := "i"
			if tc.kind == "TABLE" {
				kind = "t"
			}
			var rawOID any = oid
			if b.PG {
				rawOID = strconv.FormatInt(oid, 10)
			}
			m := fixture.Select(meta, []string{"oid", "amname", "amhandler", "amtype"}, []string{"OID", "NAME", "REGPROC", "CHAR"}, [][]any{{rawOID, tc.name, tc.handler, kind}}, tc.name)
			classRoutes(t, p, b, conn, m)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, m)
			run(fixture.Select(`SELECT amhandler::oid::bigint FROM pg_am WHERE amname=$1`, []string{"amhandler"}, []string{"INT8"}, [][]any{{tc.handlerOID}}, tc.name))
			amError(t, p, b, conn, `CREATE ACCESS METHOD `+tc.token+` TYPE INDEX HANDLER am_def_missing`, fixture.Diagnostic{Code: "42710", Message: `access method "` + tc.name + `" already exists`})
			// Exercise both direct and genuinely held CREATE for each kind.
			run(fixture.Exec(`DROP ACCESS METHOD `+tc.token, 0))
			if i == 1 {
				run(fixture.Exec(q, 0))
			} else {
				fixture.RunStep(t, p.Ctx, ddl, b.Recorder, fixture.Exec(q, 0))
			}
			run(amCount(`SELECT count(*) FROM pg_am WHERE amname=$1`, 1, tc.name))
		}
		// Released pg_proc.dat inherits strict=true/parallel-safe for these
		// native handlers. Utility capability metadata does not make them
		// scalar-callable or manufacture a PostgreSQL internal return pointer.
		classRoutes(t, p, b, conn, fixture.Select(
			`SELECT oid::bigint,proname,pronamespace::bigint,pronargs,prorettype::bigint,provolatile,proisstrict,proparallel,prokind FROM pg_proc WHERE oid IN (3,330,331,332,334) ORDER BY oid`,
			[]string{"oid", "proname", "pronamespace", "pronargs", "prorettype", "provolatile", "proisstrict", "proparallel", "prokind"},
			[]string{"INT8", "NAME", "INT8", "INT2", "INT8", "CHAR", "BOOL", "CHAR", "CHAR"},
			[][]any{
				{int64(3), "heap_tableam_handler", int64(11), int64(1), int64(269), "v", true, "s", "f"},
				{int64(330), "bthandler", int64(11), int64(1), int64(325), "v", true, "s", "f"},
				{int64(331), "hashhandler", int64(11), int64(1), int64(325), "v", true, "s", "f"},
				{int64(332), "gisthandler", int64(11), int64(1), int64(325), "v", true, "s", "f"},
				{int64(334), "spghandler", int64(11), int64(1), int64(325), "v", true, "s", "f"},
			}))
		props := classHeld(t, p, conn, amPropertySQL)
		for _, name := range []string{"am_def_index", "am_def_table"} {
			values := [][]any{{true, true, true, true, true, nil}}
			if name == "am_def_table" {
				values = [][]any{{nil, nil, nil, nil, nil, nil}}
			}
			s := amProperties(amPropertySQL, values, name)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, props, b.Recorder, s)
		}
		run(fixture.Select(`SELECT pg_indexam_has_property(0,'can_order') AS missing`, []string{"missing"}, []string{"BOOL"}, [][]any{{nil}}))
		run(fixture.Select(`SELECT type,schema,name,identity FROM pg_identify_object('pg_am'::regclass,(SELECT oid FROM pg_am WHERE amname='am_def_index'),0)`, []string{"type", "schema", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"access method", nil, "am_def_index", "am_def_index"}}))
		run(fixture.Select(`SELECT classid='pg_am'::regclass,objid=(SELECT oid FROM pg_am WHERE amname='am_def_index'),objsubid FROM pg_get_object_address('access method',ARRAY['am_def_index'],ARRAY[]::text[])`, []string{"?column?", "?column?", "objsubid"}, []string{"BOOL", "BOOL", "INT4"}, [][]any{{true, true, int64(0)}}))
		run(fixture.Exec(`CREATE ROLE am_def_reader`, 0))
		run(fixture.Exec(`SET ROLE am_def_reader`, 0))
		run(amCount(`SELECT count(*) FROM pg_am WHERE amname IN ('am_def_index','am_def_table','Am Déf')`, 3))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`SET search_path=pg_catalog`, 0))
		amError(t, p, b, conn, `CREATE ACCESS METHOD am_def_index TYPE INDEX HANDLER bthandler`, fixture.Diagnostic{Code: "42710", Message: `access method "am_def_index" already exists`})
		run(fixture.Exec(`RESET search_path`, 0))
		run(fixture.Exec(`CREATE TABLE am_def_rows(n integer)`, 0))
		run(fixture.Select(`SELECT relam::bigint FROM pg_class WHERE oid='am_def_rows'::regclass`, []string{"relam"}, []string{"INT8"}, [][]any{{int64(2)}}))
		for i, token := range []string{"am_def_index", "am_def_table", `"Am Déf"`} {
			q := `DROP ACCESS METHOD ` + token
			drop := classHeld(t, p, conn, q)
			run(amCount(`SELECT count(*) FROM pg_am WHERE amname IN ('am_def_index','am_def_table','Am Déf')`, int64(3-i)))
			if i == 1 {
				fixture.RunStep(t, p.Ctx, drop, b.Recorder, fixture.Exec(q, 0))
			} else {
				run(fixture.Exec(q, 0))
			}
		}
		checkAbsent("am_def_index")
		fixture.RunStep(t, p.Ctx, props, b.Recorder, amProperties(amPropertySQL, nil, "am_def_index"))
		run(fixture.Exec(`CREATE ACCESS METHOD am_def_index TYPE TABLE HANDLER heap_tableam_handler`, 0))
		fixture.RunStep(t, p.Ctx, props, b.Recorder, amProperties(amPropertySQL, [][]any{{nil, nil, nil, nil, nil, nil}}, "am_def_index"))
		run(fixture.Exec(`DROP ACCESS METHOD am_def_index`, 0))
		want := int64(6)
		if b.PG {
			want = 7
		}
		run(amCount(`SELECT count(*) FROM pg_am`, want))
	})
}
