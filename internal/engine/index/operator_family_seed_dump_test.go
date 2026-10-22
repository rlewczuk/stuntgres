package index

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestOperatorFamilySeedDumpOwnerDelta(t *testing.T) {
	domain := &authorization.Domain{}
	state := catalog.NewState(1)
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectName: "pg_catalog", ObjectRevision: 1}
	state.SetDescriptor(namespace.Address(), namespace)
	state.BindName(catalog.DescriptorNameKey(namespace), namespace.Address())
	catalog.InstallBuiltinAccessMethods(state)
	handlers := routine.NativeAccessMethodProcedures(domain.Principal(10))
	for i := range handlers {
		state.SetDescriptor(handlers[i].Address(), &handlers[i])
		state.BindName(catalog.DescriptorNameKey(&handlers[i]), handlers[i].Address())
	}
	catalog.InstallBuiltinFamilies(state, domain.Principal(10))
	base := catalog.NewRead(nil, state)
	original, ok := base.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 1976})
	if !ok {
		t.Fatal("missing integer family")
	}
	renamed := *original
	renamed.ObjectName = "renamed_integer_family"
	renamed.ObjectRevision++
	candidate := catalog.NewCandidate(base)
	if err := candidate.Replace(&renamed); err != nil {
		t.Fatal(err)
	}
	view := base.WithState(candidate.State())
	// A name-only delta needs no owner lookup or synthetic owner transfer.
	stages, finals, err := OperatorFamilySeedDumpStatements(context.Background(), view, authorization.Read{}, []catalog.Descriptor{&renamed})
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 1 || stages[0] != "ALTER OPERATOR FAMILY \"pg_catalog\".\"integer_ops\" USING btree RENAME TO \"__stuntgres_family_seed_1976\";\n" {
		t.Fatal("seed staging", stages)
	}
	final, ok := finals[1976]
	if !ok || len(finals) != 1 || final.Owner != "postgres" || final.SQL != "ALTER OPERATOR FAMILY \"pg_catalog\".\"__stuntgres_family_seed_1976\" USING btree RENAME TO \"renamed_integer_family\";\n" {
		t.Fatal("name-only seed finalization", finals)
	}
	if original.Name() != "integer_ops" || original.Owner != domain.Principal(10) {
		t.Fatal("dump changed source seed", original)
	}
}
