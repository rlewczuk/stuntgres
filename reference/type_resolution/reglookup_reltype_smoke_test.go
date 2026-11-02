package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegLookupRelTypeSmoke(t *testing.T) {
	var emitted []byte
	regLookupScenario(t, []string{"rl_smoke", "rl_left", "rl_right", "rl_store"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c, "CREATE SCHEMA rl_smoke", `CREATE TABLE rl_smoke."MiX"(v integer)`)
		regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, `SELECT to_regclass('rl_smoke."MiX"') AS c,to_regnamespace('rl_smoke') AS n,to_regtype('varchar(12)') AS t,to_regtypemod('varchar(12)') AS m,to_regclass('missing') AS missing,to_regtypemod(NULL::text) AS nullmod`, []string{"c", "n", "t", "m", "missing", "nullmod"}, []string{"REGCLASS", "REGNAMESPACE", "REGTYPE", "INT4", "REGCLASS", "INT4"}, [][]any{{`rl_smoke."MiX"`, "rl_smoke", "character varying", int64(16), nil, nil}}))
		regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, `SELECT to_regclass(NULL) AS c,to_regnamespace(NULL) AS n,to_regtype(NULL) AS t,to_regtypemod(NULL) AS m WHERE FALSE`, []string{"c", "n", "t", "m"}, []string{"REGCLASS", "REGNAMESPACE", "REGTYPE", "INT4"}, nil))
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regtype('varchar(12)')::oid::bigint AS oid,to_regtypemod('char') AS character_default,to_regtypemod('bit') AS bit_default,format_type(to_regtype('varchar(32)'),to_regtypemod('varchar(32)')) AS canonical`, []string{"oid", "character_default", "bit_default", "canonical"}, []string{"INT8", "INT4", "INT4", "TEXT"}, [][]any{{int64(1043), int64(5), int64(1), "character varying(32)"}}))
		q := "SELECT to_regtype($1) AS t,to_regtypemod($1) AS m,pg_typeof($1)::text AS input"
		held := regLookupHeld(t, p, c, q)
		defer held.Statement.Close()
		for _, tc := range []struct{ input, typ, mod any }{{"varchar(12)", "character varying", int64(16)}, {"rl_missing", nil, nil}, {nil, nil, nil}} {
			regObjectRunStep(t, p, b, held, regLookupRelTypeSelect(b, q, []string{"t", "m", "input"}, []string{"REGTYPE", "INT4", "TEXT"}, [][]any{{tc.typ, tc.mod, "text"}}, tc.input))
		}
		regObjectRunStep(t, p, b, held, regLookupError(q, "22023", "length for type varchar must be at least 1", "varchar(0)"))
		regObjectRunStep(t, p, b, held, regLookupRelTypeSelect(b, q, []string{"t", "m", "input"}, []string{"REGTYPE", "INT4", "TEXT"}, [][]any{{"character varying", int64(16), "text"}}, "varchar(12)"))
		regLookupRelTypeLiveChecks(t, p, b, c)
		regObjectExec(t, p, b, c, "DROP SCHEMA rl_left CASCADE", "DROP SCHEMA rl_right CASCADE", "DROP SCHEMA rl_smoke CASCADE")
		regLookupRelTypeCreateStored(t, p, b, c)
		regLookupRelTypeStoredChecks(t, p, b, c)
		regLookupRelTypeStoredControls(t, p, b, c, &emitted, false)
	})
}
