package core_test

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestEventTriggerDropEffects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   []string
		command string
		want    int64
	}{
		{"table", []string{"CREATE TABLE et_subject(v integer)"}, "DROP TABLE et_subject", 1},
		{"missing_table", nil, "DROP TABLE IF EXISTS et_subject", 0},
		{"column", []string{"CREATE TABLE et_subject(v integer,w text)"}, "ALTER TABLE et_subject DROP COLUMN w", 1},
		{"missing_column", []string{"CREATE TABLE et_subject(v integer)"}, "ALTER TABLE et_subject DROP COLUMN IF EXISTS w", 0},
		{"check_constraint", []string{"CREATE TABLE et_subject(v integer,CONSTRAINT et_check CHECK(v>0))"}, "ALTER TABLE et_subject DROP CONSTRAINT et_check", 1},
		{"missing_constraint", []string{"CREATE TABLE et_subject(v integer)"}, "ALTER TABLE et_subject DROP CONSTRAINT IF EXISTS et_check", 0},
		{"foreign_key", []string{"CREATE TABLE et_parent(v integer PRIMARY KEY)", "CREATE TABLE et_subject(v integer,CONSTRAINT et_fk FOREIGN KEY(v) REFERENCES et_parent(v))"}, "ALTER TABLE et_subject DROP CONSTRAINT et_fk", 1},
		{"not_null", []string{"CREATE TABLE et_subject(v integer NOT NULL)"}, "ALTER TABLE et_subject ALTER COLUMN v DROP NOT NULL", 1},
		{"already_nullable", []string{"CREATE TABLE et_subject(v integer)"}, "ALTER TABLE et_subject ALTER COLUMN v DROP NOT NULL", 0},
		{"drop_default", []string{"CREATE TABLE et_subject(v integer DEFAULT 1)"}, "ALTER TABLE et_subject ALTER COLUMN v DROP DEFAULT", 1},
		{"missing_default", []string{"CREATE TABLE et_subject(v integer)"}, "ALTER TABLE et_subject ALTER COLUMN v DROP DEFAULT", 0},
		{"replace_default_internal", []string{"CREATE TABLE et_subject(v integer DEFAULT 1)"}, "ALTER TABLE et_subject ALTER COLUMN v SET DEFAULT 2", 0},
		{"drop_identity_internal", []string{"CREATE TABLE et_subject(v integer GENERATED ALWAYS AS IDENTITY)"}, "ALTER TABLE et_subject ALTER COLUMN v DROP IDENTITY", 0},
		{"missing_identity", []string{"CREATE TABLE et_subject(v integer)"}, "ALTER TABLE et_subject ALTER COLUMN v DROP IDENTITY IF EXISTS", 0},
		{"drop_expression", []string{"CREATE TABLE et_subject(v integer,w integer GENERATED ALWAYS AS (v+1) STORED)"}, "ALTER TABLE et_subject ALTER COLUMN w DROP EXPRESSION", 1},
		{"replace_expression", []string{"CREATE TABLE et_subject(v integer,w integer GENERATED ALWAYS AS (v+1) STORED)"}, "ALTER TABLE et_subject ALTER COLUMN w SET EXPRESSION AS (v+1)", 1},
		{"missing_expression", []string{"CREATE TABLE et_subject(v integer)"}, "ALTER TABLE et_subject ALTER COLUMN v DROP EXPRESSION IF EXISTS", 0},
		{"type_index_rebuild_internal", []string{"CREATE TABLE et_subject(v integer PRIMARY KEY)", "INSERT INTO et_subject VALUES(1)", "CREATE INDEX et_standalone ON et_subject(v)"}, "ALTER TABLE et_subject ALTER COLUMN v TYPE bigint", 0},
		{"trigger_payload", []string{"CREATE TABLE et_subject(v integer)", "CREATE FUNCTION et_row() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER et_row BEFORE INSERT ON et_subject FOR EACH ROW EXECUTE FUNCTION et_row()"}, "DROP TRIGGER et_row ON et_subject", 1},
		{"missing_trigger", []string{"CREATE TABLE et_subject(v integer)"}, "DROP TRIGGER IF EXISTS et_row ON et_subject", 0},
		{"rule_payload", []string{"CREATE TABLE et_subject(v integer)", "CREATE TABLE et_rule_audit(v integer)", "CREATE RULE et_rule AS ON INSERT TO et_subject DO ALSO INSERT INTO et_rule_audit VALUES(1)"}, "DROP RULE et_rule ON et_subject", 1},
		{"policy_payload", []string{"CREATE TABLE et_subject(v integer)", "CREATE POLICY et_policy ON et_subject USING(v>0)"}, "DROP POLICY et_policy ON et_subject", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			for _, q := range tc.setup {
				enginetest.SessionSQL(t, actor, q)
			}
			for _, q := range []string{
				"CREATE TABLE et_effect_audit(n integer)",
				"CREATE FUNCTION et_effect_record() RETURNS integer LANGUAGE SQL AS $$INSERT INTO et_effect_audit VALUES(1); SELECT 1$$",
				"CREATE FUNCTION et_effect() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT et_effect_record() INTO n; END$$",
				"CREATE EVENT TRIGGER et_effect ON sql_drop EXECUTE FUNCTION et_effect()",
			} {
				enginetest.SessionSQL(t, actor, q)
			}
			enginetest.SessionSQL(t, actor, tc.command)
			if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT count(*) FROM et_effect_audit")); !reflect.DeepEqual(got, [][]driver.Value{{tc.want}}) {
				t.Fatal("actual deletion phase count", got, "want", tc.want)
			}
		})
	}
}
