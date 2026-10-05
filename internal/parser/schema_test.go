package parser

import (
	"reflect"
	"testing"
)

func TestSchemaGrammar(t *testing.T) {
	for _, q := range []string{"CREATE SCHEMA s", "CREATE SCHEMA s AUTHORIZATION CURRENT_USER", "CREATE SCHEMA AUTHORIZATION CURRENT_ROLE", "CREATE SCHEMA IF NOT EXISTS s", "CREATE SCHEMA IF NOT EXISTS AUTHORIZATION SESSION_USER", "CREATE SCHEMA s CREATE TABLE t(id INT) CREATE TABLE s.u(note TEXT)", "ALTER SCHEMA s RENAME TO next", "ALTER SCHEMA s OWNER TO CURRENT_USER", "DROP SCHEMA IF EXISTS s,next CASCADE", "DROP SCHEMA s,s RESTRICT", "SELECT current_schema", "SELECT current_schema()"} {
		ss, err := Parse(q)
		if err != nil || len(ss) != 1 {
			t.Fatal(q, ss, err)
		}
	}
	ss, err := Parse(`CREATE SCHEMA "a.b" AUTHORIZATION "current_user" CREATE TABLE "a.b"."t.u"(id INT)`)
	if err != nil {
		t.Fatal(err)
	}
	s := ss[0].(*CreateSchema)
	if s.Name != "a.b" || s.Authorization != "current_user" || s.AuthorizationIdentity || len(s.Tables) != 1 || len(s.Tables[0].Name.Parts) != 2 || s.Tables[0].Name.Parts[1].Text != "t.u" {
		t.Fatal(s)
	}
	for _, q := range []string{"CREATE SCHEMA s CREATE VIEW v AS SELECT id FROM t", "CREATE SCHEMA IF NOT EXISTS s CREATE TABLE t(id INT)", "CREATE SCHEMA s INSERT INTO t VALUES(1)", "ALTER SCHEMA s ADD x", "DROP SCHEMA s CASCADE nonsense"} {
		ss, err := Parse(q)
		if err == nil || ss != nil {
			t.Fatal("unsupported prefix accepted", q, ss, err)
		}
	}
	ss, err = Parse("CREATE SCHEMA s CREATE TABLE t(id INT); INSERT INTO s.t VALUES(7)")
	if err != nil || len(ss) != 2 {
		t.Fatal(ss, err)
	}
}
func TestSchemaSettingIdentifierLists(t *testing.T) {
	for _, tc := range []struct {
		q    string
		want []string
	}{{`SET search_path TO "a.b","a,b","a""b"`, []string{`"a.b"`, `"a,b"`, `"a""b"`}}, {`SET application_name TO "Mixed"`, []string{"Mixed"}}, {`SET search_path TO 'a,b'`, []string{`"a,b"`}}, {`SET search_path TO U&"\0061\002eb"`, []string{`"a.b"`}}, {`SET search_path TO "lowercase","A.B",public`, []string{"lowercase", `"A.B"`, "public"}}, {`SET search_path TO ''`, []string{`""`}}, {`SET "SEARCH_PATH" TO 'sp1,'`, []string{`"sp1,"`}}} {
		ss, err := Parse(tc.q)
		if err != nil || !reflect.DeepEqual(ss[0].(*Set).Values, tc.want) {
			t.Fatal(tc, ss, err)
		}
	}
}
