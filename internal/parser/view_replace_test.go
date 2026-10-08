package parser

import "testing"

func TestViewReplaceAlterDropGrammar(t *testing.T) {
	for _, query := range []string{
		"CREATE OR REPLACE VIEW s.v(k,label) WITH(security_barrier=true) AS SELECT id,txt FROM t",
		"ALTER VIEW v RENAME TO renamed", "ALTER VIEW IF EXISTS s.v RENAME COLUMN id TO key",
		"ALTER VIEW v RENAME id TO key", "ALTER VIEW v SET SCHEMA s",
		"ALTER VIEW v OWNER TO owner", "ALTER VIEW v OWNER TO CURRENT_ROLE",
		"ALTER VIEW v OWNER TO CURRENT_USER", "ALTER VIEW v OWNER TO SESSION_USER",
		"ALTER VIEW v SET(security_invoker=false,security_barrier)",
		"ALTER VIEW v RESET(security_invoker,unknown_option)",
		"DROP VIEW v", "DROP VIEW IF EXISTS s.v,w RESTRICT", "DROP VIEW v,w CASCADE",
	} {
		t.Run(query, func(t *testing.T) {
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
		})
	}
	for _, query := range []string{
		"CREATE OR REPLACE VIEW v AS SELECT id FROM t garbage extra_token",
		"ALTER VIEW v RENAME TO renamed garbage", "DROP VIEW v; SELECT FROM",
	} {
		if statements, err := Parse(query); err == nil || len(statements) != 0 {
			t.Fatal(query, statements, err)
		}
	}
}
