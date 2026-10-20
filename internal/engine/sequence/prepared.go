package sequence

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Analysis is the private analysis cell of a prepared statement's sequence
// literals. Only sequence literal analysis is mutable; descriptions, ASTs and cursors are not.
// Engine.mu and physical-session admission serialize this cell and path cache.
type Analysis struct {
	Constants []Constant
	Path      Environment
	Private   JournalIdentity
	Session   ids.SessionToken
	// Namespace callbacks invalidate analysis even when the effective path is equal.
	Namespace uint64
}

// Environment is the effective search path a prepared sequence literal was
// resolved under.
type Environment struct {
	Namespaces                                 []ids.ObjectAddress
	ImplicitCatalog, ImplicitTemp, PendingTemp bool
}

// PathKey is every input of a physical session's effective path; the cached
// path is reused only for an equal key.
type PathKey struct {
	Text                 string
	Role                 authorization.PrincipalRef
	Authorization, Roles uint64
	Namespace            uint64
	Epoch                ids.CatalogEpoch
	Temp, Prefix         ids.ObjectAddress
	Private              JournalIdentity
	Database             catalog.Descriptor
}

// JournalIdentity is a session's private catalog journal, kept only for
// identity comparison so these cache records name no journal type.
type JournalIdentity any

type pathCache struct {
	valid       bool
	key         PathKey
	environment Environment
}

// SameEnvironment reports whether two effective paths resolve alike.
func SameEnvironment(a, b Environment) bool {
	return a.ImplicitCatalog == b.ImplicitCatalog && a.ImplicitTemp == b.ImplicitTemp && a.PendingTemp == b.PendingTemp && slices.Equal(a.Namespaces, b.Namespaces)
}

// Constant is one captured regclass literal of a prepared statement: its
// source offset, provider identity, temporary owner and definition stamp.
type Constant struct {
	Offset      int
	Ref         ids.ObjectRef
	Temporary   ids.SessionToken
	Stamp       ids.DefinitionRevision
	Represented bool
}
