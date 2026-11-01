package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestIsnValidationProvider(t *testing.T) {
	e, ext := isnInstallation(t)
	view := e.CatalogRead()
	if extension.IsnValidityStart != 404 || extension.IsnRepairStart != 412 || extension.IsnWeakGetterSlot != 420 || extension.IsnWeakSetterSlot != 421 || extension.IsnMemberCount != 422 { t.Fatal("append-only recipe bounds") }
	if ext.Installation.Members[403].ClassID != ids.CastClassID || ext.Installation.Members[404].ClassID != ids.ProcedureClassID { t.Fatal("cast/procedure boundary") }
	for slot := 404; slot < 422; slot++ {
		p := view.MustProcedure(ext.Installation.Members[slot])
		op, ok := extension.IsnValidationOperation(view, p)
		wantOperation, wantVolatility, wantParallel := sqltype.ISNIsValid, expr.Immutable, byte('s')
		if slot >= 412 { wantOperation = sqltype.ISNMakeValid }
		if slot == 420 { wantOperation, wantVolatility = sqltype.ISNWeakGet, expr.Stable }
		if slot == 421 { wantOperation, wantVolatility, wantParallel = sqltype.ISNWeakSet, expr.Volatile, 'u' }
		if !ok || op != wantOperation || p.Volatility != wantVolatility || p.Installed.Parallel != wantParallel || !p.Strict { t.Fatal("final declaration metadata", slot) }
		if slot < 420 {
			role := (slot-404)%8
			typ := view.MustType(ext.Installation.Members[role*3]).SQLType
			result := ids.OID(sqltype.Bool)
			if slot >= 412 { result = ids.OID(typ.ID()) }
			if p.Signature != extension.IsnSignature(typ.ID()) || p.Result != result { t.Fatal("nominal signature", slot) }
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Strict = false },
			func(p *catalog.Procedure) { p.Volatility = (p.Volatility+1)%3 },
			func(p *catalog.Procedure) { p.Installed.Parallel = 'r' },
			func(p *catalog.Procedure) { p.Signature = "25" },
			func(p *catalog.Procedure) { p.Result = 25 },
			func(p *catalog.Procedure) { p.Display.Source = "foreign" },
			func(p *catalog.Procedure) { p.Origin = nil },
			func(p *catalog.Procedure) { o := *p.Origin; o.Declaration = 404+(slot-403)%18; p.Origin = &o },
		} {
			bad := *p
			mutate(&bad)
			state := catalog.CloneState(view.State())
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockIsn.ValidateProcedure(view.WithState(state), &bad); err == nil { t.Fatal("forged validation declaration", slot) }
		}
		if _, _, err := extension.IsnCodecType(view, p); err == nil { t.Fatal("validation acquired codec authority", slot) }
	}
	// Genuine module members survive header retirement without losing origin.
	state := catalog.CloneState(view.State())
	for _, address := range ext.Members {
		d := state.Get(address)
		next, err := catalog.WithExtensionMembership(d, ids.ObjectAddress{}, d.Revision())
		if err != nil { t.Fatal(err) }
		state.SetDescriptor(address, next)
	}
	state.UnbindName(catalog.DescriptorNameKey(ext))
	state.RemoveDescriptor(ext.Address())
	detached := view.WithState(state)
	for slot := 404; slot < 422; slot++ {
		if err := extension.StockIsn.ValidateProcedure(detached, detached.MustProcedure(ext.Installation.Members[slot])); err != nil { t.Fatal("detached origin rejected", slot, err) }
	}
	for role := range 8 {
		typ := detached.MustType(ext.Installation.Members[role*3]).SQLType
		if _, err := extension.IsnInputProcedure(detached, typ); err != nil { t.Fatal("detached input rejected", role, err) }
	}
}
