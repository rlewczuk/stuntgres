package parser

import "testing"

func TestCreateViewGrammar(t *testing.T) {
	for _, query := range []string{
		"CREATE VIEW v AS SELECT id FROM t",
		"CREATE VIEW public.v(k,label) AS SELECT id,txt FROM t WHERE visible",
		"CREATE VIEW v(k) WITH (security_barrier=true,security_invoker=false) AS SELECT id,txt FROM t",
		"CREATE VIEW v AS SELECT count(*) FROM t GROUP BY id HAVING count(*)>1",
		"CREATE VIEW z AS SELECT FROM t",
	} {
		t.Run(query, func(t *testing.T) {
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
		})
	}
}

func TestCreateViewConsumesEntireInput(t *testing.T) {
	for _, query := range []string{
		"CREATE VIEW v AS SELECT id FROM t garbage extra_token",
		"CREATE VIEW v AS SELECT id FROM t; SELECT FROM",
	} {
		statements, err := Parse(query)
		if err == nil || len(statements) != 0 {
			t.Fatal(query, statements, err)
		}
	}
}
