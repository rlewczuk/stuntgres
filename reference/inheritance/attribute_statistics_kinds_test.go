package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceAttributeStatisticsKinds(t *testing.T){withAttributeStats(t,func(s attributeStatsScenario){
 s.exec("CREATE TABLE ast_t(a INT,b INT,arr INT[],doc tsvector,r int4range,m int4multirange)")
 s.restore("'most_common_vals','{1,2}','most_common_freqs','{0.5,0.25}'::real[],'histogram_bounds','{3,5,9}','correlation',.75::real",true)
 s.run(fixture.Select("SELECT most_common_vals::text,most_common_freqs::text,histogram_bounds::text,correlation FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='a' AND NOT inherited",[]string{"most_common_vals","most_common_freqs","histogram_bounds","correlation"},[]string{"TEXT","TEXT","TEXT","FLOAT4"},[][]any{{"{1,2}","{0.5,0.25}","{3,5,9}",float64(.75)}}))
 for _,column:=range []string{"arr","doc"}{
  elements:="{1,2}";if column=="doc"{elements="{alpha,beta}"}
  s.run(fixture.Select(attributeStatsRestore(column,false,"'most_common_elems','"+elements+"','most_common_elem_freqs','{0.5,0.25,0.25,0.5,0}'::real[],'elem_count_histogram','{0,1,2,3}'::real[]"),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
  s.run(fixture.Select("SELECT most_common_elems::text,most_common_elem_freqs::text,elem_count_histogram::text FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='"+column+"' AND NOT inherited",[]string{"most_common_elems","most_common_elem_freqs","elem_count_histogram"},[]string{"TEXT","TEXT","TEXT"},[][]any{{elements,"{0.5,0.25,0.25,0.5,0}","{0,1,2,3}"}}))
 }
 for _,column:=range []string{"r","m"}{
  s.run(fixture.Select(attributeStatsRestore(column,false,`'range_length_histogram','{1,3,9}','range_empty_frac',.125::real,'range_bounds_histogram','{"[1,2)","[5,9)"}'`),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
  s.run(fixture.Select("SELECT range_length_histogram::text,range_empty_frac,range_bounds_histogram::text FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='"+column+"' AND NOT inherited",[]string{"range_length_histogram","range_empty_frac","range_bounds_histogram"},[]string{"TEXT","FLOAT4","TEXT"},[][]any{{"{1,3,9}",float64(.125),`{"[1,2)","[5,9)"}`}}))
 }
 s.run(fixture.Select(attributeStatsRestore("m",false,`'most_common_vals','{"{[1,2)}","{[5,9)}"}','most_common_freqs','{0.5,0.25}'::real[]`),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select("SELECT most_common_vals::text FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='m' AND NOT inherited",[]string{"most_common_vals"},[]string{"TEXT"},[][]any{{`{"{[1,2)}","{[5,9)}"}`}}))
 s.run(fixture.Select(attributeStatsRestore("arr",false,`'most_common_vals','{"{1,2}","{3}"}','most_common_freqs','{0.5,0.25}'::real[],'histogram_bounds','{"{4}","{5,6}"}','correlation',.5::real`),[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.run(fixture.Select("SELECT most_common_vals::text,histogram_bounds::text FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='arr' AND NOT inherited",[]string{"most_common_vals","histogram_bounds"},[]string{"TEXT","TEXT"},[][]any{{`{"{1,2}","{3}"}`,`{"{4}","{5,6}"}`}}))
 s.restore("'histogram_bounds','[0:2]={3,5,9}'",true)
 s.run(fixture.Select("SELECT histogram_bounds::text FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='a' AND NOT inherited",[]string{"histogram_bounds"},[]string{"TEXT"},[][]any{{"[0:2]={3,5,9}"}}))
 attributeStatsReadbackMetadata(s)
})}

func TestReferenceAttributeStatisticsWarnings(t *testing.T){withAttributeStats(t,func(s attributeStatsScenario){
 s.exec("CREATE TABLE ast_t(a INT,b INT,j json)")
 s.restore("'most_common_vals','{1,2}','most_common_freqs','{0.5,0.25}'::real[]",true)
 cases:=[]struct{extra string;warnings []fixture.Diagnostic}{
  {"'bogus',1",[]fixture.Diagnostic{attributeStatsWarning("01000",`unrecognized argument name: "bogus"`,"")}},
  {"'correlation',.5::double precision",[]fixture.Diagnostic{attributeStatsWarning("01000",`argument "correlation" has type double precision, expected type real`,"")}},
  {"'most_common_vals','{bad}','most_common_freqs','{0.5}'::real[]",[]fixture.Diagnostic{attributeStatsWarning("22P02",`invalid input syntax for type integer: "bad"`,"")}},
  {"'most_common_vals','{3,4}','most_common_freqs','{0.5}'::real[]",[]fixture.Diagnostic{attributeStatsWarning("22023",`could not parse "most_common_vals": incorrect number of elements (same as "most_common_freqs" required)`,"")}},
  {"'most_common_vals','{3,NULL}','most_common_freqs','{0.5,0.25}'::real[]",[]fixture.Diagnostic{attributeStatsWarning("22023",`"most_common_vals" array must not contain null values`,"")}},
  {"'most_common_vals','{{3,4}}','most_common_freqs','{0.5,0.25}'::real[]",[]fixture.Diagnostic{attributeStatsWarning("22023",`"most_common_vals" must be a one-dimensional array`,"")}},
  {"'most_common_vals','{}','most_common_freqs','{0.5}'::real[]",[]fixture.Diagnostic{attributeStatsWarning("22023",`"most_common_vals" must be a one-dimensional array`,"")}},
  {"'most_common_vals','{3}'",[]fixture.Diagnostic{attributeStatsWarning("22023",`argument "most_common_freqs" must be specified when argument "most_common_vals" is specified`,"")}},
  {"'most_common_vals','{3}','most_common_freqs','{0.5}'::double precision[]",[]fixture.Diagnostic{attributeStatsWarning("01000",`argument "most_common_freqs" has type double precision[], expected type real[]`,""),attributeStatsWarning("22023",`argument "most_common_freqs" must be specified when argument "most_common_vals" is specified`,"")}},
  {"'most_common_vals','{3}','most_common_freqs','{{0.5}}'::real[]",[]fixture.Diagnostic{attributeStatsWarning("22023",`argument "most_common_freqs" must not be a multidimensional array`,"")}},
  {"'most_common_vals','{3}','most_common_freqs','{NULL}'::real[]",[]fixture.Diagnostic{attributeStatsWarning("22023",`argument "most_common_freqs" array must not contain null values`,"")}},
  {"'range_bounds_histogram','{1}'",[]fixture.Diagnostic{attributeStatsWarning("22023",`column "a" is not a range type`,"Cannot set STATISTIC_KIND_RANGE_LENGTH_HISTOGRAM or STATISTIC_KIND_BOUNDS_HISTOGRAM.")}},
  {"'most_common_elems','{1}','most_common_elem_freqs','{0.5}'::real[]",[]fixture.Diagnostic{attributeStatsWarning("01000",`could not determine element type of column "a"`,"Cannot set STATISTIC_KIND_MCELEM or STATISTIC_KIND_DECHIST.")}},
 }
 for _,tc:=range cases{s.warnings("'null_frac',.25::real,"+tc.extra,tc.warnings...);s.run(fixture.Select("SELECT null_frac,most_common_vals::text FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='a' AND NOT inherited",[]string{"null_frac","most_common_vals"},[]string{"FLOAT4","TEXT"},[][]any{{float64(.25),"{1,2}"}}))}
 attributeStatsKindBoundaries(s)
})}
