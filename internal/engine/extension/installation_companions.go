package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// AdmitInstallationCompanions records PostgreSQL's generated type membership
// after the installer's released recipe is validated/sealed. Generated arrays
// have genuine element authority, not invented provider declaration slots.
func AdmitInstallationCompanions(stage Stage, ext *catalog.Extension) error {
	view := stage.CurrentRead()
	seen := make(map[ids.ObjectAddress]bool, len(ext.Members))
	for _, a := range ext.Members {
		seen[a] = true
	}
	var companions []catalog.Descriptor
	for _, a := range ext.Installation.Members {
		d := view.Get(a)
		switch d.(type) {
		case *catalog.Type, *catalog.Relation:
		default:
			continue
		}
		closure, err := catalog.ExtensionMemberClosure(view, d)
		if err != nil {
			return err
		}
		for _, member := range closure {
			if seen[member.Address()] {
				continue
			}
			if catalog.ExtensionMembership(member) != (ids.ObjectAddress{}) {
				return fmt.Errorf("stuntgres: unexpected installation companion membership")
			}
			seen[member.Address()] = true
			companions = append(companions, member)
		}
	}
	if len(companions) == 0 {
		return nil
	}
	members := make([]ids.ObjectAddress, len(ext.Members), len(ext.Members)+len(companions))
	copy(members, ext.Members)
	for _, d := range companions {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		n, err := catalog.WithExtensionMembership(d, ext.Address(), revision)
		if err != nil {
			return err
		}
		if err := stage.Replace(n); err != nil {
			return err
		}
		members = append(members, d.Address())
	}
	ext.Members = members
	return nil
}
