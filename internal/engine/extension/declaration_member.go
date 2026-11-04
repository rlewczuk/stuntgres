package extension

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
)

// declarationProducerMember is the same sealed producer-ordinal authority used
// by declaration presentation and actual native SQL-body initialization.
func declarationProducerMember(view catalog.Read, installation *catalog.Installation, slot int, class ids.OID) (catalog.Descriptor, error) {
	if !installation.Valid() || slot < 0 || slot >= len(installation.Members) || installation.Members[slot].ClassID != class {
		return nil, ids.ErrCatalogReplaced
	}
	current := view.Get(installation.Members[slot])
	if current == nil {
		return nil, ids.ErrCatalogReplaced
	}
	origin := catalog.DescriptorOrigin(current)
	if !origin.Valid(current.Address()) || origin.Installation != installation || origin.Declaration != slot {
		return nil, ids.ErrCatalogReplaced
	}
	if err := catalog.ValidateExtensionMembership(view, current); err != nil {
		return nil, ids.ErrCatalogReplaced
	}
	return current, nil
}

// DeclarationMember selects only an original typed recipe member. Mutable
// schema/name/membership is never a callable lookup or replacement authority.
func DeclarationMember(view catalog.Read, target *catalog.Procedure, p proc.DeclarationPart) (catalog.Descriptor, error) {
	owner, ok := catalog.OriginInstallation(target)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	pageinspect := owner.Provider == "pageinspect" && owner.Version == "1.13"
	freespace := owner.Provider == "pg_freespacemap" && owner.Version == "1.3"
	if pageinspect {
		slot := target.Origin.Declaration
		allowed := slot == 19 && (p.Slot == 15 || p.Slot == 18) || slot == 20 && p.Slot == 19
		if !allowed || p.Extension != "" || p.Class != uint32(ids.ProcedureClassID) || view.Owner() == nil {
			return nil, ids.ErrCatalogReplaced
		}
		if err := ValidatePageinspectIdentity(view, target, view.Owner().Domain.Ref(view.Epoch(), target.Address())); err != nil {
			return nil, ids.ErrCatalogReplaced
		}
	} else if freespace {
		if target.Origin.Declaration != 1 || p.Slot != 0 || p.Extension != "" || p.Class != uint32(ids.ProcedureClassID) {
			return nil, ids.ErrCatalogReplaced
		}
		if err := StockPgFreespace.ValidateProcedure(view, target); err != nil {
			return nil, err
		}
	} else if owner.Provider != "earthdistance" || owner.Version != "1.2" {
		return nil, ids.ErrCatalogReplaced
	}
	original, ok := owner.Declarations[target.Origin.Declaration].(*catalog.Procedure)
	if !ok || original.DeclarationBody != target.DeclarationBody {
		return nil, ids.ErrCatalogReplaced
	}
	if err := catalog.ValidateExtensionMembership(view, target); err != nil {
		return nil, ids.ErrCatalogReplaced
	}
	installation := owner
	if p.Extension != "" {
		if p.Extension != "cube" {
			return nil, ids.ErrCatalogReplaced
		}
		domain, err := declarationProducerMember(view, owner, 0, ids.TypeClassID)
		if err != nil {
			return nil, err
		}
		earth, ok := domain.(*catalog.Type)
		if !ok || earth.SQLType == nil || !earth.SQLType.IsDomain() {
			return nil, ids.ErrCatalogReplaced
		}
		base := earth.SQLType.DomainBase().Type
		if base == nil || !base.IsCube() {
			return nil, ids.ErrCatalogReplaced
		}
		cube, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.ID())})
		if !ok || cube.SQLType == nil || cube.SQLType.ID() != earth.SQLType.BaseType().ID() {
			return nil, ids.ErrCatalogReplaced
		}
		installation, ok = catalog.OriginInstallation(cube)
		if !ok || installation.Provider != "cube" || installation.Version != "1.5" || installation.Members[0] != cube.Address() {
			return nil, ids.ErrCatalogReplaced
		}
		if _, err := declarationProducerMember(view, installation, 0, ids.TypeClassID); err != nil {
			return nil, err
		}
	}
	d, err := declarationProducerMember(view, installation, p.Slot, ids.OID(p.Class))
	if err != nil {
		return nil, err
	}
	switch current := d.(type) {
	case *catalog.Procedure:
		if current.RoutineKind != catalog.FunctionRoutine {
			return nil, ids.ErrCatalogReplaced
		}
		if pageinspect {
			if err := ValidatePageinspectIdentity(view, current, view.Owner().Domain.Ref(view.Epoch(), current.Address())); err != nil {
				return nil, ids.ErrCatalogReplaced
			}
		}
		count := 0
		if current.Signature != "" {
			for arg := range strings.SplitSeq(string(current.Signature), ",") {
				if count >= p.ArgumentCount {
					return nil, ids.ErrCatalogReplaced
				}
				expected := p.Arguments[count]
				if expected == 0 {
					expected = uint32(installation.Members[0].ObjectID)
				}
				actual, err := strconv.ParseUint(arg, 10, 32)
				if err != nil || uint32(actual) != expected {
					return nil, ids.ErrCatalogReplaced
				}
				count++
			}
		}
		if count != p.ArgumentCount {
			return nil, ids.ErrCatalogReplaced
		}
	case *catalog.Type:
	default:
		return nil, ids.ErrCatalogReplaced
	}
	return d, nil
}
