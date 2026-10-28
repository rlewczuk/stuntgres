package reference_test

import (
 "database/sql"
 "strconv"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceLtreePathDiagnostics(t *testing.T) {
 for _,tc := range []struct{name,query,code,message,detail string; args []any}{
  {"slice_negative","SELECT subltree($1::ltree,$2::integer,$3::integer) AS v","22023","invalid positions","",[]any{"A.B.C",-1,2}},
  {"slice_reverse","SELECT subltree($1::ltree,$2::integer,$3::integer) AS v","22023","invalid positions","",[]any{"A.B.C",2,1}},
  {"slice_end","SELECT subpath($1::ltree,$2::integer,$3::integer) AS v","22023","invalid positions","",[]any{"A.B.C",3,0}},
  {"slice_length","SELECT subpath($1::ltree,$2::integer,$3::integer) AS v","22023","invalid positions","",[]any{"A.B.C",2,-2}},
  {"slice_empty","SELECT subpath($1::ltree,$2::integer) AS v","22023","invalid positions","",[]any{"",0}},
  {"double_negative","SELECT subpath($1::ltree,$2::integer) AS v","22023","invalid positions","",[]any{"A.B.C",-7}},
  {"rank","SELECT lca($1::ltree[]) AS v","2202E","array must be one-dimensional","",[]any{"{{A,NULL}}"}},
  {"null_element","SELECT lca($1::ltree[]) AS v","22004","array must not contain nulls","",[]any{`{"",NULL}`}},
  {"text_syntax","SELECT text2ltree($1::text) AS v","42601","ltree syntax error at character 3","",[]any{"A..B"}},
  {"text_end","SELECT text2ltree($1::text) AS v","42601","ltree syntax error","Unexpected end of input.",[]any{"A."}},
  {"text_long","SELECT text2ltree($1::text) AS v","42622","label string is too long","Label length is 1001, must be at most 1000, at character 1002.",[]any{strings.Repeat("a",1001)}},
  {"concat_right","SELECT $1::ltree || $2::text AS v","42601","ltree syntax error at character 1","",[]any{"A",".B"}},
  {"concat_left","SELECT $1::text || $2::ltree AS v","42601","ltree syntax error at character 1","",[]any{".A","B"}},
 } { t.Run(tc.name,func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   want := fixture.ControlsReferenceDiagnostic(tc.code,tc.message); want.Detail = tc.detail
   for _,prepared := range []bool{false,true} { ltreeError(t,p,b,c,tc.query,prepared,true,false,want,tc.args...) }
   stmt,err := c.PrepareContext(p.Ctx,tc.query); if err != nil { t.Fatal(err) }; defer stmt.Close()
   s := fixture.ErrorDiagnostic(tc.query,want,tc.args...); s.SelectRows,s.ErrorStage = true,"execute"
   fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:tc.query},b.Recorder,s)
   recovery,wantValue:=[]any{"A","B"},"A.B"
   switch {
   case strings.HasPrefix(tc.query,"SELECT subltree"): recovery,wantValue=[]any{"A.B.C",1,2},"B"
   case strings.HasPrefix(tc.query,"SELECT subpath"):
    recovery,wantValue=[]any{"A.B.C",1},"B.C";if len(tc.args)==3{recovery,wantValue=[]any{"A.B.C",1,1},"B"}
   case strings.HasPrefix(tc.query,"SELECT lca"): recovery=[]any{"{A.B.C,A.B.D}"}
   case strings.HasPrefix(tc.query,"SELECT text2ltree"): recovery=[]any{"A.B"}
   }
   fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:tc.query},b.Recorder,ltreePathResult(b,tc.query,"LTREE",wantValue,recovery...))
  })
 }) }
 t.Run("typed_input_origin",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   q := "/*é*/ SELECT nlevel('A..B'::ltree) AS v"
   want := fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 3"); want.Position = ltreeSQLPosition(q,"'A..B'")
   for _,prepared := range []bool{false,true} { ltreeError(t,p,b,c,q,prepared,true,true,want) }
   q = "SELECT nlevel($1::ltree) AS v"
   want = queryIntParameterDiagnostic("42601","ltree syntax error at character 3","A..B",1)
   for _,prepared := range []bool{false,true} { ltreeError(t,p,b,c,q,prepared,true,false,want,"A..B") }
   q = "SELECT subpath($1::ltree,$2::integer) AS v"
   want = queryIntParameterDiagnostic("22003",`value "2147483648" is out of range for type integer`,"2147483648",2)
   for _,prepared := range []bool{false,true} { ltreeError(t,p,b,c,q,prepared,true,false,want,"A.B","2147483648") }
   q = "SELECT $1::ltree || $2::text AS v"
   want = queryIntParameterDiagnostic("42601","ltree syntax error at character 3","A..B",1)
   for _,prepared := range []bool{false,true} { ltreeError(t,p,b,c,q,prepared,true,false,want,"A..B",nil) }
   q = "/*é*/ SELECT subpath('A.B'::ltree,'2147483648'::integer)"
   want = fixture.ControlsReferenceDiagnostic("22003",`value "2147483648" is out of range for type integer`)
   want.Position=ltreeSQLPosition(q,"'2147483648'")
   for _,prepared := range []bool{false,true} { ltreeError(t,p,b,c,q,prepared,true,true,want) }
  })
 })
}

func TestReferenceLtreePathBoundaries(t *testing.T) {
 for _,n := range []int{999,1000,65534,65535} { t.Run("accepted_"+strconv.Itoa(n),func(t *testing.T) {
  text := strings.Repeat("a",n); depth := int64(1)
  if n > 1000 { text = strings.Repeat("a.",n-1)+"a"; depth = int64(n) }
  ltreePathMatrix(t,[]ltreePathCase{{"SELECT nlevel(text2ltree($1::text)) AS v","INT4",[]any{text},depth}})
 }) }
 for _,tc := range []struct{name,query,message string;args []any}{
  {"concat_limit","SELECT nlevel($1::ltree || $2::ltree) AS v","",[]any{strings.Repeat("a.",65533)+"a","a"}},
  {"concat_overflow","SELECT $1::ltree || $2::ltree AS v","number of ltree levels (65536) exceeds the maximum allowed (65535)",[]any{strings.Repeat("a.",65534)+"a","a"}},
  {"input_overflow","SELECT text2ltree($1::text) AS v","number of ltree labels (65536) exceeds the maximum allowed (65535)",[]any{strings.Repeat("a.",65535)+"a"}},
  {"text_concat_input_overflow","SELECT $1::ltree || $2::text AS v","number of ltree labels (65536) exceeds the maximum allowed (65535)",[]any{"A",strings.Repeat("a.",65535)+"a"}},
 } { t.Run(tc.name,func(t *testing.T) {
  if tc.message == "" { ltreePathMatrix(t,[]ltreePathCase{{tc.query,"INT4",tc.args,int64(65535)}}); return }
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   want := fixture.ControlsReferenceDiagnostic("54000",tc.message)
   for _,prepared := range []bool{false,true} { ltreeError(t,p,b,c,tc.query,prepared,true,false,want,tc.args...) }
  })
 }) }
 t.Run("int32_positions",func(t *testing.T) {
  ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   ltreeInstall(t,p,b,c)
   for _,tc := range []struct{query string;args []any}{
    {"SELECT subltree($1::ltree,$2::integer,$3::integer)",[]any{"A.B.C",int64(-2147483648),int64(2147483647)}},
    {"SELECT subltree($1::ltree,$2::integer,$3::integer)",[]any{"A.B.C",0,int64(-2147483648)}},
    {"SELECT subpath($1::ltree,$2::integer)",[]any{"A.B.C",int64(-2147483648)}},
    {"SELECT subpath($1::ltree,$2::integer)",[]any{"A.B.C",int64(2147483647)}},
    {"SELECT subpath($1::ltree,$2::integer,$3::integer)",[]any{"A.B.C",1,int64(2147483647)}},
    {"SELECT subpath($1::ltree,$2::integer,$3::integer)",[]any{"A.B.C",0,int64(-2147483648)}},
   } { for _,prepared := range []bool{false,true} { ltreeError(t,p,b,c,tc.query,prepared,true,false,fixture.ControlsReferenceDiagnostic("22023","invalid positions"),tc.args...) } }
  })
 })
}
