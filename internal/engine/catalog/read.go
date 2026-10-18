package catalog

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// Typed read accessors over an immutable catalog snapshot.
//
// Each accessor is defined once on *State and delegated by Read. They replace
// direct indexing of the descriptor and name maps and keep that indexing's
// semantics exactly:
//
//   - Kind(a) is the comma-ok type assertion: (nil, false) when the address
//     is absent or holds another kind.
//   - MustKind(a) is the single-value type assertion: it panics with a
//     *runtime.TypeAssertionError when the address is absent or holds another
//     kind. It never returns a nil descriptor for a missing entry.
//   - Get, Find, NameAddress and LookupName are the single-value and comma-ok
//     map indexes.
//
// None of them guards a nil state: a zero Read panics, as the map index did.
// The older Descriptor, Lookup, LookupType and LookupArray methods keep their
// nil-state guard.

// descriptorAs is the comma-ok type assertion of one descriptor entry.
func descriptorAs[T Descriptor](s *State, a ids.ObjectAddress) (T, bool) {
	d, ok := s.descriptors[a].(T)
	return d, ok
}

// Get returns the descriptor at a, or nil when it is absent.
func (s *State) Get(a ids.ObjectAddress) Descriptor { return s.descriptors[a] }

// Find returns the descriptor at a and whether the entry exists.
func (s *State) Find(a ids.ObjectAddress) (Descriptor, bool) {
	d, ok := s.descriptors[a]
	return d, ok
}

// Descriptors iterates every descriptor in unspecified order.
func (s *State) Descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for a, d := range s.descriptors {
			if !yield(a, d) {
				return
			}
		}
	}
}

// DescriptorCount returns the number of descriptors.
func (s *State) DescriptorCount() int { return len(s.descriptors) }

// NameAddress returns the address bound to k, or the zero address.
func (s *State) NameAddress(k ids.NameKey) ids.ObjectAddress { return s.names[k] }

// LookupName returns the address bound to k and whether the binding exists.
func (s *State) LookupName(k ids.NameKey) (ids.ObjectAddress, bool) {
	a, ok := s.names[k]
	return a, ok
}

// Names iterates every name binding in unspecified order.
func (s *State) Names() iter.Seq2[ids.NameKey, ids.ObjectAddress] {
	return func(yield func(ids.NameKey, ids.ObjectAddress) bool) {
		for k, a := range s.names {
			if !yield(k, a) {
				return
			}
		}
	}
}

// NameCount returns the number of name bindings.
func (s *State) NameCount() int { return len(s.names) }

func (s *State) Relation(a ids.ObjectAddress) (*Relation, bool) {
	return descriptorAs[*Relation](s, a)
}

func (s *State) MustRelation(a ids.ObjectAddress) *Relation {
	return s.descriptors[a].(*Relation)
}

func (s *State) Namespace(a ids.ObjectAddress) (*Namespace, bool) {
	return descriptorAs[*Namespace](s, a)
}

func (s *State) MustNamespace(a ids.ObjectAddress) *Namespace {
	return s.descriptors[a].(*Namespace)
}

func (s *State) Type(a ids.ObjectAddress) (*Type, bool) {
	return descriptorAs[*Type](s, a)
}

func (s *State) MustType(a ids.ObjectAddress) *Type {
	return s.descriptors[a].(*Type)
}

func (s *State) Procedure(a ids.ObjectAddress) (*Procedure, bool) {
	return descriptorAs[*Procedure](s, a)
}

func (s *State) MustProcedure(a ids.ObjectAddress) *Procedure {
	return s.descriptors[a].(*Procedure)
}

func (s *State) Operator(a ids.ObjectAddress) (*Operator, bool) {
	return descriptorAs[*Operator](s, a)
}

func (s *State) MustOperator(a ids.ObjectAddress) *Operator {
	return s.descriptors[a].(*Operator)
}

func (s *State) Collation(a ids.ObjectAddress) (*Collation, bool) {
	return descriptorAs[*Collation](s, a)
}

func (s *State) MustCollation(a ids.ObjectAddress) *Collation {
	return s.descriptors[a].(*Collation)
}

func (s *State) Conversion(a ids.ObjectAddress) (*Conversion, bool) {
	return descriptorAs[*Conversion](s, a)
}

func (s *State) MustConversion(a ids.ObjectAddress) *Conversion {
	return s.descriptors[a].(*Conversion)
}

func (s *State) Database(a ids.ObjectAddress) (*Database, bool) {
	return descriptorAs[*Database](s, a)
}

func (s *State) MustDatabase(a ids.ObjectAddress) *Database {
	return s.descriptors[a].(*Database)
}

func (s *State) Extension(a ids.ObjectAddress) (*Extension, bool) {
	return descriptorAs[*Extension](s, a)
}

func (s *State) MustExtension(a ids.ObjectAddress) *Extension {
	return s.descriptors[a].(*Extension)
}

func (s *State) Foreign(a ids.ObjectAddress) (*ForeignObject, bool) {
	return descriptorAs[*ForeignObject](s, a)
}

func (s *State) MustForeign(a ids.ObjectAddress) *ForeignObject {
	return s.descriptors[a].(*ForeignObject)
}

func (s *State) Parameter(a ids.ObjectAddress) (*Parameter, bool) {
	return descriptorAs[*Parameter](s, a)
}

func (s *State) MustParameter(a ids.ObjectAddress) *Parameter {
	return s.descriptors[a].(*Parameter)
}

func (s *State) Tablespace(a ids.ObjectAddress) (*Tablespace, bool) {
	return descriptorAs[*Tablespace](s, a)
}

func (s *State) MustTablespace(a ids.ObjectAddress) *Tablespace {
	return s.descriptors[a].(*Tablespace)
}

func (s *State) TSDictionary(a ids.ObjectAddress) (*TSDictionary, bool) {
	return descriptorAs[*TSDictionary](s, a)
}

func (s *State) MustTSDictionary(a ids.ObjectAddress) *TSDictionary {
	return s.descriptors[a].(*TSDictionary)
}

func (s *State) TSTemplate(a ids.ObjectAddress) (*TSTemplate, bool) {
	return descriptorAs[*TSTemplate](s, a)
}

// Read delegates to its state; see the accessor contract above.

func (r Read) Get(a ids.ObjectAddress) Descriptor { return r.state.Get(a) }

func (r Read) Find(a ids.ObjectAddress) (Descriptor, bool) { return r.state.Find(a) }

func (r Read) Descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return r.state.Descriptors()
}

func (r Read) DescriptorCount() int { return r.state.DescriptorCount() }

func (r Read) NameAddress(k ids.NameKey) ids.ObjectAddress { return r.state.NameAddress(k) }

func (r Read) LookupName(k ids.NameKey) (ids.ObjectAddress, bool) {
	return r.state.LookupName(k)
}

func (r Read) Names() iter.Seq2[ids.NameKey, ids.ObjectAddress] { return r.state.Names() }

func (r Read) NameCount() int { return r.state.NameCount() }

func (r Read) Relation(a ids.ObjectAddress) (*Relation, bool) {
	return r.state.Relation(a)
}

func (r Read) MustRelation(a ids.ObjectAddress) *Relation {
	return r.state.MustRelation(a)
}

func (r Read) Namespace(a ids.ObjectAddress) (*Namespace, bool) {
	return r.state.Namespace(a)
}

func (r Read) MustNamespace(a ids.ObjectAddress) *Namespace {
	return r.state.MustNamespace(a)
}

func (r Read) Type(a ids.ObjectAddress) (*Type, bool) { return r.state.Type(a) }

func (r Read) MustType(a ids.ObjectAddress) *Type { return r.state.MustType(a) }

func (r Read) Procedure(a ids.ObjectAddress) (*Procedure, bool) {
	return r.state.Procedure(a)
}

func (r Read) MustProcedure(a ids.ObjectAddress) *Procedure {
	return r.state.MustProcedure(a)
}

func (r Read) Operator(a ids.ObjectAddress) (*Operator, bool) {
	return r.state.Operator(a)
}

func (r Read) MustOperator(a ids.ObjectAddress) *Operator {
	return r.state.MustOperator(a)
}

func (r Read) Collation(a ids.ObjectAddress) (*Collation, bool) {
	return r.state.Collation(a)
}

func (r Read) MustCollation(a ids.ObjectAddress) *Collation {
	return r.state.MustCollation(a)
}

func (r Read) Conversion(a ids.ObjectAddress) (*Conversion, bool) {
	return r.state.Conversion(a)
}

func (r Read) MustConversion(a ids.ObjectAddress) *Conversion {
	return r.state.MustConversion(a)
}

func (r Read) Database(a ids.ObjectAddress) (*Database, bool) {
	return r.state.Database(a)
}

func (r Read) MustDatabase(a ids.ObjectAddress) *Database {
	return r.state.MustDatabase(a)
}

func (r Read) Extension(a ids.ObjectAddress) (*Extension, bool) {
	return r.state.Extension(a)
}

func (r Read) MustExtension(a ids.ObjectAddress) *Extension {
	return r.state.MustExtension(a)
}

func (r Read) Foreign(a ids.ObjectAddress) (*ForeignObject, bool) {
	return r.state.Foreign(a)
}

func (r Read) MustForeign(a ids.ObjectAddress) *ForeignObject {
	return r.state.MustForeign(a)
}

func (r Read) Parameter(a ids.ObjectAddress) (*Parameter, bool) {
	return r.state.Parameter(a)
}

func (r Read) MustParameter(a ids.ObjectAddress) *Parameter {
	return r.state.MustParameter(a)
}

func (r Read) Tablespace(a ids.ObjectAddress) (*Tablespace, bool) {
	return r.state.Tablespace(a)
}

func (r Read) MustTablespace(a ids.ObjectAddress) *Tablespace {
	return r.state.MustTablespace(a)
}

func (r Read) TSDictionary(a ids.ObjectAddress) (*TSDictionary, bool) {
	return r.state.TSDictionary(a)
}

func (r Read) MustTSDictionary(a ids.ObjectAddress) *TSDictionary {
	return r.state.MustTSDictionary(a)
}

func (r Read) TSTemplate(a ids.ObjectAddress) (*TSTemplate, bool) {
	return r.state.TSTemplate(a)
}
