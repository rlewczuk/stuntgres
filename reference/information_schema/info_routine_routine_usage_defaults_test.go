package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoutineRoutineUsageDefaultsAndBodies(t *testing.T) {
	rruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		rruExec(t, p, b, c, "CREATE FUNCTION rru_body_leaf() RETURNS integer RETURN 3", "CREATE FUNCTION rru_default_leaf() RETURNS integer RETURN 5")
		cases := []struct {
			name, inputs, declaration string
			targets                   []string
		}{
			{"rru_body_return", "", "() RETURNS integer RETURN rru_body_leaf()+rru_body_leaf()", []string{"rru_body_leaf"}},
			{"rru_body_atomic", "", "() RETURNS integer BEGIN ATOMIC SELECT rru_body_leaf()+rru_body_leaf(); END", []string{"rru_body_leaf"}},
			{"rru_body_quote", "", "() RETURNS integer LANGUAGE SQL AS 'SELECT rru_body_leaf()'", nil},
			{"rru_body_dollar", "", "() RETURNS integer LANGUAGE SQL AS $$SELECT rru_body_leaf()$$", nil},
			{"rru_body_quote_default", "integer", "(a integer DEFAULT rru_default_leaf()) RETURNS integer LANGUAGE SQL AS 'SELECT rru_body_leaf()'", []string{"rru_default_leaf"}},
			{"rru_body_both", "integer", "(a integer DEFAULT rru_default_leaf()) RETURNS integer RETURN rru_body_leaf()", []string{"rru_body_leaf", "rru_default_leaf"}},
			{"rru_body_duplicate", "integer", "(a integer DEFAULT rru_body_leaf()) RETURNS integer RETURN rru_body_leaf()", []string{"rru_body_leaf"}},
			{"rru_body_pl", "", "() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN rru_body_leaf(); END$$", nil},
			{"rru_body_pl_default", "integer", "(a integer DEFAULT rru_default_leaf()) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN rru_body_leaf(); END$$", []string{"rru_default_leaf"}},
			{"rru_body_pin_sql", "integer", "(a integer DEFAULT power(2::float8,3::float8)::integer) RETURNS integer LANGUAGE SQL AS 'SELECT 3'", nil},
			{"rru_body_pin_pl", "integer", "(a integer DEFAULT power(2::float8,3::float8)::integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 3; END$$", nil},
			{"rru_body_null", "integer", "(a integer DEFAULT NULL) RETURNS integer RETURN 3", nil},
		}
		var want [][]any
		for _, tc := range cases {
			rruExec(t, p, b, c, "CREATE FUNCTION "+tc.name+tc.declaration)
			source := rruID(t, p, c, "public", tc.name, tc.inputs)
			for _, target := range tc.targets {
				want = append(want, rruRow(database, "public", source, "public", rruID(t, p, c, "public", target, "")))
			}
		}
		rruCheck(t, p, b, c, "specific_schema='public'", want)
		for _, tc := range cases {
			value := int64(3)
			if tc.name == "rru_body_return" || tc.name == "rru_body_atomic" {
				value = 6
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT "+tc.name+"() AS v", []string{"v"}, []string{"INT4"}, [][]any{{value}}))
		}
		rruCheck(t, p, b, c, "specific_schema='public'", want)
	})
}
