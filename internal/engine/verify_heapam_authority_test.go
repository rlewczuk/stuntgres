package engine

import (
	"context"
	"strings"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestVerifyHeapamAuthority(t *testing.T) {
	e, _, _ := typedUnitCatalog(t, false)
	ext := heapUnitInstalled(t, e)
	view := e.CatalogRead()
	p := view.MustProcedure(ext.Members[0])
	n := heapUnitExpression(t, e)
	control := extension.StockAmcheck.Control()
	if control.Version != "1.5" || control.Trusted || !control.Superuser || !control.Relocatable || control.Comment == nil || *control.Comment != "functions for verifying relation integrity" { t.Fatal("control", control) }
	if p.Signature != extension.VerifyHeapamSignature || p.Result != 2249 || p.Strict || p.Volatility != expr.Volatile || !procedureReturnsSet(p) || p.Installed.Defaults != 5 || p.Installed.Parallel != 'u' || len(ext.Members) != 1 { t.Fatal("installed declaration", p) }
	if !p.ACL.Explicit || len(p.ACL.Entries) != 1 || p.ACL.Entries[0].Grantee.Public || p.ACL.Entries[0].Grantee.Role != p.Owner || p.ACL.Entries[0].Privileges != authorization.ExecutePrivilege { t.Fatal("initial owner-only ACL", p.ACL) }
	if err := stored.ValidateInstalledVerifyHeapam(n, view); err != nil { t.Fatal(err) }
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Strict = true },
		func(p *catalog.Procedure) { p.Signature = "23" },
		func(p *catalog.Procedure) { p.Result = 16 },
		func(p *catalog.Procedure) { p.Volatility = expr.Stable },
		func(p *catalog.Procedure) { p.ObjectRevision = 0 },
		func(p *catalog.Procedure) { p.Implementation = new(extension.AmcheckProvider) },
		func(p *catalog.Procedure) { p.Extension = ids.ObjectAddress{} },
		func(p *catalog.Procedure) { p.Installed.Defaults = 0 },
		func(p *catalog.Procedure) { p.Installed.ReturnsSet = false },
		func(p *catalog.Procedure) { p.Installed.Parallel = 's' },
	} {
		state := catalog.CloneState(view.State())
		bad := *p
		mutate(&bad)
		state.SetDescriptor(p.Address(), &bad)
		if err := extension.StockAmcheck.ValidateExtension(e.Catalog.Read(state), ext); err == nil { t.Fatal("forged member admitted", bad) }
	}
	for _, members := range [][]ids.ObjectAddress{nil, {p.Address(), p.Address()}, {{ClassID: ids.ProcedureClassID, ObjectID: p.Address().ObjectID+1}}} {
		state := catalog.CloneState(view.State())
		bad := *ext
		bad.Members = members
		state.SetDescriptor(ext.Address(), &bad)
		if err := extension.StockAmcheck.ValidateExtension(e.Catalog.Read(state), &bad); err == nil { t.Fatal("forged manifest admitted", members) }
	}
	for _, bad := range []plan.InstalledVerifyHeapamExpr{
		func() plan.InstalledVerifyHeapamExpr { bad := n; bad.Ref.Epoch++; return bad }(),
		func() plan.InstalledVerifyHeapamExpr { bad := n; bad.Ref = New().Catalog.Domain.Ref(n.Ref.Epoch, n.Ref.Address); return bad }(),
		func() plan.InstalledVerifyHeapamExpr { bad := n; stale := *p; stale.ObjectAddress.ObjectID++; bad.Procedure, bad.RoutineReference = &stale, &stale; return bad }(),
		func() plan.InstalledVerifyHeapamExpr { bad := n; bad.Args[0] = n.Args[1]; return bad }(),
		func() plan.InstalledVerifyHeapamExpr { bad := n; bad.Usage = testUsage(e, sqltype.Bool); return bad }(),
	} {
		if err := stored.ValidateInstalledVerifyHeapam(bad, view); err == nil { t.Fatal("foreign/stale/wrong-shape expression admitted", bad) }
	}
	// A genuine identity remap changes extension/procedure addresses and epoch,
	// while preserving the source carrier, canonical defaults and OUT types.
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}, p.Address(): {ClassID: ids.ProcedureClassID, ObjectID: 900001}}
	state := catalog.CloneState(view.State())
	for old := range mapping { state.UnbindName(catalog.DescriptorNameKey(view.Get(old))); state.RemoveDescriptor(old) }
	for old, destination := range mapping {
		d := extension.RemapDescriptor(view.Get(old), mapping)
		state.SetDescriptor(destination, d)
		state.BindName(catalog.DescriptorNameKey(d), destination)
	}
	state.SetEpoch(state.Epoch()+1)
	destination := e.Catalog.Read(state)
	remap := func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch = state.Epoch(); if address, ok := mapping[ref.Address]; ok { ref.Address = address }; return ref }
	mapper := stored.ViewQueryRemapper{Catalog: destination, Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(mapping, nil, nil)}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "findings", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
	mapped, err := mapper.Query(query)
	if err != nil { t.Fatal(err) }
	m := mapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledVerifyHeapamExpr)
	if m.Ref != remap(n.Ref) || !exec.SameRoutineDeclaration(destination.MustProcedure(m.Ref.Address), m.Procedure) || n.Procedure != view.Get(n.Ref.Address) { t.Fatal("remapped authority", m) }
	if err := stored.ValidateInstalledVerifyHeapam(m, destination); err != nil { t.Fatal(err) }
	text, err := (stored.Deparser{View: destination}).SQL(context.Background(), m, stored.DeparseParent{})
	if err != nil || !strings.Contains(text, "verify_heapam") || !strings.Contains(text, "none") { t.Fatal("default-preserving deparse", text, err) }
	state.RemoveDescriptor(m.Ref.Address)
	if err := stored.ValidateInstalledVerifyHeapam(m, destination); err == nil { t.Fatal("removed identity revived") }
}

func TestVerifyHeapamAuthorityCatalogKinds(t *testing.T) {
	e, _, _ := typedUnitCatalog(t, false)
	heapUnitInstalled(t, e)
	n := heapUnitExpression(t, e)
	view := e.CatalogRead()
	reader, _ := blockingUnitOwner(t, e)
	scope := pgrowlocksUnitScope(t, e, reader)
	for _, tc := range []struct { oid ids.OID; name, code, message, detail string }{
		{1259, "pg_class", "0A000", "verify_heapam requires stored heap data", ""},
		{12081, "pg_available_extensions", "42809", `cannot check relation "pg_available_extensions"`, "This operation is not supported for views."},
		{12003, "pg_tables", "42809", `cannot check relation "pg_tables"`, "This operation is not supported for views."},
	} {
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tc.oid})
		if d.Name() != tc.name || d.RelationKind != catalog.CatalogRelation { t.Fatal("not an actual catalog execution descriptor", d) }
		for _, physical := range []bool{false, true} {
			args := heapUnitArgs(t, e, d, false)
			args[2] = sqltype.Boolean(testUsage(e, sqltype.Bool).Type, physical)
			_, err := (*statementHost)(scope).VerifyHeapam(args, n.Usage.Type)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
			if !reflect.DeepEqual(err, want) { t.Fatal("projected kind/availability precedence", tc.name, physical, err, want) }
		}
	}
}
