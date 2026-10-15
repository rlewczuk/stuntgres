package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBContainmentTypingMetadataHeld(t *testing.T) {
	jsonbContainmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN jbc_doc AS jsonb", 0))
		for _, op := range []string{"@>", "<@"} {
			for _, operands := range [][2]string{
				{"NULL::jsonb", "'{}'::jsonb"}, {"'{}'::jsonb", "NULL::jsonb"}, {"NULL::jsonb", "NULL::jsonb"},
			} {
				s := jsonbExistsSelect("SELECT "+operands[0]+" "+op+" "+operands[1]+" AS v", nil)
				s.CheckMetadata = jsonbExistsMetadata
				jsonbExistsBoth(t, p, b, c, s)
			}
			for _, operands := range [][2]string{
				{"'{}'::jsonb", "'{}'"}, {"'{}'", "'{}'::jsonb"}, {"'{}'::jbc_doc", "'{}'::jbc_doc"},
				{"'{}'::jbc_doc", "'{}'::jsonb"}, {"'{}'::jsonb", "'{}'::jbc_doc"},
			} {
				jsonbExistsBoth(t, p, b, c, jsonbExistsSelect("SELECT "+operands[0]+" "+op+" "+operands[1]+" AS v", true))
			}
			for _, expr := range []string{"$1::jsonb " + op + " $2", "$1 " + op + " $2::jsonb"} {
				jsonbExistsBoth(t, p, b, c, jsonbExistsSelect("SELECT "+expr+" AS v", true, `{}`, `{}`))
			}
			jsonbExistsBoth(t, p, b, c, jsonbExistsSelect("SELECT '{}'::jsonb OPERATOR(pg_catalog."+op+") '{}'::jsonb AS v", true))
		}
		jsonbContainmentHeld(t, p, b, c)
		jsonbContainmentRows(t, p, b, c)
	})
}
