package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

// Owner is the identity every Read of one engine carries: the provenance
// domain, the identities that stored principal and ACL values must belong
// to, and the provider registries. The engine's catalog store embeds it and
// is never copied or replaced, so its address identifies the engine's
// catalog. It holds no engine services: code that needs roles, paths or
// engine state takes them from its authorization view or caller.
type Owner struct {
	Domain       *ids.Domain
	Principals   *authorization.Domain
	ACLAuthority *sqltype.ACLAuthority
	Providers    map[ids.OID]Provider
	// Database identifies the selected runtime within its cluster. Zero retains
	// the bootstrap address for independently constructed catalog fixtures.
	Database ids.ObjectAddress
	// Extensions is the registry of extension implementations. Like
	// providers, it closes when construction ends.
	Extensions map[string]ExtensionImplementation
	// seed holds the descriptors the builtin seed installed, except the
	// database descriptor. FinishSeedLocked sets it once; see SeedIdentical.
	seed util.PersistentMap[ids.ObjectAddress, Descriptor]
}

// Provider returns the registered provider of a catalog class, or nil. The
// registry is immutable after construction.
func (s *Owner) Provider(class ids.OID) Provider {
	return s.Providers[class]
}

func (s *Owner) DatabaseAddress() ids.ObjectAddress {
	if s.Database == (ids.ObjectAddress{}) {
		return CurrentDatabaseAddress
	}
	return s.Database
}

// SeedIdentical reports whether d is the very descriptor the builtin seed of
// the view's owner installed at d's address. Published descriptors are
// immutable, so such a descriptor still has its seeded content, and the seed
// was validated as a whole (Store.ValidateSeed). Builtin seeds depend only on
// pinned builtin objects. Any change installs a new descriptor, which no
// longer matches.
func (r Read) SeedIdentical(d Descriptor) bool {
	if r.owner == nil || d == nil {
		return false
	}
	seeded, ok := r.owner.seed.Get(d.Address())
	return ok && seeded == d
}

// Validate checks d with p in v, unless d is seed-identical in v
// (Read.SeedIdentical) and so already validated with its seed.
func Validate(p Provider, v Read, d Descriptor) error {
	if v.SeedIdentical(d) {
		return nil
	}
	return p.Validate(v, d)
}
