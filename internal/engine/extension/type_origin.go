package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ValidateTypeOrigin checks this surviving released declaration and its actual
// companion/base identities. Neither current membership nor a live originating
// extension is required. Ordinary SQL types do not acquire this capability.
func ValidateTypeOrigin(v catalog.Read, t *catalog.Type) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid installed type declaration") }
	if t == nil || t.SQLType == nil || !t.Origin.Valid(t.Address()) || v.Owner() == nil || v.Get(t.Address()) != t || t.ObjectRevision == 0 || t.Owner.ID() == 0 || t.Owner != v.Owner().Principals.Principal(t.Owner.ID()) {
		return invalid()
	}
	i := t.Origin.Installation
	if v.Owner().Extensions[i.Provider] != i.Implementation || i.Implementation == nil || i.Implementation.Versions().InstallPath(i.Version) == nil {
		return invalid()
	}
	slot := t.Origin.Declaration
	canonical, ok := i.Declarations[slot].(*catalog.Type)
	if !ok || t.Array != canonical.Array {
		return invalid()
	}
	accepted := false
	switch i.Implementation {
	case StockHstore:
		accepted = slot == 0 && t.SQLType.IsHstore()
	case StockIntarray:
		accepted = slot == 0 && t.SQLType.IsQueryInt()
	case StockSeg:
		accepted = slot == 0 && t.SQLType.IsSeg()
	case StockCube:
		accepted = slot == 0 && t.SQLType.IsCube()
	case StockCitext:
		accepted = slot == 0 && t.SQLType.IsCitext()
	case StockTablefunc:
		if err := validateTablefuncTypeOrigin(v, t); err != nil {
			return err
		}
		accepted = true
	case StockLtree:
		for _, r := range ltreeRecipes {
			if slot == r.slot && r.accept(t.SQLType) {
				accepted = true
				break
			}
		}
	case StockIsn:
		for _, r := range isnRecipes {
			if slot == r.slot && r.accept(t.SQLType) {
				accepted = true
				break
			}
		}
	case StockLo, StockEarthdistance:
		if slot == 0 {
			accepted = t.SQLType.IsDomain()
			if accepted && i.Implementation == StockLo {
				base, ok := v.LookupType(sqltype.Oid)
				accepted = ok && t.SQLType.DomainBase() == base.Usage() && t.Dimensions == 0
			}
			if accepted && i.Implementation == StockEarthdistance {
				base := t.SQLType.DomainBase().Type
				current, ok := v.LookupType(base.ID())
				accepted = ok && current == base && base.IsCube() && base.ID() == canonical.SQLType.DomainBase().Type.ID()
				if accepted {
					if err := validateEarthDomainChecks(t, base.Usage()); err != nil {
						return err
					}
				}
			}
		} else if slot == 1 && t.SQLType.IsArray() && len(i.Members) > 0 {
			base, ok := v.Type(i.Members[0])
			accepted = ok && catalog.SameInstallation(base, i) && base.Array == t.SQLType.ID() && t.SQLType.Element() == base.SQLType
		}
	}
	if !accepted {
		return invalid()
	}
	if t.Array != 0 {
		a, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.Array)})
		if !ok || a.SQLType == nil || !a.SQLType.IsArray() || a.SQLType.Element() != t.SQLType || a.Namespace() != t.Namespace() || a.Owner != t.Owner || a.ObjectRevision == 0 {
			return invalid()
		}
	}
	return ValidateProviderHeader(v, t, ids.TypeClassID)
}
