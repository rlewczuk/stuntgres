package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestCitextComparisonProviderManifest(t *testing.T) {
	e, ext, base := citextUnitInstalled(t)
	view := e.CatalogRead()
	if len(ext.Members) != extension.CitextMemberCount+1 || len(ext.Installation.Members) != extension.CitextMemberCount || catalog.ExtensionMembership(view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})) != ext.Address() {
		t.Fatal(ext.Members)
	}
	for i, name := range extension.CitextComparisonNames {
		p := view.State().MustProcedure(ext.Installation.Members[extension.CitextComparisonProcedureStart+i])
		symbol, err := extension.ProcedureSymbol(view, p)
		if err != nil || symbol != name || p.Signature != extension.CitextComparisonSignature(base.SQLType.ID()) || p.Result != ids.OID(sqltype.Bool) {
			t.Fatal(p, symbol, err)
		}
		op, ok := view.Operator(ext.Installation.Members[extension.CitextComparisonOperatorStart+i])
		if !ok || op.Function != p.Address().ObjectID || op.Hash || op.Merge {
			t.Fatal(op)
		}
	}
	if _, err := sqltype.BindComparison(base.SQLType, base.SQLType, base.SQLType.Usage().Collation, false); err == nil {
		t.Fatal("scalar manifest granted generic ordering")
	}
}

func TestCitextComparisonProviderIdentity(t *testing.T) {
	e, ext, _ := citextUnitInstalled(t)
	for _, mutate := range []func(*catalog.State){
		func(s *catalog.State) {
			p := *s.MustProcedure(ext.Members[3])
			p.Signature = "25,25"
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State) {
			p := *s.MustProcedure(ext.Members[3])
			p.Strict = false
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State) {
			p := *s.MustProcedure(ext.Members[3])
			p.Implementation = extension.StockCube
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State) {
			op, _ := s.Operator(ext.Members[9])
			p := *op
			p.Function = ext.Members[4].ObjectID
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State) {
			op, _ := s.Operator(ext.Members[9])
			p := *op
			p.Hash = true
			s.SetDescriptor(p.Address(), &p)
		},
	} {
		s := catalog.CloneState(e.Catalog.State)
		mutate(s)
		if err := extension.StockCitext.ValidateExtension(e.Catalog.Read(s), ext); err == nil {
			t.Fatal("corrupt comparison admitted")
		}
	}
	if err := extension.StockCitext.ValidateExtension(e.CatalogRead(), ext); err != nil {
		t.Fatal("source mutated", err)
	}
}

func TestCitextComparisonProviderOwnerImport(t *testing.T) {
	// Existing import units exercise real independent-engine codec ownership;
	// comparison signatures must map both formals without touching the source.
	e, ext, base := citextUnitInstalled(t)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{base.Address(): {ClassID: ids.TypeClassID, ObjectID: 990001}}
	for i := range extension.CitextComparisonNames {
		p := e.Catalog.State.MustProcedure(ext.Members[3+i])
		mapped := extension.RemapDescriptor(p, mapping).(*catalog.Procedure)
		if mapped.Signature != "990001,990001" || p.Signature != extension.CitextComparisonSignature(base.SQLType.ID()) {
			t.Fatal(mapped, p)
		}
	}
}
