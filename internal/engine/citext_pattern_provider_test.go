package engine

import (
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCitextPatternProviderIdentity(t *testing.T) {
	e, ext, base := citextUnitInstalled(t)
	view := e.CatalogRead()
 if len(ext.Members) != extension.CitextMemberCount {
		t.Fatal(ext.Members)
	}
	names := [4]string{"texticlike", "texticnlike", "texticregexeq", "texticregexne"}
	for kind, name := range names {
		for pair := range 2 {
			slot := 18 + kind*2 + pair
			p := view.MustProcedure(ext.Members[slot])
			right := base.SQLType.ID()
			if pair == 1 {
				right = sqltype.Text
			}
			symbol, err := extension.ProcedureSymbol(view, p)
			if err != nil || symbol != name || p.Signature != extension.CitextPatternSignature(base.SQLType.ID(), right) || p.Result != ids.OID(sqltype.Bool) || !p.Strict || p.Volatility != expr.Immutable || p.Display.Language != catalog.InternalRoutineLanguage || p.Display.Source != name {
				t.Fatal(p, symbol, err)
			}
			for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Signature = "25,25" }, func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Display.Source = "other" }, func(p *catalog.Procedure) { p.ObjectRevision = 0 }, func(p *catalog.Procedure) { p.Implementation = extension.StockCube }} {
				state := catalog.CloneState(view.State())
				bad := *p
				mutate(&bad)
				state.SetDescriptor(bad.Address(), &bad)
				if err := extension.StockCitext.ValidateExtension(view.WithState(state), ext); err == nil {
					t.Fatal("corrupt procedure admitted", slot)
				}
			}
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{base.Address(): {ClassID: ids.TypeClassID, ObjectID: 990001}}
			mapped := extension.RemapDescriptor(p, mapping).(*catalog.Procedure)
			want := catalog.ProcedureSignature("990001,990001")
			if pair == 1 {
				want = "990001,25"
			}
			if mapped.Signature != want {
				t.Fatal(mapped.Signature, want)
			}
		}
	}
	opNames := [8]string{"~", "~*", "!~", "!~*", "~~", "~~*", "!~~", "!~~*"}
	kinds := [8]int{2, 2, 3, 3, 0, 0, 1, 1}
	neg := [8]int{2, 3, 0, 1, 6, 7, 4, 5}
	for pair := range 2 {
		for i, name := range opNames {
			op, ok := view.Operator(ext.Members[26+pair*8+i])
			if !ok || op.Name() != name || op.Function != ext.Members[18+kinds[i]*2+pair].ObjectID || op.Negator != ext.Members[26+pair*8+neg[i]].ObjectID || op.Commutator != 0 || op.Hash || op.Merge {
				t.Fatal(op)
			}
			state := catalog.CloneState(view.State())
			bad := *op
			bad.Function = ext.Members[18].ObjectID
			bad.Negator = 0
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockCitext.ValidateExtension(view.WithState(state), ext); err == nil {
				t.Fatal("corrupt operator admitted")
			}
		}
	}
	if err := extension.StockCitext.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	metadata, err := extension.OwnerMetadata(view, e.roles.PublishedRead(&e.catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	decoded, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
 if err != nil || !found || len(decoded.Members) != extension.CitextMemberCount {
		t.Fatal(decoded, found, err)
	}
	for i := 18; i < 26; i++ {
		want := "citext,citext"
		if i%2 == 1 {
			want = "citext,25"
		}
		if decoded.Members[i].Signature != want {
			t.Fatal(decoded.Members[i])
		}
	}
}
