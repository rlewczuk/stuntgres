package catalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

// ValidateInstalledClassOrigin admits the immutable binding fields of generic
// installed operator/class/family/cast descriptors. Public identity, owner, ACL
// and current membership remain mutable and confer no provider authority.
func ValidateInstalledClassOrigin(v Read, d Descriptor) error {
	o := DescriptorOrigin(d)
	if o == nil {
		return nil
	}
	invalid := func() error { return fmt.Errorf("stuntgres: invalid installed class declaration") }
	if !o.Valid(d.Address()) || v.Owner() == nil || v.Get(d.Address()) != d {
		return invalid()
	}
	i := o.Installation
	implementation := v.Owner().Extensions[i.Provider]
	if implementation == nil || implementation != i.Implementation || implementation.Versions().InstallPath(i.Version) == nil {
		return invalid()
	}
	if err := ValidateExtensionMembership(v, d); err != nil {
		return err
	}
	// The seal authenticates released bindings, never current principals.
	// Each surviving descriptor and backing routine belongs to this engine's
	// principal domain even after a lawful owner or membership change.
	var ownerIDPrincipal bool
	switch live := d.(type) {
	case *Operator:
		ownerIDPrincipal = live.Owner.ID() != 0 && live.Owner == v.Owner().Principals.Principal(live.Owner.ID())
	case *OperatorClass:
		ownerIDPrincipal = live.Owner.ID() != 0 && live.Owner == v.Owner().Principals.Principal(live.Owner.ID())
	case *OperatorFamily:
		ownerIDPrincipal = live.Owner.ID() != 0 && live.Owner == v.Owner().Principals.Principal(live.Owner.ID())
	case *Cast:
		ownerIDPrincipal = true // Casts have no owner or ACL.
	}
	if !ownerIDPrincipal {
		return invalid()
	}
	canonical := i.Declarations[o.Declaration]
	switch live := d.(type) {
	case *Operator:
		original, ok := canonical.(*Operator)
		if !ok || live.Function != original.Function || live.Left != original.Left || live.Right != original.Right || live.Result != original.Result || live.Commutator != original.Commutator || live.Negator != original.Negator || live.Hash != original.Hash || live.Merge != original.Merge || live.Restriction != original.Restriction || live.Join != original.Join {
			return invalid()
		}
		procedure, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: live.Function})
		if !ok || procedure.Owner.ID() == 0 || procedure.Owner != v.Owner().Principals.Principal(procedure.Owner.ID()) || !SameInstallation(procedure, i) {
			return invalid()
		}
		if provider := v.Owner().Provider(ids.ProcedureClassID); provider != nil {
			if err := provider.Validate(v, procedure); err != nil {
				return err
			}
		}
	case *OperatorClass:
		original, ok := canonical.(*OperatorClass)
		if !ok || live.Method != original.Method || live.Family != original.Family || live.InputType != original.InputType || live.StorageType != original.StorageType || live.Default != original.Default {
			return invalid()
		}
	case *OperatorFamily:
		original, ok := canonical.(*OperatorFamily)
		if !ok || live.Method != original.Method {
			return invalid()
		}
	case *Cast:
		original, ok := canonical.(*Cast)
		if !ok || live.Source != original.Source || live.Target != original.Target || live.Function != original.Function || live.InputCast != original.InputCast || live.ResultCast != original.ResultCast || live.Context != original.Context || live.Method != original.Method || live.GeneratedRange != original.GeneratedRange {
			return invalid()
		}
		if live.Function != (ids.ObjectAddress{}) {
			procedure, ok := v.Procedure(live.Function)
			if !ok || procedure.Owner.ID() == 0 || procedure.Owner != v.Owner().Principals.Principal(procedure.Owner.ID()) || !SameInstallation(procedure, i) {
				return invalid()
			}
			if provider := v.Owner().Provider(ids.ProcedureClassID); provider != nil {
				if err := provider.Validate(v, procedure); err != nil {
					return err
				}
			}
		}
	default:
		return invalid()
	}
	return nil
}
