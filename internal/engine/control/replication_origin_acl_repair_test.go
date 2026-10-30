package control

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestReplicationOriginStateACLRepairAdmission(t *testing.T) {
	for _, identity := range []string{
		"pg_catalog.pg_replication_origin_create(text)",
		"pg_catalog.pg_replication_origin_drop(text)",
		"pg_catalog.pg_replication_origin_oid(text)",
		"pg_catalog.pg_replication_origin_advance(text, pg_lsn)",
		"pg_catalog.pg_replication_origin_progress(text, boolean)",
	} {
		t.Run(identity, func(t *testing.T) {
			parse := func(query string) *parser.Update {
				t.Helper()
				statements, err := parser.Parse(query)
				if err != nil || len(statements) != 1 {
					t.Fatal("fixture syntax", query, err)
				}
				return statements[0].(*parser.Update)
			}
			for _, value := range []struct {
				expression, text string
				absent           bool
			}{
				{"NULL", "", true},
				{emptyObjectACLExpression, "{}", false},
				{"E'{postgres=X/postgres,state_origin_user=X/postgres}'", "{postgres=X/postgres,state_origin_user=X/postgres}", false},
			} {
				query := "UPDATE pg_catalog.pg_proc SET proacl=" + value.expression + "::pg_catalog.aclitem[] WHERE oid=E'" + identity + "'::pg_catalog.regprocedure"
				want := ObjectACLRepair{Class: ids.ProcedureClassID, Identity: identity, Text: value.text, Absent: value.absent}
				if got, ok := ParseObjectACLRepair(parse(query)); !ok || got != want {
					t.Fatal("canonical repair lost exact raw authority", query, got, want, ok)
				}
				for _, invalid := range []string{
					query + " OR TRUE",
					query + " RETURNING oid",
					strings.Replace(query, "::pg_catalog.aclitem[]", "::aclitem[]", 1),
					strings.Replace(query, value.expression+"::pg_catalog.aclitem[]", "'{}'::pg_catalog.aclitem[]", 1),
					strings.Replace(query, value.expression+"::pg_catalog.aclitem[]", "$1::pg_catalog.aclitem[]", 1),
					strings.Replace(query, " WHERE", ",proowner=10 WHERE", 1),
					strings.Replace(query, "UPDATE pg_catalog.pg_proc", "UPDATE public.pg_proc", 1),
				} {
					if got, ok := ParseObjectACLRepair(parse(invalid)); ok {
						t.Fatal("noncanonical repair admitted", invalid, got)
					}
				}
			}
		})
	}
}
