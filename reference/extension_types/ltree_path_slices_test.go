package reference_test

import "testing"

func TestReferenceLtreePathSlices(t *testing.T) {
 var cases []ltreePathCase
 for _,tc := range []struct{call string; args []any; want string}{
  {"subltree($1::ltree,$2::integer,$3::integer)",[]any{"Top.Child1.Child2",1,2},"Child1"},
  {"subltree($1::ltree,$2::integer,$3::integer)",[]any{"Top.Child1.Child2",1,99},"Child1.Child2"},
  {"subltree($1::ltree,$2::integer,$3::integer)",[]any{"Top.Child1.Child2",1,1},""},
  {"subpath($1::ltree,$2::integer)",[]any{"Top.Child1.Child2",1},"Child1.Child2"},
  {"subpath($1::ltree,$2::integer,$3::integer)",[]any{"Top.Child1.Child2",-1,1},"Child2"},
  {"subpath($1::ltree,$2::integer)",[]any{"Top.Child1.Child2",-3},"Top.Child1.Child2"},
  {"subpath($1::ltree,$2::integer,$3::integer)",[]any{"Top.Child1.Child2",0,-2},"Top"},
  {"subpath($1::ltree,$2::integer,$3::integer)",[]any{"Top.Child1.Child2",1,-1},"Child1"},
  {"subpath($1::ltree,$2::integer,$3::integer)",[]any{"Top.Child1.Child2",0,-3},""},
  {"subpath($1::ltree,$2::integer,$3::integer)",[]any{"Top.Child1.Child2",1,0},""},
  {"subpath($1::ltree,$2::integer,$3::integer)",[]any{"Top.Child1.Child2",1,99},"Child1.Child2"},
  {"subpath($1::ltree,$2::integer)",[]any{"A.B.C",-4},"C"},
  {"subpath($1::ltree,$2::integer)",[]any{"A.B.C",-6},"A.B.C"},
  {"subltree($1::ltree,$2::integer,$3::integer)",[]any{"A.B.C",0,int64(2147483647)},"A.B.C"},
  {"subpath($1::ltree,$2::integer,$3::integer)",[]any{"A.B.C",0,int64(2147483647)},"A.B.C"},
 } {
  query := "SELECT "+tc.call+" AS v"
  cases = append(cases,ltreePathCase{query,"LTREE",tc.args,tc.want})
  for i := range tc.args { args := append([]any(nil),tc.args...); args[i] = nil; cases = append(cases,ltreePathCase{query,"LTREE",args,nil}) }
 }
 cases = append(cases,ltreePathCase{"SELECT subpath('Top.Child1.Child2'::ltree,1) AS v","LTREE",nil,"Child1.Child2"})
 ltreePathMatrix(t,cases)
}

func TestReferenceLtreePathIndex(t *testing.T) {
 var cases []ltreePathCase
 for _,tc := range []struct{a,b string; offset int64; want int64}{
  {"A.B.A.B","A.B",0,0},{"A.B.A.B","A.B",1,2},{"A.B.A.B","A.B",-2,2},{"A.B.A.B","A.B",-99,0},
  {"A.B","missing",0,-1},{"Topical","Top",0,-1},{"A.B","a",0,-1},
  {"A.B","",0,-1},{"","A",0,-1},{"","",0,-1},{"A.B","A",2,-1},{"A.B","A.B",1,-1},
  {"A.B.C","A",2147483647,-1},{"A.B.C","A",-2147483648,-1},
 } {
  cases = append(cases,ltreePathCase{"SELECT index($1::ltree,$2::ltree,$3::integer) AS v","INT4",[]any{tc.a,tc.b,tc.offset},tc.want})
  if tc.offset == 0 { cases = append(cases,ltreePathCase{"SELECT index($1::ltree,$2::ltree) AS v","INT4",[]any{tc.a,tc.b},tc.want}) }
 }
 for _,query := range []string{"SELECT index($1::ltree,$2::ltree) AS v","SELECT index($1::ltree,$2::ltree,$3::integer) AS v"} {
  args := []any{"A.B","A"}; if query == "SELECT index($1::ltree,$2::ltree,$3::integer) AS v" { args = append(args,0) }
  for i := range args { nulls := append([]any(nil),args...); nulls[i] = nil; cases = append(cases,ltreePathCase{query,"INT4",nulls,nil}) }
 }
 ltreePathMatrix(t,cases)
}
