package schemacmds_test

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestLtreeLtxtqueryProviderOwners(t *testing.T) {
	source, _, se, _ := lqueryProviderInstalled(t, false)
	target, _, te, _ := lqueryProviderInstalled(t, true)
	sv, tv := source.CatalogRead(), target.CatalogRead()
	metadata, err := extension.OwnerMetadata(sv, source.RoleStore.PublishedRead(&source.Catalog.Owner, sv), se)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	if err != nil || !found || len(record.Members) != 97 {
		t.Fatal("complete owner encoding", err)
	}
	if _, err := extension.ImportOwners(context.Background(), sv.WithState(catalog.CloneState(sv.State())), source.RoleStore.PublishedRead(&source.Catalog.Owner, sv), []extension.OwnerRecord{record}); err != nil {
		t.Fatal("genuine v3 owner import", err)
	}
	record = extensionOwnerRecipeOrder(record)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{se.Address(): te.Address()}
	for i, a := range se.Installation.Members {
		mapping[a] = te.Installation.Members[i]
	}
	for _, slot := range []int{0, 3, 6} {
		a, b := sv.MustType(se.Installation.Members[slot]), tv.MustType(te.Installation.Members[slot])
		mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(a.Array)}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(b.Array)}
	}
	for i, signature := range []string{"public.ltree, public.ltxtquery", "public.ltxtquery, public.ltree"} {
		for _, slot := range []int{extension.LtreeLtxtqueryProcedureStart + i, extension.LtreeLtxtqueryOperatorStart + i} {
			member := record.Members[slot]
			if member.Declaration == nil || *member.Declaration != slot || member.Signature != signature || member.Owner != "postgres" {
				t.Fatal("mixed symbolic owner role", member)
			}
		}
		old := sv.MustProcedure(se.Installation.Members[extension.LtreeLtxtqueryProcedureStart+i])
		saved := *old
		next := extension.RemapDescriptor(old, mapping).(*catalog.Procedure)
		want := tv.MustProcedure(te.Installation.Members[extension.LtreeLtxtqueryProcedureStart+i])
		if next.Address() != want.Address() || next.Signature != want.Signature || next.Result != want.Result || next.Extension != te.Address() || next.Implementation != extension.StockLtree {
			t.Fatal("mixed signature destination remap")
		}
		if old.Signature != saved.Signature || old.Address() != saved.Address() || sv.MustProcedure(old.Address()) != old {
			t.Fatal("source mutated")
		}
		operator, _ := sv.Operator(se.Installation.Members[extension.LtreeLtxtqueryOperatorStart+i])
		moved := extension.RemapDescriptor(operator, mapping).(*catalog.Operator)
		expected, _ := tv.Operator(te.Installation.Members[extension.LtreeLtxtqueryOperatorStart+i])
		if moved.Left != expected.Left || moved.Right != expected.Right || moved.Function != expected.Function || moved.Commutator != expected.Commutator {
			t.Fatal("mixed operator relationship remap")
		}
	}
	candidate := catalog.CloneState(sv.State())
	if err := typedef.ImportScalarTypesLocked(candidate, mapping, func() (int64, error) { t.Fatal("scalar import allocated enum member"); return 0, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := typedef.ReownDomainTypes(candidate, mapping, true, routine.RangeTypeRebinder(candidate, mapping)); err != nil {
		t.Fatal(err)
	}
	typedef.RefreshScalarImportDestinations(candidate)
	imports := candidate.ScalarImports()
	for _, tc := range []struct {
		slot int
		text string
	}{{3, "Europe.Russia"}, {6, "Europe & Russia"}} {
		old := sv.MustType(se.Installation.Members[tc.slot])
		next := candidate.MustType(old.Address()).SQLType
		if next == old.SQLType || next.ID() != tv.MustType(te.Installation.Members[tc.slot]).SQLType.ID() || imports[old.SQLType] != next {
			t.Fatal("source-authorized destination generation missing")
		}
		value, err := old.SQLType.Input(t.Context(), tc.text, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, sourceValue := range []sqltype.Value{value, sqltype.Null(old.SQLType)} {
			moved, err := typedef.ReownDomainDatum(sourceValue, next, nil, imports, nil)
			if err != nil || moved.Type() != next || moved.IsNull() != sourceValue.IsNull() {
				t.Fatal("mixed operand import", err)
			}
			var back sqltype.Value
			if tc.slot == 3 {
				back, err = moved.RemapLtree(old.SQLType)
			} else {
				back, err = moved.RemapLtxtquery(old.SQLType)
			}
			if err != nil || back != sourceValue {
				t.Fatal("immutable imported backing changed", err)
			}
			if _, err := sourceValue.Retag(next); err == nil {
				t.Fatal("ordinary retag gained import authority")
			}
		}
		if sv.MustType(old.Address()) != old {
			t.Fatal("source descriptor changed")
		}
	}
	ltxtqueryRenamedOwner(t)
}
