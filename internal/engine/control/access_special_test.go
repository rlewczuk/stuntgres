package control

import (
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestAccessSpecialACLRepairAdmission(t *testing.T) {
	valid := "UPDATE pg_catalog.pg_parameter_acl SET paracl='{reader=s/postgres}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text"
	for _, tc := range []struct {
		sql  string
		want bool
	}{
		{valid, true},
		{strings.Replace(valid, "'{reader=s/postgres}'", emptyObjectACLExpression, 1), true},
		{strings.Replace(valid, "'lc_messages'::pg_catalog.text", "$1::pg_catalog.text", 1), false},
		{strings.Replace(valid, "'lc_messages'::pg_catalog.text", "lower('lc_messages')::pg_catalog.text", 1), false},
		{strings.Replace(valid, "::pg_catalog.text", "::pg_catalog.name", 1), false},
		{valid + " OR true", false}, {valid + " RETURNING parname", false},
		{strings.Replace(valid, " WHERE", ",parname='x' WHERE", 1), false},
		{strings.Replace(valid, "'{reader=s/postgres}'", "$1", 1), false},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.sql, err)
		}
		repair, ok := ParseObjectACLRepair(statements[0].(*parser.Update))
		if ok != tc.want {
			t.Fatal(tc.sql, ok)
		}
		if ok && (repair.Class != ids.ParameterClassID || repair.Identity != "lc_messages" || repair.Owner || repair.Absent) {
			t.Fatal("parameter repair identity", repair)
		}
	}
}
