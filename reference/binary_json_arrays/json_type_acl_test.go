package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const jsonTypeReader = "jt_type_reader"

func jsonTypeACL(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE ROLE "+jsonTypeReader, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jt_acl(j json,b jsonb)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jt_acl VALUES('{"a":1}','{"b":2}'),('[1,2]','[3]')`, 2))
	// Make the relation namespace visible without granting any table authority.
	// Native schema visibility uses ownership; the table remains bootstrap-owned.
	if b.PG {
		jsonTypeRun(t, p, b, c, fixture.Exec("GRANT USAGE ON SCHEMA public TO "+jsonTypeReader, 0))
	} else {
		jsonTypeRun(t, p, b, c, fixture.Exec("ALTER SCHEMA public OWNER TO "+jsonTypeReader, 0))
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION "+jsonTypeReader, 0))
	defer func() {
		if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Error(err)
			return
		}
		if !b.PG {
			if _, err := c.ExecContext(p.Ctx, "ALTER SCHEMA public OWNER TO CURRENT_USER"); err != nil {
				t.Error(err)
			}
		}
	}()
	jsonTypeRun(t, p, b, c, fixture.Select("SELECT current_user AS identity", []string{"identity"}, []string{"NAME"}, [][]any{{jsonTypeReader}}))
	for _, family := range []string{"json", "jsonb"} {
		column := "j"
		if family == "jsonb" {
			column = "b"
		}
		for _, suffix := range []string{"typeof", "array_length", "object_keys"} {
			for _, tail := range []string{"", " WHERE FALSE"} {
				jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT "+family+"_"+suffix+"("+column+") FROM jt_acl"+tail, "42501", "permission denied for table jt_acl", 0))
			}
		}
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("GRANT SELECT(j) ON jt_acl TO "+jsonTypeReader, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION "+jsonTypeReader, 0))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT json_typeof(j) AS k FROM jt_acl ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"array"}, {"object"}}))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT json_object_keys(j) AS k FROM jt_acl WHERE json_typeof(j)='object'", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}}))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT json_array_length(j) AS n FROM jt_acl WHERE json_typeof(j)='array'", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
	for _, suffix := range []string{"typeof", "array_length", "object_keys"} {
		jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT jsonb_"+suffix+"(b) FROM jt_acl", "42501", "permission denied for table jt_acl", 0))
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT j::text AS j,b::text AS b FROM jt_acl ORDER BY j::text", []string{"j", "b"}, []string{"TEXT", "TEXT"}, [][]any{{`[1,2]`, `[3]`}, {`{"a":1}`, `{"b": 2}`}}))
}

func TestReferenceJSONTypeACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{jsonTypeReader})
	jsonTypeScenario(t, jsonTypeACL)
}
