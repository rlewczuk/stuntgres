package reference_test

import "testing"

func TestReferenceJSONPathNavigateNavigation(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`[1,2]`, `$`, ``, `[[1, 2]]`},
		{`[1,2]`, `$[*]`, ``, `[1, 2]`},
		{`{"a b":{"v":[10,20]}}`, `$."a b".v[1]`, ``, `[20]`},
		{`[10,20,30]`, `$[2,0 to 1,last]`, ``, `[30, 10, 20, 30]`},
		{`{"bb":2,"a":1}`, `$.*`, ``, `[1, 2]`},
		{`[[1,2],[3,4]]`, `$[*][*]`, ``, `[1, 2, 3, 4]`},
		{`{"a":null}`, `$.a`, ``, `[null]`},
		{`{}`, `$.a`, ``, `[]`},
	})
}

func TestReferenceJSONPathNavigateRecursive(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`{"a":{"b":1}}`, `strict $.**{0}`, ``, `[{"a": {"b": 1}}]`},
		{`{"a":{"b":1}}`, `strict $.**{2}`, ``, `[1]`},
		{`{"a":{"b":1}}`, `strict $.**{last}`, ``, `[1]`},
		{`{"a":[{"HR":73},{"HR":135}]}`, `strict $.**.HR`, ``, `[73, 135]`},
		{`{"a":[{"HR":73},{"HR":135}]}`, `$.**.HR`, ``, `[73, 135, 73, 135]`},
		{`{"a":{"b":1},"c":2}`, `strict $.**{1 to 2}`, ``, `[{"b": 1}, 1, 2]`},
	})
}

func TestReferenceJSONPathNavigateScopeVariables(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`{"min":2,"a":[{"v":1},{"v":3}]}`, `$.a[*]?(@.v > $.min).v`, ``, `[3]`},
		{`[{"x":1,"c":[{"y":2}]},{"x":2,"c":[{"y":0}]}]`, `$[*]?(exists(@.c[*]?(@.y > 1))).x`, ``, `[1]`},
		{`[[1,2],[3]]`, `$[*][last]`, ``, `[2, 3]`},
		{`[[0],[1]]`, `$[$[last][0],last]`, ``, `[[1], [1]]`},
		{`[1,2,3]`, `$[*]?(@ >= $min && @ <= $max)`, `{"min":2,"max":3}`, `[2, 3]`},
		{`0`, `$v`, `{"v":{"x":1}}`, `[{"x": 1}]`},
		{`0`, `$v`, `{"v":[1,2]}`, `[[1, 2]]`},
		{`0`, `$v`, `{"v":"abc"}`, `["abc"]`},
		{`0`, `$v`, `{"v":1.00}`, `[1.00]`},
		{`0`, `$v`, `{"v":true}`, `[true]`},
		{`0`, `$v`, `{"v":null}`, `[null]`},
	})
}

func TestReferenceJSONPathNavigateStrictLax(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`[{"x":1},[{"x":2}]]`, `$.x`, ``, `[1]`},
		{`7`, `$[0]`, ``, `[7]`}, {`7`, `$[*]`, ``, `[7]`},
		{`[10,20]`, `$[-1]`, ``, `[]`},
		{`[10,20]`, `$[0.9]`, ``, `[10]`}, {`[10,20]`, `$[-0.9]`, ``, `[10]`},
		{`[10,20,30]`, `$[-2 to 1]`, ``, `[10, 20]`},
		{`[10,20,30]`, `$[2 to 0]`, ``, `[]`},
		{`1`, `(1 == 1)[0]`, ``, `[true]`},
	})
}
