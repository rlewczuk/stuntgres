package core_test

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestEventTriggerRewriteSemantics(t *testing.T) {
	t.Run("metadata_first_visibility", eventTriggerRewriteVisibility)
	for _, tc := range []struct {
		name, definition, alter string
		setup                   []string
		empty                   bool
		want                    int64
	}{
		{"integer_widen", "v integer", "ALTER COLUMN v TYPE bigint", nil, false, 1},
		{"empty_integer_widen", "v integer", "ALTER COLUMN v TYPE bigint", nil, true, 1},
		{"same_type", "v integer", "ALTER COLUMN v TYPE integer", nil, false, 0},
		{"using_same_var", "v integer", "ALTER COLUMN v TYPE integer USING v", nil, false, 0},
		{"using_noop_cast", "v integer", "ALTER COLUMN v TYPE integer USING v::integer", nil, false, 0},
		{"using_arithmetic", "v integer", "ALTER COLUMN v TYPE integer USING v+0", nil, false, 1},
		{"using_constant", "v integer", "ALTER COLUMN v TYPE integer USING 7", nil, false, 1},
		{"binary_text_varchar", "v text", "ALTER COLUMN v TYPE varchar", nil, false, 0},
		{"varchar_widen", "v varchar(10)", "ALTER COLUMN v TYPE varchar(20)", nil, false, 0},
		{"varchar_narrow", "v varchar(20)", "ALTER COLUMN v TYPE varchar(10)", nil, false, 1},
		{"numeric_precision", "v numeric(5,2)", "ALTER COLUMN v TYPE numeric(9,2)", nil, false, 0},
		{"numeric_scale", "v numeric(5,2)", "ALTER COLUMN v TYPE numeric(9,3)", nil, false, 1},
		{"unconstrained_domain", "v integer", "ALTER COLUMN v TYPE et_rewrite_domain", []string{"CREATE DOMAIN et_rewrite_domain AS integer"}, false, 0},
		{"checked_domain", "v integer", "ALTER COLUMN v TYPE et_rewrite_domain", []string{"CREATE DOMAIN et_rewrite_domain AS integer CHECK(VALUE>0)"}, false, 1},
		{"remove_checked_domain", "v et_rewrite_domain", "ALTER COLUMN v TYPE integer", []string{"CREATE DOMAIN et_rewrite_domain AS integer CHECK(VALUE>0)"}, false, 0},
		{"constant_default", "v integer", "ADD COLUMN extra integer DEFAULT 5", nil, false, 0},
		{"volatile_default", "v integer", "ADD COLUMN extra bigint DEFAULT nextval('et_rewrite_sequence')", []string{"CREATE SEQUENCE et_rewrite_sequence"}, false, 1},
		{"stored_expression", "v integer,w integer GENERATED ALWAYS AS(v+1) STORED", "ALTER COLUMN w SET EXPRESSION AS(v+2)", nil, false, 1},
		{"metadata_only", "v integer", "RENAME COLUMN v TO renamed", nil, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			for _, q := range tc.setup {
				enginetest.SessionSQL(t, actor, q)
			}
			enginetest.SessionSQL(t, actor, "CREATE TABLE et_rewrite_subject("+tc.definition+")")
			if !tc.empty {
				value := "7"
				if tc.name == "binary_text_varchar" || tc.name == "varchar_widen" || tc.name == "varchar_narrow" {
					value = "'seven'"
				}
				enginetest.SessionSQL(t, actor, "INSERT INTO et_rewrite_subject(v) VALUES("+value+")")
			}
			for _, q := range []string{
				"CREATE TABLE et_rewrite_audit(n integer)",
				"CREATE FUNCTION et_rewrite_record() RETURNS integer LANGUAGE SQL AS $$INSERT INTO et_rewrite_audit VALUES(1); SELECT 1$$",
				"CREATE FUNCTION et_rewrite_event() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT et_rewrite_record() INTO n; END$$",
				"CREATE EVENT TRIGGER et_rewrite_event ON table_rewrite WHEN TAG IN ('ALTER TABLE') EXECUTE FUNCTION et_rewrite_event()",
			} {
				enginetest.SessionSQL(t, actor, q)
			}
			enginetest.SessionSQL(t, actor, "ALTER TABLE et_rewrite_subject "+tc.alter)
			if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT count(*) FROM et_rewrite_audit")); !reflect.DeepEqual(got, [][]driver.Value{{tc.want}}) {
				t.Fatal("semantic rewrite count", got, "want", tc.want)
			}
		})
	}
}
