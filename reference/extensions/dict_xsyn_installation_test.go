package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func xsynInstallation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema string) {
	t.Helper()
	xsynSelect(t, p, b, c, "SELECT e.extversion,e.extrelocatable,n.nspname,obj_description(e.oid,'pg_extension') AS comment,r.rolname=current_user AS installer FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='dict_xsyn'", []string{"extversion", "extrelocatable", "nspname", "comment", "installer"}, []string{"TEXT", "BOOL", "NAME", "TEXT", "BOOL"}, [][]any{{"1.0", true, schema, "text search dictionary template for extended synonym processing", true}})
	xsynSelect(t, p, b, c, "SELECT p.proname,p.pronargs,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proparallel,p.probin,p.prosrc,l.lanname,p.proowner=e.extowner AS owned FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang JOIN pg_extension e ON e.extnamespace=p.pronamespace WHERE e.extname='dict_xsyn' AND (p.proname='dxsyn_init' OR p.proname='dxsyn_lexize') ORDER BY p.proname", []string{"proname", "pronargs", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "probin", "prosrc", "lanname", "owned"}, []string{"NAME", "INT2", "TEXT", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "NAME", "BOOL"}, [][]any{{"dxsyn_init", int64(1), "2281", int64(2281), true, "v", "u", "$libdir/dict_xsyn", "dxsyn_init", "c", true}, {"dxsyn_lexize", int64(4), "2281 2281 2281 2281", int64(2281), true, "v", "u", "$libdir/dict_xsyn", "dxsyn_lexize", "c", true}})
	xsynSelect(t, p, b, c, "SELECT t.tmplname,t.tmplinit=$2::regprocedure AS init,t.tmpllexize=$3::regprocedure AS lexize,d.dictname,d.dictinitoption,obj_description(d.oid,'pg_ts_dict') AS comment,d.dictowner=e.extowner AS owned FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate JOIN pg_extension e ON e.extnamespace=d.dictnamespace WHERE d.oid=$1::regdictionary::oid", []string{"tmplname", "init", "lexize", "dictname", "dictinitoption", "comment", "owned"}, []string{"NAME", "BOOL", "BOOL", "NAME", "TEXT", "TEXT", "BOOL"}, [][]any{{"xsyn_template", true, true, "xsyn", nil, "eXtended synonym dictionary", true}}, schema+".xsyn", schema+".dxsyn_init(internal)", schema+".dxsyn_lexize(internal,internal,internal,internal)")
	xsynSelect(t, p, b, c, "SELECT dep.classid::bigint,count(*) FROM pg_depend dep JOIN pg_extension e ON e.oid=dep.refobjid WHERE dep.refclassid=3079 AND e.extname='dict_xsyn' AND dep.deptype='e' GROUP BY dep.classid ORDER BY dep.classid", []string{"classid", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(1255), int64(2)}, {int64(3600), int64(1)}, {int64(3764), int64(1)}})
	xsynSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_ts_dict d ON d.dicttemplate=dep.refobjid WHERE d.oid=$1::regdictionary::oid AND dep.classid=3600 AND dep.objid=d.oid AND dep.refclassid=3764 AND dep.deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, schema+".xsyn")
	xsynSelect(t, p, b, c, "SELECT count(*) FROM pg_depend dep JOIN pg_ts_template t ON t.oid=dep.objid JOIN pg_ts_dict d ON d.dicttemplate=t.oid WHERE d.oid=$1::regdictionary::oid AND dep.classid=3764 AND dep.refclassid=1255 AND (dep.refobjid=t.tmplinit::oid OR dep.refobjid=t.tmpllexize::oid) AND dep.deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, schema+".xsyn")
	xsynLexemes(t, p, b, c, nil, schema+".xsyn", "supernova")
}

func TestReferenceDictXsynInstallation(t *testing.T) {
	t.Run("lifecycle", func(t *testing.T) {
		xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			check := func(installed any, enabled bool) {
				xsynSelect(t, p, b, c, "SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='dict_xsyn'", []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"dict_xsyn", "1.0", installed, "text search dictionary template for extended synonym processing"}})
				xsynSelect(t, p, b, c, "SELECT version,installed,superuser,trusted,relocatable,schema,requires FROM pg_available_extension_versions WHERE name='dict_xsyn' ORDER BY version", []string{"version", "installed", "superuser", "trusted", "relocatable", "schema", "requires"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "_NAME"}, [][]any{{"1.0", enabled, true, false, true, nil, nil}})
			}
			absent := func() {
				check(nil, false)
				xsynSelect(t, p, b, c, "SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND (proname='dxsyn_init' OR proname='dxsyn_lexize')", []string{"proname"}, []string{"NAME"}, nil)
				xsynSelect(t, p, b, c, "SELECT tmplname FROM pg_ts_template WHERE tmplnamespace='public'::regnamespace AND tmplname='xsyn_template'", []string{"tmplname"}, []string{"NAME"}, nil)
				xsynSelect(t, p, b, c, "SELECT dictname FROM pg_ts_dict WHERE dictnamespace='public'::regnamespace AND dictname='xsyn'", []string{"dictname"}, []string{"NAME"}, nil)
			}
			check(nil, false)
			xsynExec(t, p, b, c, "CREATE ROLE xsyn_denied", 0)
			xsynExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO xsyn_denied", 0)
			xsynExec(t, p, b, c, "SET SESSION AUTHORIZATION xsyn_denied", 0)
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("CREATE EXTENSION dict_xsyn SCHEMA public", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "dict_xsyn"`, Hint: "Must be superuser to create this extension."})
				s.Prepared, s.ErrorStage = prepared, "execute"
				xsynRun(t, p, b, c, s)
			}
			xsynExec(t, p, b, c, "RESET SESSION AUTHORIZATION", 0)
			absent()
			xsynExec(t, p, b, c, "BEGIN", 0)
			xsynExec(t, p, b, c, "CREATE EXTENSION dict_xsyn VERSION '1.0'", 0)
			xsynInstallation(t, p, b, c, "public")
			check("1.0", true)
			xsynExec(t, p, b, c, "ROLLBACK", 0)
			absent()
			xsynExec(t, p, b, c, "BEGIN READ ONLY", 0)
			xsynRun(t, p, b, c, fixture.ImplicitError("CREATE EXTENSION dict_xsyn", fixture.Diagnostic{Code: "25006", Message: "cannot execute CREATE EXTENSION in a read-only transaction"}))
			xsynExec(t, p, b, c, "ROLLBACK", 0)
			xsynRun(t, p, b, c, fixture.ImplicitError("CREATE EXTENSION dict_xsyn VERSION 'missing'", fixture.Diagnostic{Code: "22023", Message: `extension "dict_xsyn" has no installation script nor update path for version "missing"`}))
			xsynExec(t, p, b, c, "CREATE SCHEMA xsyn_addon", 0)
			xsynExec(t, p, b, c, "CREATE EXTENSION dict_xsyn SCHEMA xsyn_addon", 0)
			check("1.0", true)
			xsynInstallation(t, p, b, c, "xsyn_addon")
			xsynRun(t, p, b, c, fixture.ImplicitError("CREATE EXTENSION dict_xsyn", fixture.Diagnostic{Code: "42710", Message: `extension "dict_xsyn" already exists`}))
			var notices []fixture.Diagnostic
			_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("CREATE EXTENSION IF NOT EXISTS dict_xsyn", 0))
			fixture.NoticeWitnesses(t, b.PG, notices, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "dict_xsyn" already exists, skipping`})
			if !b.PG {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				check(nil, false)
				xsynExec(t, p, b, c, "CREATE EXTENSION dict_xsyn", 0)
				xsynInstallation(t, p, b, c, "public")
			}
		})
	})
	t.Run("collision", func(t *testing.T) {
		xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			xsynExec(t, p, b, c, "CREATE TEXT SEARCH TEMPLATE public.xsyn_template (INIT=pg_catalog.dsimple_init,LEXIZE=pg_catalog.dsimple_lexize)", 0)
			xsynExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY public.xsyn_collision_probe (TEMPLATE=public.xsyn_template)", 0)
			const identity = "SELECT oid::bigint,tmplnamespace::bigint,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE tmplname='xsyn_template' AND tmplnamespace='public'::regnamespace"
			before := dictIntRows(t, p, c, identity)
			namespace := dictIntID(t, p, c, "SELECT oid::bigint FROM pg_namespace WHERE nspname='public'")
			// A real template collision occurs after both released callbacks.
			// Whitespace and near-line come from the released 1.0 script.
			want := fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "pg_ts_template_tmplname_index"`, Detail: fmt.Sprintf("Key (tmplname, tmplnamespace)=(xsyn_template, %d) already exists.", namespace), Schema: "pg_catalog", Table: "pg_ts_template", Constraint: "pg_ts_template_tmplname_index", Context: "SQL statement \"CREATE TEXT SEARCH TEMPLATE xsyn_template (\n        LEXIZE = dxsyn_lexize,\n\tINIT   = dxsyn_init\n)\"\nextension script file \"dict_xsyn--1.0.sql\", near line 16"}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("CREATE EXTENSION dict_xsyn", want)
				s.Prepared, s.ErrorStage = prepared, "execute"
				xsynRun(t, p, b, c, s)
				dictIntUnchanged(t, p, c, before, identity)
				xsynSelect(t, p, b, c, "SELECT extname FROM pg_extension WHERE extname='dict_xsyn'", []string{"extname"}, []string{"NAME"}, nil)
				xsynSelect(t, p, b, c, "SELECT proname FROM pg_proc WHERE proname='dxsyn_init' AND pronamespace='public'::regnamespace", []string{"proname"}, []string{"NAME"}, nil)
				xsynSelect(t, p, b, c, "SELECT dictname FROM pg_ts_dict WHERE dictname='xsyn' AND dictnamespace='public'::regnamespace", []string{"dictname"}, []string{"NAME"}, nil)
				xsynSelect(t, p, b, c, "SELECT proname FROM pg_proc WHERE proname='dxsyn_lexize' AND pronamespace='public'::regnamespace", []string{"proname"}, []string{"NAME"}, nil)
				xsynLexemes(t, p, b, c, "{supernova}", "xsyn_collision_probe", "SUPERNOVA")
			}
		})
	})
}
