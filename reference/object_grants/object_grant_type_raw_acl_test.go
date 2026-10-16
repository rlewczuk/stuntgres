package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantTypeRawACL(t *testing.T) {
	for _, tc := range []struct{ name, create string }{
		{"og_raw_domain", "CREATE DOMAIN og_raw_domain AS INTEGER"},
		{"og_raw_enum", "CREATE TYPE og_raw_enum AS ENUM('one')"},
		{"og_raw_composite", "CREATE TYPE og_raw_composite AS(a INTEGER)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec(tc.create, 0))
				owner := "postgres"
				if b.PG {
					owner = "stuntgres"
				}
				call := "pg_get_acl(1247,'" + tc.name + "'::regtype,0)"
				absent := func(want bool) {
					run(fixture.Select("SELECT "+call+" IS NULL AS absent", []string{"absent"}, []string{"BOOL"}, [][]any{{want}}))
				}
				entries := func(public bool) {
					want := [][]any{{owner + "=U/" + owner}}
					if public {
						want = append(want, []any{"=U/" + owner})
					}
					step := fixture.Select("SELECT unnest("+call+") AS acl", []string{"acl"}, []string{"ACLITEM"}, want)
					step.Bag = true
					run(step)
				}
				absent(true)
				run(fixture.Exec("GRANT USAGE ON TYPE "+tc.name+" TO PUBLIC", 0))
				absent(false)
				entries(true)
				run(fixture.Exec("REVOKE USAGE ON TYPE "+tc.name+" FROM PUBLIC", 0))
				absent(false)
				entries(false)
				run(fixture.Exec("GRANT USAGE ON TYPE "+tc.name+" TO PUBLIC", 0))
				absent(false)
				entries(true)
			})
		})
	}
}
