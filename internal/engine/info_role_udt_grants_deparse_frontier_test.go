package engine

import (
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestInfoRoleUdtGrantsDeparseFrontier(t *testing.T) {
	e := New()
	read := e.CatalogRead()
	scope := &StatementScope{Inquiry: inquirySnapshot{CommentCatalog: read, deparseAuth: catalogAuthorization(e, read), DeparsePath: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}}}
	old := scope.deparseInputs()
	scope.Output.QuoteAllIdentifiers = true
	fresh := scope.deparseInputs()
	text, ok := read.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("missing text")
	}
	oid, ok := read.LookupType(sqltype.Oid)
	if !ok {
		t.Fatal("missing oid")
	}
	id, err := sqltype.OIDValue(oid, uint32(pgcatalog.InfoRoleUdtGrantsOID))
	if err != nil {
		t.Fatal(err)
	}
	const plain = ` SELECT grantor,
    grantee,
    udt_catalog,
    udt_schema,
    udt_name,
    privilege_type,
    is_grantable
   FROM information_schema.udt_privileges
  WHERE (((grantor)::name IN ( SELECT enabled_roles.role_name
           FROM information_schema.enabled_roles)) OR ((grantee)::name IN ( SELECT enabled_roles.role_name
           FROM information_schema.enabled_roles)));`
	const quoted = ` SELECT "grantor",
    "grantee",
    "udt_catalog",
    "udt_schema",
    "udt_name",
    "privilege_type",
    "is_grantable"
   FROM "information_schema"."udt_privileges"
  WHERE ((("grantor")::"name" IN ( SELECT "enabled_roles"."role_name"
           FROM "information_schema"."enabled_roles")) OR (("grantee")::"name" IN ( SELECT "enabled_roles"."role_name"
           FROM "information_schema"."enabled_roles")));`
	for _, frontier := range []struct {
		inputs table.DeparseInputs
		want   string
	}{{old, plain}, {fresh, quoted}, {old, plain}} {
		for _, call := range []struct {
			signature functions.DeparseSignature
			value     sqltype.Value
		}{{functions.DeparseViewOID, id}, {functions.DeparseViewText, sqltype.String(text, "information_schema.role_udt_grants")}} {
			ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
			value, err := table.Deparse(ctx, frontier.inputs, call.signature, [3]sqltype.Value{call.value}, text)
			if err != nil {
				t.Fatal(err)
			}
			got, err := value.Text()
			if err != nil || got != frontier.want {
				t.Fatalf("definition=%q err=%v want=%q", got, err, frontier.want)
			}
		}
	}
}
