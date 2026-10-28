package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func templatePersistenceCheck(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	id := templateIdentity(t,p,c,"template_saved")
	if id.init != 3725 || id.lexize != 3726 { t.Fatal("restored stock callbacks",id) }
	templateLink(t,p,c,r,"template_saved_dict",id.oid)
	templateTokens(t,p,c,r,pg,"public.template_saved_dict")
	fixture.RunStep(t,p.Ctx,c,r,fixture.Select("SELECT d='public.template_saved_dict'::regdictionary AS same FROM public.template_saved_rows",[]string{"same"},[]string{"BOOL"},[][]any{{true}}))
	fixture.RunStep(t,p.Ctx,c,r,fixture.Select("SELECT d='public.template_saved_dict'::regdictionary AS same FROM public.template_saved_view",[]string{"same"},[]string{"BOOL"},[][]any{{true}}))
	zero := templateIdentity(t,p,c,"template_zero")
	if zero.init != 0 { t.Fatal("absent INIT acquired state",zero) }
	templateLexemes(t,p,c,r,pg,"SELECT ts_lexize('ext_addon.unaccent','Hôtel') AS lexemes","{Hotel}")
	fixture.RunStep(t,p.Ctx,c,r,fixture.Select("SELECT n.nspname,p.proname FROM pg_ts_template t JOIN pg_proc p ON p.oid=t.tmplinit::oid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE t.tmplname='template_installed'",[]string{"nspname","proname"},[]string{"NAME","NAME"},[][]any{{"ext_locked","template_init_saved"}}))
}

func TestReferenceTSTemplatePersistence(t *testing.T) {
	var emitted string
	extensionCase(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,r *fixture.Recorder,pg bool) {
		run := func(q string) { fixture.RunStep(t,p.Ctx,c,r,fixture.Exec(q,0)) }
		if !pg { templateDefinition(t,p,c,r,"pg_temp.template_source_temp") }
		run("CREATE SCHEMA ext_addon")
		run("CREATE SCHEMA ext_locked")
		templateDefinition(t,p,c,r,"public.template_saved")
		source := templateIdentity(t,p,c,"template_saved")
		run("CREATE TEXT SEARCH TEMPLATE public.template_zero (LEXIZE=pg_catalog.dsimple_lexize)")
		run("CREATE TEXT SEARCH DICTIONARY public.template_saved_dict (TEMPLATE=public.template_saved,STOPWORDS=english)")
		run("CREATE TABLE public.template_saved_rows (d regdictionary DEFAULT 'public.template_saved_dict')")
		fixture.RunStep(t,p.Ctx,c,r,fixture.Exec("INSERT INTO public.template_saved_rows DEFAULT VALUES",1))
		run("CREATE VIEW public.template_saved_view AS SELECT 'public.template_saved_dict'::regdictionary AS d")
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		run("ALTER FUNCTION ext_addon.unaccent_init(internal) RENAME TO template_init_saved")
		run("ALTER FUNCTION ext_addon.template_init_saved(internal) SET SCHEMA ext_locked")
		run("ALTER TEXT SEARCH TEMPLATE ext_addon.unaccent RENAME TO template_installed")
		run("ALTER TEXT SEARCH TEMPLATE ext_addon.template_installed SET SCHEMA ext_locked")
		run("ALTER TEXT SEARCH TEMPLATE pg_catalog.simple RENAME TO template_seed")
		defer c.ExecContext(p.Ctx,"ALTER TEXT SEARCH TEMPLATE pg_catalog.template_seed RENAME TO simple")
		templatePersistenceCheck(t,p,c,r,pg)
		if pg {
			if emitted == "" { t.Fatal("actual native dump required") }
			for _,q := range []string{"DROP VIEW public.template_saved_view","DROP TABLE public.template_saved_rows","DROP TEXT SEARCH TEMPLATE public.template_saved CASCADE","DROP TEXT SEARCH TEMPLATE public.template_zero","DROP EXTENSION unaccent CASCADE","DROP SCHEMA ext_addon CASCADE","DROP SCHEMA ext_locked CASCADE","ALTER TEXT SEARCH TEMPLATE pg_catalog.template_seed RENAME TO simple"} { run(q) }
			if _,err := c.ExecContext(p.Ctx,emitted); err != nil { t.Fatal("replay actual emitted template SQL",err) }
			templatePersistenceCheck(t,p,c,r,true)
			return
		}
		emitted = extensionDump(t,p.Ctx,p.Native)
		if !strings.Contains(emitted,"CREATE TEXT SEARCH TEMPLATE") || strings.Contains(emitted,`CREATE TEXT SEARCH TEMPLATE "pg_catalog"`) || strings.Contains(emitted,"ALTER TEXT SEARCH TEMPLATE \"ext_locked\".\"template_installed\" OWNER") { t.Fatal("template declaration classification",emitted) }
		old,err := c.QueryContext(p.Ctx,"SELECT ts_lexize('public.template_saved_dict','WORD') AS lexemes")
		if err != nil { t.Fatal(err) }
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
		templateAbsent(t,p,c,r,"template_saved")
		if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(emitted)); err != nil { t.Fatal(err) }
		templatePersistenceCheck(t,p,c,r,false)
		if !old.Next() { t.Fatal("retained rows lost",old.Err()) }
		var value string
		if err := old.Scan(&value); err != nil || value != "{word}" { t.Fatal(value,err) }
		if old.Next() || old.Err() != nil { t.Fatal("retained cardinality",old.Err()) }
		if extensionDump(t,p.Ctx,p.Native) != emitted { t.Fatal("template redump is nondeterministic") }
		committed := templateIdentity(t,p,c,"template_saved")
		run("CREATE TABLE public.template_sentinel(v int)")
		fixture.RunStep(t,p.Ctx,c,r,fixture.Exec("INSERT INTO public.template_sentinel VALUES(42)",1))
		preserved := func() {
			t.Helper()
			if templateIdentity(t,p,c,"template_saved") != committed { t.Fatal("failed restore replaced template") }
			templatePersistenceCheck(t,p,c,r,false)
			fixture.RunStep(t,p.Ctx,c,r,fixture.Select("SELECT v FROM public.template_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(42)}}))
		}
		for _,suffix := range []string{"CREATE TEXT SEARCH TEMPLATE public.template_late (INIT=pg_catalog.bthandler,LEXIZE=pg_catalog.dsimple_lexize);","CREATE TEXT SEARCH DICTIONARY public.template_late_dict (TEMPLATE=public.template_saved,STOPWORDS=template_missing_resource);","ALTER TEXT SEARCH DICTIONARY public.template_saved_dict OWNER TO template_missing_owner;"} {
			if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(emitted+suffix)); err == nil { t.Fatal("late failed restore published") }
			preserved()
		}
		ctx,cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx,p.Native,&fixture.CatalogControlCancelReader{Cancel:cancel})
		cancel()
		if !errors.Is(err,context.Canceled) { t.Fatal("reader cancellation",err) }
		preserved()
		// Retain a real same-class TEMP template at the imported source address.
		fresh := fixture.NewFixture(t,false,true)
		fc,err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil { t.Fatal(err) }
		defer fc.Close()
		var collisionName string
		for i:=0; ;i++ {
			collisionName = fmt.Sprintf("template_collision_%d",i)
			templateDefinition(t,fresh,fc,fresh.MemoryRecord,"pg_temp."+collisionName)
			id := templateIdentity(t,fresh,fc,collisionName)
			if id.oid > source.oid { t.Fatalf("same-class collision overshot: %d > %d",id.oid,source.oid) }
			if id.oid == source.oid { break }
		}
		checkCollision := func() {
			t.Helper()
			fixture.RunStep(t,fresh.Ctx,fc,fresh.MemoryRecord,fixture.Select("SELECT oid::bigint,tmplnamespace=pg_my_temp_schema() AS temporary FROM pg_ts_template WHERE tmplname=$1",[]string{"oid","temporary"},[]string{"INT8","BOOL"},[][]any{{source.oid,true}},collisionName))
		}
		checkCollision()
		if err := stuntgres.Restore(fresh.Ctx,fresh.Native,strings.NewReader(emitted)); err != nil { t.Fatal(err) }
		checkCollision()
		imported := templateIdentity(t,fresh,fc,"template_saved")
		if imported.oid == source.oid { t.Fatal("import reused retained TEMP template identity") }
		templatePersistenceCheck(t,fresh,fc,fresh.MemoryRecord,false)
	})
}
