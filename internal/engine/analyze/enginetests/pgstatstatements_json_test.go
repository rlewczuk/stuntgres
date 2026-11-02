package enginetests

import (
	"testing"

	"stuntgres/internal/engine"
)

func TestPgStatStatementsFingerprintSQLJSON(t *testing.T) {
	e := engine.New()
	// Independent REL_18_6 expectations: parse_expr.c transformJsonOutput,
	// transformJsonConstructorOutput and transformJsonFuncExpr; gram.y
	// json_wrapper_behavior; primnodes.h JsonReturning/JsonExpr selected fields.
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"SELECT JSON_ARRAY(1)", "SELECT JSON_ARRAY(2 RETURNING json)", true},
		{"SELECT JSON_ARRAY(1)", "SELECT JSON_ARRAY(2 RETURNING jsonb)", false},
		{"SELECT JSON_ARRAY(1 RETURNING text)", "SELECT JSON_ARRAY(2 RETURNING text FORMAT JSON)", true},
		{"SELECT JSON_ARRAY(1 RETURNING varchar(5))", "SELECT JSON_ARRAY(2 RETURNING varchar(6))", false},
		{"SELECT JSON_ARRAY(NULL)", "SELECT JSON_ARRAY(NULL ABSENT ON NULL)", true},
		{"SELECT JSON_ARRAY(NULL)", "SELECT JSON_ARRAY(NULL NULL ON NULL)", false},
		{"SELECT JSON_ARRAY('{}'::json)", "SELECT JSON_ARRAY('{}'::json FORMAT JSON)", true},
		{"SELECT JSON_ARRAY('{}'::text)", "SELECT JSON_ARRAY('{}'::text FORMAT JSON)", false},
		{"SELECT JSON_SERIALIZE('{}'::json)", "SELECT JSON_SERIALIZE('{}'::json RETURNING text)", true},
		{"SELECT JSON_SERIALIZE('{}'::json RETURNING text)", "SELECT JSON_SERIALIZE('{}'::json RETURNING bytea)", false},
		{"SELECT JSON_VALUE('{}'::jsonb,'$')", "SELECT JSON_VALUE('{}'::jsonb,'$' RETURNING text)", true},
		{"SELECT JSON_VALUE('{}'::jsonb,'$')", "SELECT JSON_VALUE('{}'::jsonb,'$' NULL ON EMPTY NULL ON ERROR)", true},
		{"SELECT JSON_VALUE('{}'::jsonb,'$' RETURNING integer)", "SELECT JSON_VALUE('{}'::jsonb,'$' RETURNING bigint)", false},
		{"SELECT JSON_QUERY('{}'::jsonb,'$')", "SELECT JSON_QUERY('{}'::jsonb,'$' RETURNING jsonb)", false},
		{"SELECT JSON_QUERY('{}'::jsonb,'$')", "SELECT JSON_QUERY('{}'::jsonb,'$' WITHOUT WRAPPER)", false},
		{"SELECT JSON_QUERY('{}'::jsonb,'$')", "SELECT JSON_QUERY('{}'::jsonb,'$' KEEP QUOTES)", true},
		{"SELECT JSON_QUERY('{}'::jsonb,'$')", "SELECT JSON_QUERY('{}'::jsonb,'$' OMIT QUOTES)", false},
		{"SELECT JSON_QUERY('{}'::jsonb,'$' WITH WRAPPER)", "SELECT JSON_QUERY('{}'::jsonb,'$' WITH UNCONDITIONAL ARRAY WRAPPER)", true},
		{"SELECT JSON_QUERY('{}'::jsonb,'$')", "SELECT JSON_QUERY('{}'::jsonb FORMAT JSON,'$')", false},
		{"SELECT JSON_EXISTS('{}'::jsonb,'$')", "SELECT JSON_EXISTS('{}'::jsonb,'$' FALSE ON ERROR)", true},
		{"SELECT JSON_EXISTS('{}'::jsonb,'$')", "SELECT JSON_EXISTS('{}'::jsonb,'$' TRUE ON ERROR)", false},
		{"SELECT '{}'::text IS JSON", "SELECT '{}'::text IS JSON VALUE", true},
		{"SELECT '{}'::text IS JSON", "SELECT '{}'::text IS JSON OBJECT", false},
	} {
		t.Run(tc.left+" / "+tc.right, func(t *testing.T) {
			left, right := pgssAnalyzedJumble(t, e, tc.left), pgssAnalyzedJumble(t, e, tc.right)
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("released SQL/JSON selected fields: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
}
