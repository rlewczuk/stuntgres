package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// aclValuesScenario keeps the existing physical-session and cleanup convention.
func aclValuesScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	aclitemScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		body(t, p, b, c)
	})
}

func aclValuesOwner(pg bool) string {
	if pg {
		return "stuntgres"
	}
	return "postgres"
}

func aclValuesOID(pg bool, id int64) any {
	if pg {
		return strconv.FormatInt(id, 10)
	}
	return id
}

func aclValuesExplode(q string, rows [][]any) fixture.Step {
	s := fixture.Select(q, []string{"grantor", "grantee", "privilege_type", "is_grantable"}, []string{"OID", "OID", "TEXT", "BOOL"}, rows)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.ScalarAssertDescription(t, columns, s.Columns, s.Types)
	}
	return s
}

func aclValuesRows(pg bool, grantor, grantee int64, privileges string, grantable bool) [][]any {
	var rows [][]any
	for _, privilege := range strings.Split(privileges, ",") {
		if privilege != "" {
			rows = append(rows, []any{aclValuesOID(pg, grantor), aclValuesOID(pg, grantee), privilege, grantable})
		}
	}
	return rows
}

func TestReferenceACLValuesDefaults(t *testing.T) {
	aclValuesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		owner := aclValuesOwner(b.PG)
		for _, tc := range []struct{ code, letters, privileges, publicLetters, publicPrivileges string }{
			{"c", "", "", "", ""},
			{"r", "arwdDxtm", "INSERT,SELECT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN", "", ""},
			{"s", "rwU", "SELECT,UPDATE,USAGE", "", ""},
			{"d", "CTc", "CREATE,TEMPORARY,CONNECT", "Tc", "TEMPORARY,CONNECT"},
			{"f", "X", "EXECUTE", "X", "EXECUTE"},
			{"l", "U", "USAGE", "U", "USAGE"},
			{"L", "rw", "SELECT,UPDATE", "", ""},
			{"n", "UC", "USAGE,CREATE", "", ""},
			{"p", "sA", "SET,ALTER SYSTEM", "", ""},
			{"t", "C", "CREATE", "", ""},
			{"F", "U", "USAGE", "", ""},
			{"S", "U", "USAGE", "", ""},
			{"T", "U", "USAGE", "U", "USAGE"},
		} {
			t.Run(tc.code, func(t *testing.T) {
				var entries []string
				if tc.publicLetters != "" {
					entries = append(entries, "="+tc.publicLetters+"/"+owner)
				}
				if tc.letters != "" {
					entries = append(entries, owner+"="+tc.letters+"/"+owner)
				}
				want := "{" + strings.Join(entries, ",") + "}"
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT acldefault($1::\"char\",10) AS a", []string{"a"}, []string{"_ACLITEM"}, [][]any{{want}}, tc.code)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					rows := aclValuesRows(b.PG, 10, 10, tc.privileges, false)
					rows = append(rows, aclValuesRows(b.PG, 10, 0, tc.publicPrivileges, false)...)
					s = aclValuesExplode("SELECT * FROM aclexplode(acldefault('"+tc.code+"',10))", rows)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		for _, id := range []int64{0, 4294967295} {
			t.Run(strconv.FormatInt(id, 10), func(t *testing.T) {
				n := strconv.FormatInt(id, 10)
				grantee := n
				if id == 0 {
					grantee = ""
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT acldefault('r',$1::oid) AS a", []string{"a"}, []string{"_ACLITEM"}, [][]any{{"{" + grantee + "=arwdDxtm/" + n + "}"}}, id))
				s := aclValuesExplode("SELECT * FROM aclexplode(acldefault('r',$1::oid))", aclValuesRows(b.PG, id, id, "INSERT,SELECT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN", false))
				s.Args = []any{id}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
	})
}

func TestReferenceACLValuesConstruction(t *testing.T) {
	aclValuesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		owner := aclValuesOwner(b.PG)
		for _, tc := range []struct{ input, letters string }{
			{" select, INSERT, update ,DELETE ", "arwd"},
			{"INSERT,SELECT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,EXECUTE,USAGE,CREATE,TEMPORARY,CONNECT,SET,ALTER SYSTEM,MAINTAIN", "arwdDxtXUCTcsAm"},
			{"TEMP,tempORary,TEMP", "T"},
			{"\t\n SELECT \r\v\f", "r"},
		} {
			for _, grantable := range []bool{false, true} {
				letters := tc.letters
				if grantable {
					letters = strings.Join(strings.Split(letters, ""), "*") + "*"
				}
				text := owner + "=" + letters + "/" + owner
				for _, prepared := range []bool{false, true} {
					s := aclitemSelect("SELECT makeaclitem(10,10,$1::text,$2::bool) AS a", [][]any{{text}}, tc.input, grantable)
					s.Prepared = prepared
					run(s)
					s = fixture.Select("SELECT makeaclitem(10,10,$1::text,$2::bool)=$3::aclitem AS same", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, tc.input, grantable, text)
					s.Prepared = prepared
					run(s)
				}
			}
		}
		for _, tc := range []struct{ q, text string }{
			{"makeaclitem(0,0,'SELECT',false)", "=r/0"},
			{"makeaclitem(0,0,'SELECT',true)", "=r*/0"},
			{"makeaclitem(4294967295,4294967295,'SELECT',false)", "4294967295=r/4294967295"},
		} {
			run(aclitemSelect("SELECT "+tc.q+" AS a", [][]any{{tc.text}}))
		}
		for _, call := range []string{"makeaclitem(NULL,10,'INVALID',false)", "makeaclitem(10,NULL,'INVALID',false)", "makeaclitem(10,10,NULL,false)", "makeaclitem(10,10,'INVALID',NULL)"} {
			run(aclitemSelect("SELECT "+call+" AS a", [][]any{{nil}}))
		}
		for _, call := range []string{"acldefault(NULL,10)", "acldefault('z',NULL)"} {
			run(fixture.Select("SELECT "+call+" AS a", []string{"a"}, []string{"_ACLITEM"}, [][]any{{nil}}))
		}
		run(fixture.Exec("CREATE TABLE acl_values_constructed(id INT,a aclitem)", 0))
		run(fixture.Exec("INSERT INTO acl_values_constructed VALUES (1,makeaclitem(10,10,'SELECT',false)),(2,makeaclitem(0,0,'SELECT',true))", 2))
		run(fixture.Select("SELECT id,a FROM acl_values_constructed ORDER BY id", []string{"id", "a"}, []string{"INT4", "ACLITEM"}, [][]any{{int64(1), owner + "=r/" + owner}, {int64(2), "=r*/0"}}))
		q := "SELECT makeaclitem(10,10,$1::text,$2::bool) AS a"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, s := range []fixture.Step{
			aclitemSelect(q, [][]any{{owner + "=r/" + owner}}, "SELECT", false),
			aclitemSelect(q, [][]any{{nil}}, nil, true),
			fixture.ErrorDiagnostic(q, aclitemDiagnostic("22023", `unrecognized privilege type: "bad"`), "bad", false),
			aclitemSelect(q, [][]any{{owner + "=w*/" + owner}}, "UPDATE", true),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}
