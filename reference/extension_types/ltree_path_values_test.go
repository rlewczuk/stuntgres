package reference_test

import (
 "fmt"
 "strings"
 "testing"
)

func TestReferenceLtreePathConversionsAndConcat(t *testing.T) {
 cases := []ltreePathCase{
  {"SELECT nlevel($1::ltree) AS v","INT4",[]any{""},int64(0)},
  {"SELECT nlevel($1::ltree) AS v","INT4",[]any{"Top.Child1.Child2"},int64(3)},
  {"SELECT nlevel($1::ltree) AS v","INT4",[]any{nil},nil},
 }
 for _,text := range []any{"AbC.a_b.-.42","",nil} {
  cases = append(cases,ltreePathCase{"SELECT text2ltree($1::text) AS v","LTREE",[]any{text},text},ltreePathCase{"SELECT ltree2text($1::ltree) AS v","TEXT",[]any{text},text})
 }
 for _,tc := range []struct{left,right,backer string; a,b,want string}{
  {"ltree","ltree","ltree_addltree","A","B.C","A.B.C"},
  {"ltree","text","ltree_addtext","A","B.C","A.B.C"},
  {"text","ltree","ltree_textadd","A.B","C","A.B.C"},
 } {
  for _,direct := range []bool{false,true} {
   a,b := "$1::"+tc.left,"$2::"+tc.right
   call := a+" || "+b; if direct { call = tc.backer+"("+a+","+b+")" }
   query := "SELECT "+call+" AS v"
   for _,pair := range []struct{a,b,w any}{{tc.a,tc.b,tc.want},{"",tc.b,tc.b},{tc.a,"",tc.a},{"","",""},{nil,tc.b,nil},{tc.a,nil,nil},{nil,nil,nil}} {
    cases = append(cases,ltreePathCase{query,"LTREE",[]any{pair.a,pair.b},pair.w})
   }
  }
 }
 cases = append(cases,
  ltreePathCase{"SELECT 'A'::ltree || 'B'::text || 'C'::ltree AS v","LTREE",nil,"A.B.C"},
  ltreePathCase{"SELECT 'A'::text || 'B'::text AS v","TEXT",nil,"AB"},
  ltreePathCase{"SELECT NULL::ltree || '.bad'::text AS v","LTREE",nil,nil},
  ltreePathCase{"SELECT '.bad'::text || NULL::ltree AS v","LTREE",nil,nil},
 )
 ltreePathMatrix(t,cases)
}

func TestReferenceLtreePathLCA(t *testing.T) {
 var cases []ltreePathCase
 for _,tc := range []struct{a,b string; want any}{
  {"A.B.C","A.B.C.D","A.B"},{"A.B.C","A.B.C","A.B"},{"A.B","A.C","A"},
  {"A","A",""},{"A.B","X.Y",""},{"","A.B",nil},{"A.B","",nil},{"A.B","a.C",""},
 } { cases = append(cases,ltreePathCase{"SELECT lca($1::ltree,$2::ltree) AS v","LTREE",[]any{tc.a,tc.b},tc.want}) }
 for count := 2; count <= 8; count++ {
  placeholders := make([]string,count); args := make([]any,count)
  for i := range count { placeholders[i] = fmt.Sprintf("$%d::ltree",i+1); args[i] = "A.B.C" }
  query := "SELECT lca("+strings.Join(placeholders,",")+") AS v"
  cases = append(cases,ltreePathCase{query,"LTREE",append([]any(nil),args...),"A.B"})
  args[count-1] = "A.X.Y"
  cases = append(cases,ltreePathCase{query,"LTREE",append([]any(nil),args...),"A"})
  for i := range count { nulls := append([]any(nil),args...); nulls[i] = nil; cases = append(cases,ltreePathCase{query,"LTREE",nulls,nil}) }
 }
 for _,tc := range []struct{array any; want any}{
  {"{A.B.C}","A.B"},{"{A}",""},{"{}",nil},{`{""}`,nil},
  {"{A.B.C,A.B.C}","A.B"},{"{A.B.C,A.B.D}","A.B"},{"[7:8]={A.B.C,A.B.D}","A.B"},
  {nil,nil},{"{A.B,X.Y}",""},
 } { cases = append(cases,ltreePathCase{"SELECT lca($1::ltree[]) AS v","LTREE",[]any{tc.array},tc.want}) }
 ltreePathMatrix(t,cases)
}
