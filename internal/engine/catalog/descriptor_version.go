package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// DescriptorVersion retains one immutable descriptor inventory and its exact
// catalog authority. The comparable value owns its persistent backing, not the
// State wrapper, which may subsequently be written through another alias.
// A zero version cannot certify that reconciliation is unnecessary.
type DescriptorVersion struct {
	descriptors util.PersistentMap[ids.ObjectAddress, Descriptor]
	owner       *Owner
	domain      *ids.Domain
	epoch       ids.CatalogEpoch
	valid       bool
}

// CaptureDescriptorVersion freezes r's descriptor inventory. Bulk-load states
// cannot be captured: their ordinary maps remain mutable until EndBulkLoad.
// Missing owner, domain, state or epoch leaves the version uncertified.
func (r Read) CaptureDescriptorVersion() DescriptorVersion {
	if r.state == nil {
		return DescriptorVersion{}
	}
	r.state.mustNotBulkLoad()
	return DescriptorVersion{
		descriptors: r.state.descriptors,
		owner:       r.owner,
		domain:      r.Domain(),
		epoch:       r.state.epoch,
		valid:       r.owner != nil && r.Domain() != nil && r.state.epoch != 0,
	}
}

// Get returns the descriptor from the captured inventory, independent of later
// writes to the source State. It returns nil for an absent address.
func (v DescriptorVersion) Get(address ids.ObjectAddress) Descriptor {
	descriptor, _ := v.descriptors.Get(address)
	return descriptor
}

// SameAuthority reports whether current belongs to the captured owner, domain
// and epoch. It does not establish descriptor equivalence; callers using an
// edit delta must independently authenticate the projection and edit history.
func (v DescriptorVersion) SameAuthority(current Read) bool {
	return v.valid && current.state != nil && current.state.bulk == nil &&
		current.owner == v.owner && current.Domain() == v.domain &&
		current.state.epoch == v.epoch
}

// Matches reports whether current has the exact captured immutable inventory
// and owner/domain/epoch authority. A bulk-loading current never matches, and
// neither does an independently rebuilt equivalent inventory.
func (v DescriptorVersion) Matches(current Read) bool {
	return v.SameAuthority(current) && current.state.descriptors == v.descriptors
}

// DescriptorsToReconcile returns no entries only when current matches the exact
// captured immutable inventory and owner/domain/epoch authority, or when it is
// a stateless read with no descriptors to reconcile. Otherwise it returns the
// real whole current inventory, captured when this method is called; separately
// rebuilt equivalent inventories conservatively require a scan.
func (v DescriptorVersion) DescriptorsToReconcile(current Read) iter.Seq2[ids.ObjectAddress, Descriptor] {
	if v.Matches(current) || current.state == nil {
		return func(func(ids.ObjectAddress, Descriptor) bool) {}
	}
	return current.Descriptors()
}
