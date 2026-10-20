package engine

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Only sequence literal analysis is mutable; descriptions, ASTs and cursors are not.
// Engine.mu and physical-session admission serialize this cell and path cache.
type sequenceAnalysis struct {
	constants []sequenceConstant
	path      sequenceEnvironment
	private   journalIdentity
	session   ids.SessionToken
	// Namespace callbacks invalidate analysis even when the effective path is equal.
	namespace uint64
}

type sequenceEnvironment struct {
	namespaces                                 []ids.ObjectAddress
	implicitCatalog, implicitTemp, pendingTemp bool
}

type sequencePathKey struct {
	text                 string
	role                 authorization.PrincipalRef
	authorization, roles uint64
	namespace            uint64
	epoch                ids.CatalogEpoch
	temp, prefix         ids.ObjectAddress
	private              journalIdentity
	database             catalog.Descriptor
}

// journalIdentity is a session's private catalog journal, kept only for
// identity comparison so these cache records name no journal type.
type journalIdentity any

type sequencePathCache struct {
	valid       bool
	key         sequencePathKey
	environment sequenceEnvironment
}

func sameSequenceEnvironment(a, b sequenceEnvironment) bool {
	return a.implicitCatalog == b.implicitCatalog && a.implicitTemp == b.implicitTemp && a.pendingTemp == b.pendingTemp && slices.Equal(a.namespaces, b.namespaces)
}

type sequenceConstant struct {
	offset      int
	ref         ids.ObjectRef
	temporary   ids.SessionToken
	stamp       ids.DefinitionRevision
	represented bool
}
