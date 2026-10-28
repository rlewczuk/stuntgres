package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func validateTemplate(v catalog.Read, d *catalog.TSTemplate) error {
	if d.Snowball() {
		return catalog.ValidateSnowballTemplate(v, d)
	}
	for i, a := range [2]ids.ObjectAddress{d.Init, d.Lexize} {
		if i == 0 && a == (ids.ObjectAddress{}) {
			continue
		}
		p, ok := v.Procedure(a)
		if !ok || p.RoutineKind == catalog.ProcedureRoutine || p.Result != 2281 || p.Signature != [2]catalog.ProcedureSignature{catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature}[i] {
			return InvalidProviderDescriptor()
		}
		if err := ValidateProviderHeader(v, p, ids.ProcedureClassID); err != nil {
			return err
		}
		if v.Owner() == nil || v.Owner().Provider(ids.ProcedureClassID) == nil {
			return InvalidProviderDescriptor()
		}
		if err := catalog.Validate(v.Owner().Provider(ids.ProcedureClassID), v, p); err != nil {
			return err
		}
	}
	if d.Extension == (ids.ObjectAddress{}) {
		if d.Implementation != nil {
			return InvalidProviderDescriptor()
		}
		return nil
	}
	parent, ok := v.Extension(d.Extension)
	if !ok || UnaccentTemplateProviderOf(d) != StockUnaccent || len(parent.Members) != 6 || parent.Members[4] != d.Address() {
		return InvalidProviderDescriptor()
	}
	return StockUnaccent.ValidateExtension(v, parent)
}

// dictionaryCapability checks the actual captured procedures, not the template's
// public label, seed address or implementation claim. Zero means valid metadata
// whose native callback combination is unavailable.
func dictionaryCapability(v catalog.Read, d *catalog.TSTemplate) uint8 {
	init, iok := v.Procedure(d.Init)
	lexize, lok := v.Procedure(d.Lexize)
	if !iok || !lok {
		return 0
	}
	if d.Init == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725}) && d.Lexize == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3726}) {
		for _, p := range [2]*catalog.Procedure{init, lexize} {
			if p.Builtin == nil || p.Builtin.Identity.ID != p.Address().ObjectID || p.Builtin.Identity.Signature != p.Signature || p.Builtin.Result != 2281 || p.Result != 2281 || p.Extension != (ids.ObjectAddress{}) || p.Implementation != nil || p.SQL != nil || p.Foreign != nil {
				return 0
			}
			if err := ValidateProviderHeader(v, p, ids.ProcedureClassID); err != nil {
				return 0
			}
		}
		return 1
	}
	if init.SnowballHelper == catalog.SnowballInit && lexize.SnowballHelper == catalog.SnowballLexize {
		if catalog.ValidateSnowballHelper(v, init) != nil || catalog.ValidateSnowballHelper(v, lexize) != nil {
			return 0
		}
		return 3
	}
	if UnaccentProviderOf(init) != StockUnaccent || UnaccentProviderOf(lexize) != StockUnaccent || init.Extension != lexize.Extension {
		return 0
	}
	parent, ok := v.Extension(init.Extension)
	if !ok || len(parent.Members) != 6 || parent.Members[2] != init.Address() || parent.Members[3] != lexize.Address() {
		return 0
	}
	if err := StockUnaccent.ValidateExtension(v, parent); err != nil {
		return 0
	}
	return 2
}
