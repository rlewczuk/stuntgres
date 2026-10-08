//go:build reference

package reference_test

import "testing"

func TestReferenceLikeMatching(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE seed(i INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO seed VALUES(1)", 1))
	for _, tc := range []struct {
		expr string
		want any
	}{
		{"'abc' LIKE 'abc'", true}, {"'abc' LIKE 'a%'", true}, {"'abc' LIKE '_b_'", true},
		{"'abc' LIKE 'b'", false}, {"'abc' LIKE 'c'", false}, {"'' LIKE ''", true}, {"'' LIKE '%'", true}, {"'' LIKE '_'", false}, {"'x' LIKE ''", false},
		{"'é' LIKE '_'", true}, {"'🙂' LIKE '_'", true}, {"'é' LIKE '_'", false}, {"'é' LIKE '__'", true},
		{"'a\nb' LIKE 'a%b'", true}, {"'.^$[]()+?*|' LIKE '.^$[]()+?*|'", true},
		{"'abc' LIKE '%a%b%c%'", true}, {"'abc' LIKE '%%%'", true}, {"'ab' LIKE '%_%_'", true},
		{"'AbC' LIKE 'abc'", false}, {"'AbC' ILIKE 'abc'", true}, {"'É' ILIKE 'é'", false}, {"'İ' ILIKE 'i'", false}, {"'ß' ILIKE 'SS'", false},
		{"'a%b' LIKE 'a\\%b'", true}, {"'a_b' LIKE 'a!_b' ESCAPE '!'", true},
		{"'a%b' LIKE 'aé%b' ESCAPE 'é'", true}, {"'ab' ILIKE 'a_' ESCAPE 'A'", true}, {"'xa' ILIKE 'X_' ESCAPE 'x'", true},
		{"'a\\b' LIKE 'a\\b' ESCAPE ''", true}, {"'a!b' LIKE 'a!!b' ESCAPE '!'", true}, {"'ab' LIKE '!ab' ESCAPE '!'", true},
		{"NULL NOT LIKE 'x'", nil}, {"'x' NOT ILIKE NULL", nil}, {"'x' LIKE 'x' ESCAPE NULL", nil},
		{"'abc' ~~ 'a%'", true}, {"'abc' !~~ 'a%'", false}, {"'AbC' ~~* 'a%'", true}, {"'AbC' !~~* 'a%'", false},
	} {
		s := referenceSelect("SELECT "+tc.expr+" AS matched FROM seed", []string{"matched"}, []string{"BOOL"}, [][]any{{tc.want}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceExec("CREATE TABLE chars(c CHAR(3),p CHAR(3),e CHAR(3),v VARCHAR(3),b BYTEA)", 0))
	p.step(t, referenceExec("INSERT INTO chars VALUES('a','a','%','a ','\\xc3a9')", 1))
	s := referenceSelect("SELECT c LIKE 'a' AS a,c LIKE 'a__' AS b,CAST(c AS TEXT) LIKE 'a' AS c,'a' LIKE p AS d,'a%b' LIKE 'a%%b' ESCAPE e AS e,v LIKE 'a' AS f,b LIKE '\\x5f' AS g,b LIKE '\\x5f5f' AS h FROM chars", []string{"a", "b", "c", "d", "e", "f", "g", "h"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, true, true, false, false, true}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
}
