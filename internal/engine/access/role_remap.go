package access

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

// PrincipalRemap is the principal policy of a replacement into this store.
// catalogs is the destination catalog store this role store backs; its
// current view is read at each mapping, under the caller's install lock.
// operatorSeeded reports seeded builtin operators, whose bootstrap owner maps
// to the destination bootstrap principal.
func (s *RoleStore) PrincipalRemap(catalogs *catalog.Store, source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef, operatorSeeded func(*catalog.Operator) bool) catalog.PrincipalRemap {
	remap := s.PrincipalMapper(catalogs, source, targetSnapshot, expected)
	seedOwner := func(ref authorization.PrincipalRef, seeded bool) (authorization.PrincipalRef, error) {
		if !seeded || ref.ID() != authorization.BootstrapPrincipalID {
			return remap(ref)
		}
		if _, err := source.Descriptor(ref); err != nil {
			return authorization.PrincipalRef{}, err
		}
		target := s.Domain.Principal(authorization.BootstrapPrincipalID)
		if _, err := targetSnapshot.Descriptor(target); err != nil {
			return authorization.PrincipalRef{}, err
		}
		if _, err := s.PublishedRead(&catalogs.Owner, catalogs.Current()).Descriptor(target); err != nil {
			return authorization.PrincipalRef{}, err
		}
		return target, nil
	}
	return catalog.PrincipalRemap{Principal: remap, SeededOwner: seedOwner, OperatorSeeded: operatorSeeded}
}

// PrincipalMapper maps source principals to this store's principals by name.
// Both authorization ACLs and typed ACL data use the same captured name mapping
// and target-incarnation fences. Each replacement owns its private memoization.
func (s *RoleStore) PrincipalMapper(catalogs *catalog.Store, source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) func(authorization.PrincipalRef) (authorization.PrincipalRef, error) {
	mapping := make(map[authorization.PrincipalRef]authorization.PrincipalRef)
	return func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if target, ok := mapping[ref]; ok {
			return target, nil
		}
		d, err := source.Descriptor(ref)
		if err != nil {
			return authorization.PrincipalRef{}, err
		}
		target, ok := targetSnapshot.Lookup(d.Name)
		if !ok {
			return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q does not exist", d.Name)
		}
		if _, err := s.PublishedRead(&catalogs.Owner, catalogs.Current()).Descriptor(target.Ref); err != nil {
			return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q changed", d.Name)
		}
		if expected != nil && expected[ref] != target.Ref {
			return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q changed", d.Name)
		}
		mapping[ref] = target.Ref
		return target.Ref, nil
	}
}

// ValidateReplacementACLsLocked validates the default ACLs and every
// descriptor ACL of a replacement state against the committed roles. view
// reads next through owner.
func (s *RoleStore) ValidateReplacementACLsLocked(owner *catalog.Owner, view catalog.Read, next *catalog.State) error {
	roles := s.PublishedRead(owner, view)
	if err := catalog.ValidateDefaultACLs(roles, view); err != nil {
		return err
	}
	if err := catalog.ValidateInitialPrivileges(roles, view); err != nil {
		return err
	}
	for _, d := range next.Descriptors() {
		if err := ValidateACL(roles, d); err != nil {
			return err
		}
	}
	return nil
}
