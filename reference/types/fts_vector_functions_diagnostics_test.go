package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceFTSVectorFunctionsDiagnostics(t *testing.T){
 for _,tc:=range []struct{query,code,message,detail string;position int;eager bool;args []any}{
  {query:"SELECT setweight(''::tsvector,'Z')",code:"22023",message:`unrecognized weight: "Z"`},
  {query:"SELECT setweight('cat'::tsvector,'Z','{missing}')",code:"22023",message:`unrecognized weight: "Z"`},
  {query:"SELECT setweight('cat'::tsvector,'')",code:"22023",message:`unrecognized weight: "\000"`},
  {query:"SELECT setweight('cat'::tsvector,$1)",args:[]any{"\\177"},code:"22023",message:`unrecognized weight: "\177"`},
  {query:"SELECT setweight('cat'::tsvector,$1)",args:[]any{"\\377"},code:"22023",message:`unrecognized weight: "\377"`},
  {query:"SELECT ts_filter(''::tsvector,'{Z,NULL}')",code:"22023",message:`unrecognized weight: "Z"`},
  {query:"SELECT ts_filter(''::tsvector,'{NULL,Z}')",code:"22004",message:"weight array may not contain nulls"},
  {query:"SELECT ts_filter('cat'::tsvector,'{NULL}')",code:"22004",message:"weight array may not contain nulls"},
  {query:`SELECT ts_filter('cat'::tsvector,ARRAY['A'])`,code:"42883",message:"function ts_filter(tsvector, text[]) does not exist",position:8,eager:true},
  {query:`/*é*/ SELECT ts_filter('cat'::tsvector,ARRAY['A'])`,code:"42883",message:"function ts_filter(tsvector, text[]) does not exist",position:14,eager:true},
  {query:`SELECT ts_filter('cat'::tsvector,'oops'::"char"[])`,code:"22P02",message:`malformed array literal: "oops"`,detail:`Array value must start with "{" or dimension information.`,position:34,eager:true},
  {query:`/*é*/ SELECT ts_filter('cat'::tsvector,'oops'::"char"[])`,code:"22P02",message:`malformed array literal: "oops"`,detail:`Array value must start with "{" or dimension information.`,position:40,eager:true},
  {query:`SELECT ts_delete('cat'::tsvector,$1::text[])`,args:[]any{"oops"},code:"22P02",message:`malformed array literal: "oops"`,detail:`Array value must start with "{" or dimension information.`},
  {query:`SELECT ts_filter('cat'::tsvector,$1::"char"[])`,args:[]any{"oops"},code:"22P02",message:`malformed array literal: "oops"`,detail:`Array value must start with "{" or dimension information.`},
  {query:`SELECT ts_delete('cat'::tsvector,$1::text[])`,args:[]any{"{{cat},{rat,fat}}"},code:"22P02",message:`malformed array literal: "{{cat},{rat,fat}}"`,detail:"Multidimensional arrays must have sub-arrays with matching dimensions."},
  {query:`SELECT ts_filter('cat'::tsvector,$1::"char"[])`,args:[]any{"{{A},{B,C}}"},code:"22P02",message:`malformed array literal: "{{A},{B,C}}"`,detail:"Multidimensional arrays must have sub-arrays with matching dimensions."},
  {query:`SELECT ts_delete('cat'::tsvector,$1::text[])`,args:[]any{"[0:2]={cat,rat}"},code:"22P02",message:`malformed array literal: "[0:2]={cat,rat}"`,detail:"Specified array dimensions do not match array contents."},
  {query:`SELECT ts_filter('cat'::tsvector,$1::"char"[])`,args:[]any{"[0:2]={A,B}"},code:"22P02",message:`malformed array literal: "[0:2]={A,B}"`,detail:"Specified array dimensions do not match array contents."},
  {query:`SELECT ts_delete('cat'::tsvector,$1::text[])`,args:[]any{"[1:0]={}"},code:"2202E",message:"upper bound cannot be less than lower bound"},
  {query:`SELECT ts_filter('cat'::tsvector,$1::"char"[])`,args:[]any{"[1:0]={}"},code:"2202E",message:"upper bound cannot be less than lower bound"},
  {query:`SELECT ts_delete('cat'::tsvector,'oops'::text[])`,code:"22P02",message:`malformed array literal: "oops"`,detail:`Array value must start with "{" or dimension information.`,position:34,eager:true},
  {query:`SELECT ts_delete('cat'::tsvector,'{{cat},{rat,fat}}'::text[])`,code:"22P02",message:`malformed array literal: "{{cat},{rat,fat}}"`,detail:"Multidimensional arrays must have sub-arrays with matching dimensions.",position:34,eager:true},
  {query:`SELECT ts_filter('cat'::tsvector,'{{A},{B,C}}'::"char"[])`,code:"22P02",message:`malformed array literal: "{{A},{B,C}}"`,detail:"Multidimensional arrays must have sub-arrays with matching dimensions.",position:34,eager:true},
  {query:`SELECT strip($1::tsvector)`,args:[]any{"a:0"},code:"42601",message:`wrong position info in tsvector: "a:0"`},
  {query:`SELECT setweight('cat'::tsvector,'A'::text)`,code:"42883",message:"function setweight(tsvector, text) does not exist",position:8,eager:true},
 }{t.Run(tc.query+tc.message,func(t *testing.T){ftsQueryFunctionsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  d:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);d.Detail=tc.detail;d.Position=fixture.Position(tc.position)
  if tc.code=="42883"{d.Hint="No function matches the given name and argument types. You might need to add explicit type casts."}
  if (tc.code=="22P02"||tc.code=="2202E"||tc.code=="42601")&&len(tc.args)>0{d.Context="unnamed portal parameter $1 = '...'"}
  for _,prepared:=range []bool{false,true}{for _,queryMode:=range []bool{false,true}{s:=fixture.ErrorDiagnostic(tc.query,d,tc.args...);s.Prepared=prepared;s.SelectRows=queryMode;s.ErrorStage="execute";if prepared&&tc.eager{s.ErrorStage="prepare"};fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}}
  ftsQueryFunctionsValue(t,p,b,"length('cat'::tsvector)","INT4",int64(1))
 })})}
}
