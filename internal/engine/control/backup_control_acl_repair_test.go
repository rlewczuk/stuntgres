package control

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestBackupControlACLRepairAdmission(t *testing.T) {
	parse := func(query string) *parser.Update {
		t.Helper()
		statements, err := parser.Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal("fixture syntax", query, err)
		}
		update, ok := statements[0].(*parser.Update)
		if !ok {
			t.Fatal("fixture statement", statements[0])
		}
		return update
	}
	for _, identity := range []string{
		"pg_catalog.pg_backup_start(text,boolean)",
		"pg_catalog.pg_backup_stop(boolean)",
	} {
		t.Run(identity, func(t *testing.T) {
			for _, value := range []struct {
				name, expression, text string
				absent                 bool
			}{
				{"null", "NULL", "", true},
				{"empty", `pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)`, "{}", false},
				{"owner", "'{postgres=X/postgres}'", "{postgres=X/postgres}", false},
				{"public", "'{=X/postgres,postgres=X/postgres}'", "{=X/postgres,postgres=X/postgres}", false},
				{"grant-option", "'{postgres=X/postgres,backup_reader=X*/postgres}'", "{postgres=X/postgres,backup_reader=X*/postgres}", false},
			} {
				t.Run(value.name, func(t *testing.T) {
					query := "UPDATE pg_catalog.pg_proc SET proacl=" + value.expression + "::pg_catalog.aclitem[] WHERE oid='" + identity + "'::pg_catalog.regprocedure"
					want := ObjectACLRepair{Class: ids.ProcedureClassID, Identity: identity, Text: value.text, Absent: value.absent}
					if got, ok := ParseObjectACLRepair(parse(query)); !ok || got != want {
						t.Fatal("canonical backup repair lost raw ACL state", query, got, want, ok)
					}
					// Raw empty STATE uses the fixed constructor, not a zero-dimensional
					// array cast. Backup identities receive no syntax exemption.
					for _, invalid := range []string{
						strings.Replace(query, value.expression+"::pg_catalog.aclitem[]", "'{}'::pg_catalog.aclitem[]", 1),
						strings.Replace(query, "::pg_catalog.aclitem[]", "::aclitem[]", 1),
						strings.Replace(query, "::pg_catalog.regprocedure", "::regprocedure", 1),
						strings.Replace(query, value.expression+"::pg_catalog.aclitem[]", "$1::pg_catalog.aclitem[]", 1),
						strings.Replace(query, "UPDATE pg_catalog.pg_proc", "UPDATE public.pg_proc", 1),
						strings.Replace(query, "proacl=", "proowner=", 1),
						strings.Replace(query, " WHERE", ",proowner=10 WHERE", 1),
						query + " OR TRUE",
						query + " RETURNING oid",
					} {
						if got, ok := ParseObjectACLRepair(parse(invalid)); ok {
							t.Fatal("noncanonical backup repair admitted", invalid, got)
						}
					}
				})
			}
		})
	}
}
