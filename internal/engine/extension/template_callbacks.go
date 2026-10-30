package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func validateTemplate(v catalog.Read, d *catalog.TSTemplate) error {
	if d.Snowball() {
		return catalog.ValidateSnowballTemplate(v, d)
	}
	if d.Pinned() && !SimpleTemplate(d) && !IspellTemplate(d) &&
		!(d.Address().ObjectID == 3730 && d.Implementation == nil && d.Init == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3728}) && d.Lexize == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3729})) &&
		!(d.Address().ObjectID == 3742 && d.Implementation == nil && d.Init == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3740}) && d.Lexize == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3741})) {
		return InvalidProviderDescriptor()
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
		if v.Owner() == nil || v.Owner().Principals == nil || p.Owner.ID() == 0 || p.Owner != v.Owner().Principals.Principal(p.Owner.ID()) || v.Owner().Provider(ids.ProcedureClassID) == nil {
			return InvalidProviderDescriptor()
		}
		if err := catalog.Validate(v.Owner().Provider(ids.ProcedureClassID), v, p); err != nil {
			return err
		}
	}
	if d.Origin == nil {
		if d.Implementation != nil {
			return InvalidProviderDescriptor()
		}
		return nil
	}
	installation, ok := catalog.OriginInstallation(d)
	if !ok || v.Get(d.Address()) != d || v.Owner() == nil || v.Owner().Extensions[installation.Provider] != StockUnaccent || installation.Implementation != StockUnaccent || StockUnaccent.Versions().InstallPath(installation.Version) == nil || UnaccentTemplateProviderOf(d) != StockUnaccent || d.Origin.Declaration != 4 || len(installation.Members) != 6 {
		return InvalidProviderDescriptor()
	}
	canonical, ok := installation.Declarations[d.Origin.Declaration].(*catalog.TSTemplate)
	if !ok || d.Init != canonical.Init || d.Lexize != canonical.Lexize {
		return InvalidProviderDescriptor()
	}
	for i, a := range [2]ids.ObjectAddress{d.Init, d.Lexize} {
		p, ok := v.Procedure(a)
		if !ok || !catalog.SameInstallation(p, installation) || p.Origin.Declaration != i+2 {
			return InvalidProviderDescriptor()
		}
	}
	return nil
}

// dictionaryCapability selects only validated intrinsic callbacks or released
// callbacks from one genuine cohort. Current extension membership is unrelated.
// Zero means valid metadata whose native callback combination is unavailable.
func dictionaryCapability(v catalog.Read, d *catalog.TSTemplate) uint8 {
	init, iok := v.Procedure(d.Init)
	lexize, lok := v.Procedure(d.Lexize)
	if !iok || !lok {
		return 0
	}
	simple := d.Init == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725}) && d.Lexize == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3726})
	synonym := d.Init == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3728}) && d.Lexize == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3729})
	thesaurus := d.Init == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3740}) && d.Lexize == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3741})
	ispell := d.Init == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3731}) && d.Lexize == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3732})
	if simple || synonym || thesaurus || ispell {
		for _, p := range [2]*catalog.Procedure{init, lexize} {
			if p.Builtin == nil || p.Builtin.Identity.ID != p.Address().ObjectID || p.Builtin.Identity.Signature != p.Signature || p.Builtin.Result != 2281 || p.Result != 2281 || p.Implementation != nil || p.SQL != nil || p.Foreign != nil {
				return 0
			}
			if err := ValidateProviderHeader(v, p, ids.ProcedureClassID); err != nil {
				return 0
			}
		}
		if synonym {
			return 4
		}
		if thesaurus {
			return 5
		}
		if ispell {
			return 6
		}
		return 1
	}
	if init.SnowballHelper == catalog.SnowballInit && lexize.SnowballHelper == catalog.SnowballLexize {
		if catalog.ValidateSnowballHelper(v, init) != nil || catalog.ValidateSnowballHelper(v, lexize) != nil {
			return 0
		}
		return 3
	}
	if UnaccentProviderOf(init) != StockUnaccent || UnaccentProviderOf(lexize) != StockUnaccent {
		return 0
	}
	installation, ok := catalog.OriginInstallation(init)
	if !ok || !catalog.SameInstallation(lexize, installation) || init.Origin.Declaration != 2 || lexize.Origin.Declaration != 3 {
		return 0
	}
	return 2
}
