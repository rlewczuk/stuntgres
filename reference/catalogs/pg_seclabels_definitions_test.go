package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Quote only identifier tokens in the independent released/source-derived
// expectations, never in native output. SQL literals and already quoted char
// identifiers are opaque, except the known regclass constant's identifier.
func pgSeclabelsQuotedExpectation(source string) string {
	source=strings.ReplaceAll(source,"'pg_largeobject'",`'"pg_largeobject"'`)
	var out strings.Builder
	for i:=0;i<len(source); {
		if source[i]=='\'' || source[i]=='"' {
			quote,start:=source[i],i;i++
			for i<len(source){if source[i]==quote{i++;if i<len(source)&&source[i]==quote{i++;continue};break};i++}
			out.WriteString(source[start:i]);continue
		}
		if source[i]>='a' && source[i]<='z' || source[i]=='_' {
			j:=i+1;for j<len(source)&&(source[j]>='a'&&source[j]<='z'||source[j]>='0'&&source[j]<='9'||source[j]=='_'){j++}
			out.WriteString(`"`+source[i:j]+`"`);i=j;continue
		}
		out.WriteByte(source[i]);i++
	}
	return out.String()
}

func TestReferencePGSeclabelsDefinitions(t *testing.T) {
	pgSeclabelsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		t.Cleanup(func(){
			c.ExecContext(p.Ctx,"RESET search_path");c.ExecContext(p.Ctx,"RESET quote_all_identifiers")
			c.ExecContext(p.Ctx,"DROP TABLE IF EXISTS public.pg_seclabel")
			c.ExecContext(p.Ctx,"DROP FUNCTION IF EXISTS public.quote_ident(text)")
		})
		check:=func(shadow,quoteAll bool){
			defaultDef,prettyDef:=pgSeclabelsDefaultDefinition,pgSeclabelsPrettyDefinition
			if shadow {
				// These transformations apply only to the independent source
				// constants, not to observed backend definitions.
				references:=strings.NewReplacer("pg_seclabel l","pg_catalog.pg_seclabel l","quote_ident(","pg_catalog.quote_ident(")
				defaultDef,prettyDef=references.Replace(defaultDef),references.Replace(prettyDef)
			}
			if quoteAll {defaultDef,prettyDef=pgSeclabelsQuotedExpectation(defaultDef),pgSeclabelsQuotedExpectation(prettyDef)}
			for _,s:=range []fixture.Step{
				fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_seclabels'::regclass,false) AS nonpretty,pg_get_viewdef('pg_catalog.pg_seclabels'::regclass,true) AS pretty",[]string{"nonpretty","pretty"},[]string{"TEXT","TEXT"},[][]any{{defaultDef,prettyDef}}),
				fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND viewname='pg_seclabels'",[]string{"definition"},[]string{"TEXT"},[][]any{{defaultDef}}),
				fixture.Select("SELECT view_definition::text FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='pg_seclabels'",[]string{"view_definition"},[]string{"TEXT"},[][]any{{defaultDef}}),
			}{fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,pgSeclabelsMetadata(b,pgSeclabelsSelect))
		}
		check(false,false)
		pgSeclabelExec(t,p,b,c,"SET quote_all_identifiers=on");check(false,true)
		pgSeclabelExec(t,p,b,c,"SET quote_all_identifiers=off","CREATE TABLE public.pg_seclabel(marker INT)","CREATE FUNCTION public.quote_ident(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'","SET search_path=public,pg_catalog")
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO public.pg_seclabel VALUES(7)",1))
		check(true,false)
		pgSeclabelExec(t,p,b,c,"SET quote_all_identifiers=on");check(true,true)
		pgSeclabelExec(t,p,b,c,"SET quote_all_identifiers=off")
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT marker FROM public.pg_seclabel",[]string{"marker"},[]string{"INT4"},[][]any{{int64(7)}}))
		pgSeclabelExec(t,p,b,c,"RESET search_path","RESET quote_all_identifiers")
		check(false,false)
	})
}
