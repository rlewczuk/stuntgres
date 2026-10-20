package analyze

import "stuntgres/internal/engine/ids"

// LookupKind names the provider family a search-path lookup serves. The host
// derives the effective path from it: function, operator, collation and
// conversion lookups skip TEMP namespaces, relation and type lookups add the
// session's own TEMP namespace first.
type LookupKind uint8

const (
	RelationLookup LookupKind = iota
	TypeLookup
	FunctionLookup
	InstalledFunctionLookup // Registered procedures only; ordinary TEMP isolation stays unchanged.
	OperatorLookup
	CollationLookup
	ConversionLookup
)

// NamespaceSelection is the namespace list a name lookup searches. Qualified
// lookup carries its one address inline, without a singleton slice.
type NamespaceSelection struct {
	Path      []ids.ObjectAddress
	Qualified ids.ObjectAddress
}

// Count is the number of namespaces to search.
func (p NamespaceSelection) Count() int {
	if p.Qualified != (ids.ObjectAddress{}) {
		return 1
	}
	return len(p.Path)
}

// At is the i-th namespace to search.
func (p NamespaceSelection) At(i int) ids.ObjectAddress {
	if p.Qualified != (ids.ObjectAddress{}) {
		return p.Qualified
	}
	return p.Path[i]
}
