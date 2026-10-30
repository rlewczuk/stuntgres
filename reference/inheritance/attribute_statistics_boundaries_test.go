package reference_test

import "stuntgres/reference/fixture"

func attributeStatsKindBoundaries(s attributeStatsScenario){
 s.t.Helper()
 s.warnings("'null_frac',.5::real,'most_common_vals','{3}'::integer[],'most_common_freqs','{0.5}'::real[]",attributeStatsWarning("01000",`argument "most_common_vals" has type integer[], expected type text`,""),attributeStatsWarning("22023",`argument "most_common_vals" must be specified when argument "most_common_freqs" is specified`,""))
 s.run(fixture.Select("SELECT null_frac,most_common_vals::text FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='a' AND NOT inherited",[]string{"null_frac","most_common_vals"},[]string{"FLOAT4","TEXT"},[][]any{{float64(.5),"{1,2}"}}))
 *s.notices=nil
 s.run(fixture.Select(attributeStatsRestore("j",false,"'null_frac',.25::real,'histogram_bounds','{1,2}','correlation',.5::real"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{false}}))
 expected:=attributeStatsWarning("22023",`could not determine less-than operator for column "j"`,"Cannot set STATISTIC_KIND_HISTOGRAM or STATISTIC_KIND_CORRELATION.")
 if len(*s.notices)!=1||(*s.notices)[0]!=expected{s.t.Fatalf("less-than warnings %#v",*s.notices)}
 s.run(fixture.Select("SELECT null_frac,histogram_bounds::text,correlation FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='j' AND NOT inherited",[]string{"null_frac","histogram_bounds","correlation"},[]string{"FLOAT4","TEXT","FLOAT4"},[][]any{{float64(.25),nil,nil}}))
 s.exec("ALTER TABLE ast_t ADD COLUMN v varchar(2)")
 s.run(fixture.Select(attributeStatsRestore("v",false,"'histogram_bounds','{a,b}'"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 *s.notices=nil
 s.run(fixture.Select(attributeStatsRestore("v",false,"'avg_width',17::integer,'histogram_bounds','{a,long}'"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{false}}))
 expected=attributeStatsWarning("22001","value too long for type character varying(2)","")
 if len(*s.notices)!=1||(*s.notices)[0]!=expected{s.t.Fatalf("modifier warnings %#v",*s.notices)}
 s.run(fixture.Select("SELECT avg_width,histogram_bounds::text FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='v' AND NOT inherited",[]string{"avg_width","histogram_bounds"},[]string{"INT4","TEXT"},[][]any{{int64(17),"{a,b}"}}))
}
