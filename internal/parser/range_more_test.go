package parser

import (
	"strings"
	"testing"
)

func TestRangeMoreParserAdmission(t *testing.T) {
	for _, name := range []string{"multirange", "unnest"} {
		for _, q := range []string{
			"SELECT "+name+"(v) FROM r",
			"SELECT pg_catalog."+name+"(v) FROM r",
			"SELECT "+name+"(DISTINCT v) FROM r",
			"SELECT "+name+"(v ORDER BY k) FROM r",
			"SELECT "+name+"(v) FILTER (WHERE true) FROM r",
			"SELECT "+name+"(v) OVER () FROM r",
			"SELECT "+name+"(x => v) FROM r",
			"SELECT "+name+"(VARIADIC v) FROM r",
			"SELECT v FROM r,"+name+"(v) AS u(v)",
		} {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 { t.Fatal(q, err) }
		}
		upper := strings.ToUpper(name)
		statements, err := Parse(`SELECT "`+upper+`"(v) FROM r`)
		if err != nil { t.Fatal(err) }
		if statements[0].(*Select).Targets[0].(FunctionExpr).Name.Parts[0].Text != upper { t.Fatal("quoted identity lost") }
	}
	for _, q := range []string{
		"SELECT multirange(unnest(m)) FROM r",
		"SELECT r,n FROM source,unnest(m) WITH ORDINALITY AS u(r,n)",
		"SELECT r,s FROM source,ROWS FROM(unnest(m),unnest(n)) AS u(r,s)",
		"CREATE TABLE r(a int4range,m int4multirange GENERATED ALWAYS AS(multirange(a)) STORED)",
	} { if statements, err := Parse(q); err != nil || len(statements) != 1 { t.Fatal(q, err) } }
}
