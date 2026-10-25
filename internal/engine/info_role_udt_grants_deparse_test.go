package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestInfoRoleUdtGrantsDefaultDeparse(t *testing.T) {
	e := New()
	read := e.CatalogRead()
	scope := &StatementScope{Inquiry: inquirySnapshot{CommentCatalog: read, deparseAuth: catalogAuthorization(e, read), DeparsePath: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, {ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}}}
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
	want := " SELECT grantor,\n    grantee,\n    udt_catalog,\n    udt_schema,\n    udt_name,\n    privilege_type,\n    is_grantable\n   FROM information_schema.udt_privileges\n  WHERE (((grantor)::name IN ( SELECT enabled_roles.role_name\n           FROM information_schema.enabled_roles)) OR ((grantee)::name IN ( SELECT enabled_roles.role_name\n           FROM information_schema.enabled_roles)));"
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
	for _, tc := range []struct {
		signature functions.DeparseSignature
		value     sqltype.Value
	}{{functions.DeparseViewOID, id}, {functions.DeparseViewText, sqltype.String(text, "information_schema.role_udt_grants")}} {
		value, err := table.Deparse(ctx, scope.deparseInputs(), tc.signature, [3]sqltype.Value{tc.value}, text)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Text()
		if err != nil || got != want {
			t.Fatalf("default builtin definition=%q err=%v want=%q", got, err, want)
		}
	}
	nonview, err := sqltype.OIDValue(oid, 1259)
	if err != nil {
		t.Fatal(err)
	}
	value, err := table.Deparse(ctx, scope.deparseInputs(), functions.DeparseViewOID, [3]sqltype.Value{nonview}, text)
	if err != nil || !value.IsNull() {
		t.Fatal("nonview must remain NULL", value, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	ctx.Context = canceled
	if _, err := table.Deparse(ctx, scope.deparseInputs(), functions.DeparseViewOID, [3]sqltype.Value{id}, text); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled builtin definition", err)
	}
}
