package schemacmds_test

import (
	"strings"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/sqltype"
	"testing"
)

func ltxtqueryRenamedOwner(t *testing.T) {
	t.Helper()
	e, op, ext, _ := lqueryProviderInstalled(t, false)
	for _, q := range []string{"CREATE SCHEMA lx_owner", "ALTER TYPE public.ltxtquery RENAME TO label_query", "ALTER TYPE public.label_query SET SCHEMA lx_owner"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	roles, err := extension.InstalledTypes(view, ext)
	if err != nil || len(roles) != 3 || roles[2].Name != "ltxtquery" || roles[2].Type.Name() != "label_query" {
		t.Fatal("renamed canonical role", roles, err)
	}
	metadata, err := extension.OwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	record = extensionOwnerRecipeOrder(record)
	if err != nil || !found || len(record.Members) != len(ext.Installation.Members)+3 {
		t.Fatal("renamed owner recipe", err)
	}
	if len(ext.Installation.Members) != extension.LtreeMemberCount {
		t.Fatal("ltree provider recipe count", len(ext.Installation.Members))
	}
	for _, bound := range []struct {
		name      string
		got, want int
	}{
		{"comparison procedure start", extension.LtreeComparisonProcedureStart, 41},
		{"hierarchy procedure start", extension.LtreeHierarchyProcedureStart, 60},
		{"hierarchy member end", extension.LtreeHierarchyMemberEnd, 64},
		{"lquery member end", extension.LtreeLqueryMemberEnd, 90},
		{"ltxtquery operator start", extension.LtreeLtxtqueryOperatorStart, 92},
		{"member count", extension.LtreeMemberCount, 94},
	} {
		if bound.got != bound.want {
			t.Fatal("ltree provider numeric bound", bound.name, bound.got, bound.want)
		}
	}
	for _, bound := range []struct {
		name      string
		got, want int
	}{
		{"array procedure start", extension.LtreeArrayProcedureStart, 9},
		{"array operator start", extension.LtreeArrayOperatorStart, extension.LtreeArrayProcedureStart + int(sqltype.LtreeArrayOperationCount)},
		{"comparison procedure start", extension.LtreeComparisonProcedureStart, extension.LtreeArrayOperatorStart + int(sqltype.LtreeArrayOperationCount)},
		{"hierarchy procedure start", extension.LtreeHierarchyProcedureStart, extension.LtreeHashClassSlot + 1},
		{"hierarchy operator start", extension.LtreeHierarchyOperatorStart, extension.LtreeHierarchyProcedureStart + int(sqltype.LtreeHierarchyOperationCount)},
		{"hierarchy member end", extension.LtreeHierarchyMemberEnd, extension.LtreeHierarchyOperatorStart + int(sqltype.LtreeHierarchyOperationCount)},
		{"path procedure start", extension.LtreePathProcedureStart, extension.LtreeHierarchyMemberEnd},
		{"path operator start", extension.LtreePathOperatorStart, extension.LtreePathProcedureStart + 19},
		{"path member end", extension.LtreePathMemberEnd, extension.LtreePathOperatorStart + 3},
		{"lquery procedure start", extension.LtreeLqueryProcedureStart, extension.LtreePathMemberEnd},
		{"lquery operator start", extension.LtreeLqueryOperatorStart, extension.LtreeLqueryProcedureStart + 2},
		{"lquery member end", extension.LtreeLqueryMemberEnd, extension.LtreeLqueryOperatorStart + 2},
		{"ltxtquery procedure start", extension.LtreeLtxtqueryProcedureStart, extension.LtreeLqueryMemberEnd},
		{"ltxtquery operator start", extension.LtreeLtxtqueryOperatorStart, extension.LtreeLtxtqueryProcedureStart + 2},
		{"member count", extension.LtreeMemberCount, extension.LtreeLtxtqueryOperatorStart + 2},
	} {
		if bound.got != bound.want {
			t.Fatal("ltree provider structural bound", bound.name, bound.got, bound.want)
		}
	}
	if record.Members[8].Signature != "lx_owner.label_query" {
		t.Fatal("output signature lost current portable identity", record.Members[8])
	}
	for _, role := range []struct {
		slot                  int
		kind, name, signature string
	}{
		{extension.LtreeBtreeFamilySlot, "operator_family", "ltree_ops", "btree"},
		{extension.LtreeBtreeClassSlot, "operator_class", "ltree_ops", "btree"},
		{extension.LtreeHashFamilySlot, "operator_family", "hash_ltree_ops", "hash"},
		{extension.LtreeHashClassSlot, "operator_class", "hash_ltree_ops", "hash"},
	} {
		member := record.Members[role.slot]
		if member.Declaration == nil || *member.Declaration != role.slot || member.Kind != role.kind || member.Name != role.name || member.Signature != role.signature || member.Owner != "postgres" {
			t.Fatal("renaming scalar changed comparison class/family role", member)
		}
	}
}
