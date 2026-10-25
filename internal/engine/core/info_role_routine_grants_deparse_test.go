package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestInfoRoleRoutineGrantsDefaultDeparse(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	scope := &core.StatementScope{Inquiry: core.NewInquiry(core.InquiryFields{CommentCatalog: read, DeparseAuth: catalogAuthorization(e, read), DeparsePath: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, {ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}})}
	text, ok := read.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("missing text")
	}
	oid, ok := read.LookupType(sqltype.Oid)
	if !ok {
		t.Fatal("missing oid")
	}
	id, err := sqltype.OIDValue(oid, uint32(pgcatalog.InfoRoleRoutineGrantsOID))
	if err != nil {
		t.Fatal(err)
	}
	want := " SELECT grantor,\n    grantee,\n    specific_catalog,\n    specific_schema,\n    specific_name,\n    routine_catalog,\n    routine_schema,\n    routine_name,\n    privilege_type,\n    is_grantable\n   FROM information_schema.routine_privileges\n  WHERE (((grantor)::name IN ( SELECT enabled_roles.role_name\n           FROM information_schema.enabled_roles)) OR ((grantee)::name IN ( SELECT enabled_roles.role_name\n           FROM information_schema.enabled_roles)));"
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
	for _, tc := range []struct {
		signature functions.DeparseSignature
		value     sqltype.Value
	}{{functions.DeparseViewOID, id}, {functions.DeparseViewText, sqltype.String(text, "information_schema.role_routine_grants")}} {
		value, err := table.Deparse(ctx, core.ScopeDeparseInputs(scope), tc.signature, [3]sqltype.Value{tc.value}, text)
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
	value, err := table.Deparse(ctx, core.ScopeDeparseInputs(scope), functions.DeparseViewOID, [3]sqltype.Value{nonview}, text)
	if err != nil || !value.IsNull() {
		t.Fatal("nonview must remain NULL", value, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	ctx.Context = canceled
	if _, err := table.Deparse(ctx, core.ScopeDeparseInputs(scope), functions.DeparseViewOID, [3]sqltype.Value{id}, text); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled builtin definition", err)
	}
}

func TestInfoRoleRoutineGrantsDeparseFrontier(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	pg := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	identity, ok := catalog.BuiltinRoutineIdentityBySignature("pg_get_userbyid", "26")
	if !ok {
		t.Fatal("missing released builtin identity")
	}
	original, ok := base.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID})
	if !ok {
		t.Fatal("missing real builtin descriptor")
	}
	viewAddress, ok := base.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: pg, Name: "pg_views"})
	if !ok {
		t.Fatal("missing pg_views")
	}
	oid, ok := base.LookupType(sqltype.Oid)
	if !ok {
		t.Fatal("missing oid")
	}
	text, ok := base.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("missing text")
	}
	target, err := sqltype.OIDValue(oid, uint32(viewAddress.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	oldScope := &core.StatementScope{Inquiry: core.NewInquiry(core.InquiryFields{CommentCatalog: base, DeparseAuth: catalogAuthorization(e, base), DeparsePath: []ids.ObjectAddress{pg}})}
	oldInputs := core.ScopeDeparseInputs(oldScope)
	const oldDefinition = ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    pg_get_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`
	const freshDefinition = ` SELECT "n"."nspname" AS "schemaname",
    "c"."relname" AS "viewname",
    "public"."rrg_identity_userbyid"("c"."relowner") AS "viewowner",
    "pg_get_viewdef"("c"."oid") AS "definition"
   FROM ("pg_class" "c"
     LEFT JOIN "pg_namespace" "n" ON (("n"."oid" = "c"."relnamespace")))
  WHERE ("c"."relkind" = 'v'::"char");`
	check := func(in table.DeparseInputs, want string) {
		t.Helper()
		ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
		for _, tc := range []struct {
			signature functions.DeparseSignature
			value     sqltype.Value
		}{
			{functions.DeparseViewOID, target},
			{functions.DeparseViewText, sqltype.String(text, "pg_catalog.pg_views")},
		} {
			value, err := table.Deparse(ctx, in, tc.signature, [3]sqltype.Value{tc.value}, text)
			if err != nil {
				t.Fatal(err)
			}
			got, err := value.Text()
			if err != nil || got != want {
				t.Fatalf("captured definition=%q err=%v want=%q", got, err, want)
			}
		}
	}
	check(oldInputs, oldDefinition)
	next := catalog.CloneState(base.State())
	current := *original
	current.ObjectName = "rrg_identity_userbyid"
	current.ObjectNamespace = public
	current.ObjectRevision++
	next.UnbindName(catalog.DescriptorNameKey(original))
	next.SetDescriptor(current.Address(), &current)
	next.BindName(catalog.DescriptorNameKey(&current), current.Address())
	index := catalog.ProcedureIndexEdit{Index: next.ProcedureNames()}
	index.Replace(original, &current)
	next.SetProcedureNames(index.Index)
	e.Catalog.PublishLocked(next)
	// A captured inquiry must retain both the old read and copied presentation
	// even after publication and mutation of the scope that supplied them.
	oldScope.Output.QuoteAllIdentifiers = true
	fresh := e.CatalogRead()
	freshScope := &core.StatementScope{Inquiry: core.NewInquiry(core.InquiryFields{CommentCatalog: fresh, DeparseAuth: catalogAuthorization(e, fresh), DeparsePath: []ids.ObjectAddress{pg}})}
	freshScope.Output.QuoteAllIdentifiers = true
	freshInputs := core.ScopeDeparseInputs(freshScope)
	check(oldInputs, oldDefinition)
	check(freshInputs, freshDefinition)
	check(oldInputs, oldDefinition)
}
