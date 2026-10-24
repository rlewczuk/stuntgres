package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLIntegerForNamespace(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, body, typ string
		want                         any
	}{
		{"implicit_type", "s text:='';", "FOR i IN 1..2 LOOP s:=if_type_name(i); END LOOP; RETURN s;", "TEXT", "integer"},
		{"shadow_text", "i text:='outer';", "FOR i IN 1..2 LOOP i:=99; END LOOP; RETURN i;", "TEXT", "outer"},
		{"shadow_constant", "i CONSTANT text:='outer';", "FOR i IN 1..2 LOOP i:=99; END LOOP; RETURN i;", "TEXT", "outer"},
		{"shadow_not_null", "i text NOT NULL:='outer';", "FOR i IN 1..2 LOOP i:=NULL; END LOOP; RETURN i;", "TEXT", "outer"},
		{"outer_bounds", "i integer:=3; s integer:=0;", "FOR i IN i..i+2 LOOP s:=s+i; END LOOP; RETURN s*10+i;", "INT4", int64(123)},
		{"outer_step", "i integer:=2; s integer:=0;", "FOR i IN 1..5 BY i LOOP s:=s+i; END LOOP; RETURN s*10+i;", "INT4", int64(92)},
		{"independent_counter", "s text:='';", "FOR i IN 1..3 LOOP s:=s||i::text; i:=99; END LOOP; RETURN s;", "TEXT", "123"},
		{"quoted_names", "\"by\" integer:=1; \"reverse\" integer:=3; \"loop\" integer:=1; s integer:=0;", "FOR i IN \"by\"..\"reverse\" BY \"loop\" LOOP s:=s+i; END LOOP; RETURN s;", "INT4", int64(6)},
		{"record_bound", "i record; s integer:=0;", "i:=ROW(3); FOR i IN i.f1..i.f1+2 LOOP s:=s+i; END LOOP; RETURN s;", "INT4", int64(12)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if tc.name == "implicit_type" {
					integerForTypeWitness(t, p, b, c)
				}
				returnType := map[string]string{"INT4": "integer", "TEXT": "text"}[tc.typ]
				userOperatorExec(t, p, b, c, "CREATE FUNCTION if_namespace() RETURNS "+returnType+" LANGUAGE plpgsql AS $$DECLARE "+tc.declaration+" BEGIN "+tc.body+" END$$")
				plpgsqlValue(t, p, b, c, "SELECT if_namespace() AS v", tc.typ, tc.want)
			})
		})
	}
	t.Run("formal_alias", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION if_formal(i integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE original ALIAS FOR $1; s integer:=0; BEGIN FOR i IN original..i+2 LOOP s:=s+i; END LOOP; RETURN s*10+original; END$$")
			parametersValue(t, p, b, c, "SELECT if_formal($1) AS v", "v", "INT4", int64(123), 3)
		})
	})
	t.Run("postloop_unbound", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION if_private() RETURNS integer LANGUAGE plpgsql AS $$BEGIN FOR i IN 1..2 LOOP END LOOP; RETURN i; END$$")
			want := plpgsqlRuntimeDiagnostic("42703", `column "i" does not exist`, "if_private()", "RETURN")
			want.InternalQuery, want.InternalPosition = "i", "1"
			plpgsqlError(t, p, c, "SELECT if_private() AS v", true, want)
		})
	})
}
