package reference_test

import "testing"

func TestReferenceJSONPathNavigateConditions(t *testing.T) {
	cases := []jsonpathNavigateCase{
		{`1`, `$ > 0`, ``, `[true]`}, {`-1`, `$ > 0`, ``, `[false]`}, {`"foo"`, `$ > 0`, ``, `[null]`},
		{`[-1,2,"foo"]`, `$[*]?((@ > 0) is unknown)`, ``, `["foo"]`},
		{`{"a":[1,2,3],"b":[3,4,"5"]}`, `$.a[*] >= $.b[*]`, ``, `[true]`},
		{`{"a":[1,2,3],"b":[3,4,"5"]}`, `strict $.a[*] >= $.b[*]`, ``, `[null]`},
		{`{}`, `$.a == 1`, ``, `[false]`}, {`{}`, `strict $.a == 1`, ``, `[null]`},
		{`{"g":[{"x":2},{"y":3}]}`, `strict $.g[*]?((exists(@.x)) is unknown)`, ``, `[{"y": 3}]`},
		{`{"g":[{"x":2},{"y":3}]}`, `strict $.g?((exists(@[*].x)) is unknown)`, ``, `[[{"x": 2}, {"y": 3}]]`},
		{`[9007199254740992,9007199254740993]`, `$[*]?(@ > 9007199254740992)`, ``, `[9007199254740993]`},
		{`0`, `"z" > "aa"`, ``, `[true]`},
		{`0`, `null == null`, ``, `[true]`}, {`0`, `null == 1`, ``, `[false]`},
		{`0`, `null != 1`, ``, `[true]`}, {`0`, `null < 1`, ``, `[false]`}, {`0`, `false < true`, ``, `[true]`},
		{`0`, `(1 == 2) && ($v == 1)`, ``, `[false]`}, {`0`, `(1 == 1) || ($v == 1)`, ``, `[true]`},
	}
	predicates := []string{`(1 == 1)`, `(1 == 2)`, `("x" == 1)`}
	and := [3][3]string{{"true", "false", "null"}, {"false", "false", "false"}, {"null", "false", "null"}}
	or := [3][3]string{{"true", "true", "true"}, {"true", "false", "null"}, {"true", "null", "null"}}
	for i, left := range predicates {
		for j, right := range predicates {
			cases = append(cases, jsonpathNavigateCase{`0`, left + " && " + right, ``, `[` + and[i][j] + `]`}, jsonpathNavigateCase{`0`, left + " || " + right, ``, `[` + or[i][j] + `]`})
		}
		cases = append(cases, jsonpathNavigateCase{`0`, `!` + left, ``, []string{`[false]`, `[true]`, `[null]`}[i]}, jsonpathNavigateCase{`0`, left + ` is unknown`, ``, []string{`[false]`, `[false]`, `[true]`}[i]})
	}
	jsonpathNavigateCases(t, cases)
}

func TestReferenceJSONPathNavigateRegex(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`["abc","aBc","zzz"]`, `$[*]?(@ like_regex "^abc$" flag "ii")`, ``, `["abc", "aBc"]`},
		{`"a\nb"`, `$ like_regex "a.b"`, ``, `[false]`},
		{`"a\nb"`, `$ like_regex "a.b" flag "s"`, ``, `[true]`},
		{`"a\nb"`, `$ like_regex "^b$" flag "m"`, ``, `[true]`},
		{`"\n"`, `$ like_regex "[^a]"`, ``, `[false]`},
		{`"\n"`, `$ like_regex "[^a]" flag "s"`, ``, `[true]`},
		{`["xxa.byy","axb"]`, `$[*]?(@ like_regex "a.b" flag "qsmx")`, ``, `["xxa.byy"]`},
		{`["A.B","axb"]`, `$[*]?(@ like_regex "a.b" flag "iq")`, ``, `["A.B"]`},
		{`["aa","ab"]`, `$[*]?(@ like_regex "^(a)\\1$")`, ``, `["aa"]`},
		{`1`, `$ like_regex "a"`, ``, `[null]`},
		{`["John Smith","Mary"]`, `$[*]?(@ starts with "John")`, ``, `["John Smith"]`},
		{`"John Smith"`, `$ starts with $v`, `{"v":["John"]}`, `[null]`},
	})
}
