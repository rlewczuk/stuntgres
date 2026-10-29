package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTSTemplateFeatureSmoke(t *testing.T) {
	extensionCase(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,r *fixture.Recorder,pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE TABLE public.template_smoke_sentinel(v int)",0))
		run(fixture.Exec("INSERT INTO public.template_smoke_sentinel VALUES(42)",1))
		templateDefinition(t,p,c,r,"public.template_smoke")
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_smoke_dict (TEMPLATE=public.template_smoke,STOPWORDS=english)",0))
		templateTokens(t,p,c,r,pg,"public.template_smoke_dict")
		before := templateIdentity(t,p,c,"template_smoke")
		run(fixture.Exec("CREATE SCHEMA ext_locked",0))
		run(fixture.Exec("ALTER TEXT SEARCH TEMPLATE public.template_smoke RENAME TO template_smoke_renamed",0))
		run(fixture.Exec("ALTER TEXT SEARCH TEMPLATE public.template_smoke_renamed SET SCHEMA ext_locked",0))
		after := templateIdentity(t,p,c,"template_smoke_renamed")
		if before.oid != after.oid || before.init != after.init || before.lexize != after.lexize { t.Fatal("smoke relabel changed identity",before,after) }
		templateLink(t,p,c,r,"template_smoke_dict",before.oid)
		templateDiagnostic(t,p,c,r,"CREATE TEXT SEARCH TEMPLATE public.template_smoke_bad (INIT=pg_catalog.bthandler,LEXIZE=pg_catalog.dsimple_lexize)","42P17","function pg_catalog.bthandler(internal) should return type internal")
		templateDiagnostic(t,p,c,r,"CREATE TEXT SEARCH TEMPLATE public.template_smoke_bad (LEXIZE=pg_catalog.brin_minmax_add_value)","42P17","function pg_catalog.brin_minmax_add_value(internal, internal, internal, internal) should return type internal")
		templateAbsent(t,p,c,r,"template_smoke_bad")
		fixture.TSTemplateRestrict(t,p,c,r,"ext_locked.template_smoke_renamed","template_smoke_dict")
		templateTokens(t,p,c,r,pg,"public.template_smoke_dict")
		if !pg {
			dump := fixture.ExtensionDump(t,p.Ctx,p.Native)
			old,err := c.QueryContext(p.Ctx,"SELECT ts_lexize('public.template_smoke_dict','WORD') AS lexemes")
			if err != nil { t.Fatal(err) }
			defer old.Close()
			types,err := old.ColumnTypes()
			if err != nil || len(types)!=1 || types[0].DatabaseTypeName()!="_TEXT" { t.Fatal("retained metadata",types,err) }
			if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
			templateAbsent(t,p,c,r,"template_smoke_renamed")
			if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump)); err != nil { t.Fatal(err) }
			templateLink(t,p,c,r,"template_smoke_dict",templateIdentity(t,p,c,"template_smoke_renamed").oid)
			templateTokens(t,p,c,r,false,"public.template_smoke_dict")
			if fixture.ExtensionDump(t,p.Ctx,p.Native) != dump { t.Fatal("smoke redump changed") }
			if !old.Next() { t.Fatal("retained smoke row",old.Err()) }
			var value string
			if err := old.Scan(&value); err != nil || value!="{word}" { t.Fatal(value,err) }
			if old.Next() || old.Err()!=nil { t.Fatal("retained smoke cardinality",old.Err()) }
		}
		run(fixture.Exec("DROP TEXT SEARCH TEMPLATE ext_locked.template_smoke_renamed CASCADE",0))
		templateAbsent(t,p,c,r,"template_smoke_renamed")
		run(fixture.Select("SELECT count(*) FROM pg_ts_dict WHERE dictname='template_smoke_dict'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		templateDiagnostic(t,p,c,r,"DROP TEXT SEARCH DICTIONARY public.template_smoke_dict","42704",`text search dictionary "public.template_smoke_dict" does not exist`)
		run(fixture.Select("SELECT v FROM public.template_smoke_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(42)}}))
	})
}
