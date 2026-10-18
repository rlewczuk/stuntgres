package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
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
	// Extensions is the registry of extension implementations. Like
	// providers, it closes when construction ends.
	Extensions map[string]ExtensionImplementation
}

// Provider returns the registered provider of a catalog class, or nil. The
// registry is immutable after construction.
func (s *Owner) Provider(class ids.OID) Provider {
	return s.Providers[class]
}
