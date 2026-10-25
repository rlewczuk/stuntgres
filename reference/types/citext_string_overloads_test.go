package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceCitextStringOverloads(t *testing.T) {
 citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 // Ordinary siblings exist without installation.
 for _,installed:=range []bool{false,true} {
  if installed {citextInstall(t,p,b,c)}
  for _,tc:=range []struct{expr,typ string;v any}{
   {"replace('AbA','a','X')","TEXT","AbA"},{"strpos('ABC','a')","INT4",int64(0)},
   {"split_part('aTb','t',2)","TEXT",""},{"translate('ABC','a','x')","TEXT","ABC"},
  } {citextStringRoutes(t,p,b,c,citextStringValue("SELECT "+tc.expr+" AS v",tc.typ,tc.v))}
 }
 for _,tc:=range []struct{expr,typ string;v any}{
  {"replace('AbA'::citext,'a','X')","TEXT","XbX"},
  {"replace('AbA','a'::citext,'X')","TEXT","XbX"},
  {"replace('AbA','a','X'::citext)","TEXT","XbX"},
  {"strpos('ABC'::citext,'a')","INT4",int64(1)},
  {"strpos('ABC','a'::citext)","INT4",int64(1)},
  {"split_part('aTb'::citext,'t',2)","TEXT","b"},
  {"split_part('aTb','t'::citext,2::smallint)","TEXT","b"},
  {"translate('ABC'::citext,'a','x')","TEXT","xBC"},
  {"translate('ABC','a'::citext,'x')","TEXT","xBC"},
  {"translate('ABC'::citext,'a','x'::text)","TEXT","xBC"},
  {"translate('ABC','a','x'::citext)","TEXT","ABC"},
  {"replace('AbA'::citext,'a','X'::text)","TEXT","AbA"},
  {"replace('AbA'::text,'a'::citext,'X')","TEXT","AbA"},
  {"replace('AbA'::citext,'a'::text,'X')","TEXT","AbA"},
  {"replace('AbA'::citext,'a','X'::varchar)","TEXT","AbA"},
  {"strpos('ABC'::citext,'a'::text)","INT4",int64(0)},
  {"strpos('ABC'::varchar,'a'::citext)","INT4",int64(0)},
  {"split_part('aTb'::citext,'t'::text,2)","TEXT",""},
  {"split_part('aTb'::varchar,'t'::citext,2)","TEXT",""},
  {"translate('ABC'::citext,'a'::varchar,'x')","TEXT","ABC"},
  {"translate('ABC'::text,'a'::citext,'x')","TEXT","ABC"},
  {"pg_catalog.replace('AbA'::citext,'a','X')","TEXT","AbA"},
  {"pg_catalog.strpos('ABC'::citext,'a')","INT4",int64(0)},
  {"pg_catalog.split_part('aTb'::citext,'t',2)","TEXT",""},
  {"pg_catalog.translate('ABC'::citext,'a','x')","TEXT","ABC"},
  {"public.replace('AbA'::citext,'a','X')","TEXT","XbX"},
  {"public.strpos('ABC'::citext,'a')","INT4",int64(1)},
  {"public.split_part('aTb'::citext,'t',2)","TEXT","b"},
  {"public.translate('ABC'::citext,'a','x'::text)","TEXT","xBC"},
 } {citextStringRoutes(t,p,b,c,citextStringValue("SELECT "+tc.expr+" AS v",tc.typ,tc.v))}
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET search_path=pg_catalog",0))
 defer c.ExecContext(p.Ctx,"RESET search_path")
 for _,tc:=range []struct{expr,typ string;v any}{
  {"replace('AbA'::public.citext,'a','X')","TEXT","AbA"},
  {"strpos('ABC'::public.citext,'a')","INT4",int64(0)},
  {"split_part('aTb'::public.citext,'t',2)","TEXT",""},
  {"translate('ABC'::public.citext,'a','x')","TEXT","ABC"},
  {"public.replace('AbA'::public.citext,'a','X')","TEXT","XbX"},
  {"public.strpos('ABC'::public.citext,'a')","INT4",int64(1)},
  {"public.split_part('aTb'::public.citext,'t',2)","TEXT","b"},
  {"public.translate('ABC'::public.citext,'a','x')","TEXT","xBC"},
 } {citextStringRoutes(t,p,b,c,citextStringValue("SELECT "+tc.expr+" AS v",tc.typ,tc.v))}
 })
}
