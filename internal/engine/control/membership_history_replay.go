package control

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// NativeMembershipHistoryReplay recognizes only a membership reconstruction
// declared by native installation metadata. It is not an authorization bypass:
// normal extension/object ownership, membership closure and final metadata
// validation still run. Plain SQL ADD/DROP has no such declaration.
func NativeMembershipHistoryReplay(view catalog.Read, records []extension.OwnerRecord, root catalog.Descriptor, target *catalog.Extension, action string) bool {
	if root == nil || target == nil || action != "add" && action != "drop" || view.Owner() == nil {
		return false
	}
	origin := catalog.DescriptorOrigin(root)
	for _, record := range records {
		implementation, ok := view.Owner().Extensions[record.Name]
		if !ok || implementation.Versions().InstallPath(record.Version) == nil {
			continue
		}
		for _, member := range record.Members {
			if member.Declaration != nil {
				// Synthetic DROP uses original installation coordinates; ADD
				// uses final renamed/moved coordinates. The immutable origin slot
				// identifies the same declaration in either phase.
				if !origin.Valid(root.Address()) || origin.Installation.Provider != record.Name || origin.Installation.Implementation != implementation || origin.Installation.Version != member.Version || origin.Declaration != *member.Declaration {
					continue
				}
				if action == "drop" {
					if target.Name() == record.Name && target.Installation.Valid() && target.Installation.Cohort == origin.Installation.Cohort && catalog.ExtensionMembership(root) == target.Address() {
						return true
					}
				} else if member.Extension == target.Name() && catalog.ExtensionMembership(root) == (ids.ObjectAddress{}) {
					identity, err := extension.OwnerIdentity(view, root)
					if err == nil && identity.Kind == member.Kind && identity.Schema == member.Schema && identity.Name == member.Name && identity.Signature == member.Signature {
						return true
					}
				}
				continue
			}
			// Ordinary represented members have no provider origin. Their
			// complete final logical identity and extension are declared by the
			// native owner record, not inferred from a name-only coincidence.
			if origin != nil || !record.Present || record.Name != target.Name() || member.Extension != target.Name() {
				continue
			}
			identity, err := extension.OwnerIdentity(view, root)
			if err != nil || identity.Kind != member.Kind || identity.Schema != member.Schema || identity.Name != member.Name || identity.Signature != member.Signature {
				continue
			}
			if action == "add" && catalog.ExtensionMembership(root) == (ids.ObjectAddress{}) || action == "drop" && catalog.ExtensionMembership(root) == target.Address() {
				return true
			}
		}
	}
	return false
}
