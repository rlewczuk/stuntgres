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

// Observation is what a prepared literal group is analyzed and validated
// against: the engine's catalog domain, the statement's catalog read, the
// caller's physical session, its private journal, the effective path and
// the session memory that carries local source events. The engine builds
// it under Engine.mu; it holds no closure, so validation does not allocate.
type Observation struct {
	Domain  *ids.Domain
	Read    catalog.Read
	Session ids.SessionToken
	Journal JournalIdentity
	Path    Environment
	// Memory is the caller's session memory; nil reads no source events.
	Memory *SessionMemory
}

// DefinitionStamp is the invalidation stamp of a prepared dependency. A
// sequence's is its plan stamp, raised by the session's own source events
// (a reverted reset keeps its event only in the initiating session); any
// other descriptor's is its revision. Generator state is never a stamp.
func DefinitionStamp(d catalog.Descriptor, memory *SessionMemory) ids.DefinitionRevision {
	if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind == catalog.SequenceRelation {
		stamp := relation.SequencePlanStamp
		if memory != nil {
			if m := memory.Memory(relation.Address()); m != nil {
				stamp = max(stamp, m.SourceStamp())
			}
		}
		return stamp
	}
	return d.Revision()
}

// NewAnalysis is the private analysis cell of a literal group resolved under o.
func NewAnalysis(o Observation, constants []Constant) Analysis {
	return Analysis{Constants: constants, Path: o.Path, Private: o.Journal, Session: o.Session, Namespace: o.Read.State().NamespaceGeneration()}
}

// Valid reports whether the literal group keeps its resolution under o. A
// false result asks for reanalysis of the whole original group; an error
// fences a native replacement other than the caller's exact live TEMP
// sequence. A nil or empty cell is always valid.
func (a *Analysis) Valid(o Observation) (bool, error) {
	if a == nil || len(a.Constants) == 0 {
		return true, nil
	}
	changed := a.Session != o.Session || a.Private != o.Journal || a.Namespace != o.Read.State().NamespaceGeneration() || !SameEnvironment(o.Path, a.Path)
	for _, constant := range a.Constants {
		ref := constant.Ref
		if ref.Domain() != o.Domain {
			return false, ids.ErrCatalogReplaced
		}
		if constant.Temporary != (ids.SessionToken{}) && constant.Temporary != o.Session {
			return false, ids.ErrCatalogReplaced
		}
		if ref.Epoch != o.Read.Epoch() {
			// Native replacement permits only the original owner's exact live temp.
			d, ok := o.Read.Relation(ref.Address)
			if !ok || constant.Temporary != o.Session || d.RelationKind != catalog.SequenceRelation {
				return false, ids.ErrCatalogReplaced
			}
			ns, ok := o.Read.Namespace(d.Namespace())
			if !ok || ns.Temporary != constant.Temporary {
				return false, ids.ErrCatalogReplaced
			}
		}
		d, ok := o.Read.Relation(ref.Address)
		if !ok {
			if constant.Stamp != 0 {
				changed = true
			}
			continue // Missing numeric regclass input is valid until relation opening.
		}
		if DefinitionStamp(d, o.Memory) != constant.Stamp {
			changed = true
		}
	}
	return !changed, nil
}

// PinReplacement keeps the original epoch of every exact retained TEMP
// capture of previous in the reanalyzed constants, even when another
// dependency forced the group's source to be reanalyzed. Native replacement
// refresh is provenance, not a license to rescue original SQL by name: any
// other capture from an older epoch than the current one is fenced.
func PinReplacement(epoch ids.CatalogEpoch, session ids.SessionToken, previous *Analysis, constants []Constant) error {
	if previous == nil {
		return nil
	}
	for _, old := range previous.Constants {
		if old.Ref.Epoch == epoch {
			continue
		}
		found := false
		for i := range constants {
			current := &constants[i]
			if current.Offset != old.Offset {
				continue
			}
			if old.Temporary != session || current.Temporary != old.Temporary || current.Ref.Domain() != old.Ref.Domain() || current.Ref.Address != old.Ref.Address {
				return ids.ErrCatalogReplaced
			}
			current.Ref.Epoch = old.Ref.Epoch
			found = true
			break
		}
		if !found {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}

// TemporaryEpochValid reports whether ref, from an earlier catalog epoch of
// view's domain, still names token's live TEMP sequence, so a native
// replacement may carry it forward.
func TemporaryEpochValid(view catalog.Read, ref ids.ObjectRef, token ids.SessionToken) bool {
	if token == (ids.SessionToken{}) || ref.Domain() != view.Owner().Domain || ref.Epoch == 0 || ref.Epoch > view.Epoch() || ref.Address.SubID != 0 {
		return false
	}
	sequence, ok := view.Relation(ref.Address)
	if !ok || sequence.RelationKind != catalog.SequenceRelation {
		return false
	}
	ns, ok := view.Namespace(sequence.Namespace())
	return ok && ns.Temporary == token
}
