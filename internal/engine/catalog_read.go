package engine

import (
	"iter"

	"stuntgres/internal/engine/ids"
)

// Typed read accessors over an immutable catalog snapshot.
//
// Each accessor is defined once on *catalogState and delegated by CatalogRead.
// They replace direct indexing of the descriptor and name maps and keep that
// indexing's semantics exactly:
//
//   - Kind(a) is the comma-ok type assertion: (nil, false) when the address
//     is absent or holds another kind.
//   - MustKind(a) is the single-value type assertion: it panics with a
//     *runtime.TypeAssertionError when the address is absent or holds another
//     kind. It never returns a nil descriptor for a missing entry.
//   - Get, Find, NameAddress and LookupName are the single-value and comma-ok
//     map indexes.
//
// None of them guards a nil state: a zero CatalogRead panics, as the map index
// did. The older Descriptor, Lookup, LookupType and LookupArray methods keep
// their nil-state guard.

// descriptorAs is the comma-ok type assertion of one descriptor entry.
func descriptorAs[T Descriptor](s *catalogState, a ids.ObjectAddress) (T, bool) {
	d, ok := s.descriptors[a].(T)
	return d, ok
}

// Get returns the descriptor at a, or nil when it is absent.
func (s *catalogState) Get(a ids.ObjectAddress) Descriptor { return s.descriptors[a] }

// Find returns the descriptor at a and whether the entry exists.
func (s *catalogState) Find(a ids.ObjectAddress) (Descriptor, bool) {
	d, ok := s.descriptors[a]
	return d, ok
}

// Descriptors iterates every descriptor in unspecified order.
func (s *catalogState) Descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for a, d := range s.descriptors {
			if !yield(a, d) {
				return
			}
		}
	}
}

// DescriptorCount returns the number of descriptors.
func (s *catalogState) DescriptorCount() int { return len(s.descriptors) }

// NameAddress returns the address bound to k, or the zero address.
func (s *catalogState) NameAddress(k ids.NameKey) ids.ObjectAddress { return s.names[k] }

// LookupName returns the address bound to k and whether the binding exists.
func (s *catalogState) LookupName(k ids.NameKey) (ids.ObjectAddress, bool) {
	a, ok := s.names[k]
	return a, ok
}

// Names iterates every name binding in unspecified order.
func (s *catalogState) Names() iter.Seq2[ids.NameKey, ids.ObjectAddress] {
	return func(yield func(ids.NameKey, ids.ObjectAddress) bool) {
		for k, a := range s.names {
			if !yield(k, a) {
				return
			}
		}
	}
}

// NameCount returns the number of name bindings.
func (s *catalogState) NameCount() int { return len(s.names) }

func (s *catalogState) Relation(a ids.ObjectAddress) (*relationDescriptor, bool) {
	return descriptorAs[*relationDescriptor](s, a)
}

func (s *catalogState) MustRelation(a ids.ObjectAddress) *relationDescriptor {
	return s.descriptors[a].(*relationDescriptor)
}

func (s *catalogState) Namespace(a ids.ObjectAddress) (*namespaceDescriptor, bool) {
	return descriptorAs[*namespaceDescriptor](s, a)
}

func (s *catalogState) MustNamespace(a ids.ObjectAddress) *namespaceDescriptor {
	return s.descriptors[a].(*namespaceDescriptor)
}

func (s *catalogState) Type(a ids.ObjectAddress) (*typeDescriptor, bool) {
	return descriptorAs[*typeDescriptor](s, a)
}

func (s *catalogState) MustType(a ids.ObjectAddress) *typeDescriptor {
	return s.descriptors[a].(*typeDescriptor)
}

func (s *catalogState) Procedure(a ids.ObjectAddress) (*procedureDescriptor, bool) {
	return descriptorAs[*procedureDescriptor](s, a)
}

func (s *catalogState) MustProcedure(a ids.ObjectAddress) *procedureDescriptor {
	return s.descriptors[a].(*procedureDescriptor)
}

func (s *catalogState) Operator(a ids.ObjectAddress) (*operatorDescriptor, bool) {
	return descriptorAs[*operatorDescriptor](s, a)
}

func (s *catalogState) MustOperator(a ids.ObjectAddress) *operatorDescriptor {
	return s.descriptors[a].(*operatorDescriptor)
}

func (s *catalogState) Collation(a ids.ObjectAddress) (*collationDescriptor, bool) {
	return descriptorAs[*collationDescriptor](s, a)
}

func (s *catalogState) MustCollation(a ids.ObjectAddress) *collationDescriptor {
	return s.descriptors[a].(*collationDescriptor)
}

func (s *catalogState) Conversion(a ids.ObjectAddress) (*conversionDescriptor, bool) {
	return descriptorAs[*conversionDescriptor](s, a)
}

func (s *catalogState) MustConversion(a ids.ObjectAddress) *conversionDescriptor {
	return s.descriptors[a].(*conversionDescriptor)
}

func (s *catalogState) Database(a ids.ObjectAddress) (*databaseDescriptor, bool) {
	return descriptorAs[*databaseDescriptor](s, a)
}

func (s *catalogState) MustDatabase(a ids.ObjectAddress) *databaseDescriptor {
	return s.descriptors[a].(*databaseDescriptor)
}

func (s *catalogState) Extension(a ids.ObjectAddress) (*extensionDescriptor, bool) {
	return descriptorAs[*extensionDescriptor](s, a)
}

func (s *catalogState) MustExtension(a ids.ObjectAddress) *extensionDescriptor {
	return s.descriptors[a].(*extensionDescriptor)
}

func (s *catalogState) Foreign(a ids.ObjectAddress) (*foreignDescriptor, bool) {
	return descriptorAs[*foreignDescriptor](s, a)
}

func (s *catalogState) MustForeign(a ids.ObjectAddress) *foreignDescriptor {
	return s.descriptors[a].(*foreignDescriptor)
}

func (s *catalogState) Parameter(a ids.ObjectAddress) (*parameterDescriptor, bool) {
	return descriptorAs[*parameterDescriptor](s, a)
}

func (s *catalogState) MustParameter(a ids.ObjectAddress) *parameterDescriptor {
	return s.descriptors[a].(*parameterDescriptor)
}

func (s *catalogState) Tablespace(a ids.ObjectAddress) (*tablespaceDescriptor, bool) {
	return descriptorAs[*tablespaceDescriptor](s, a)
}

func (s *catalogState) MustTablespace(a ids.ObjectAddress) *tablespaceDescriptor {
	return s.descriptors[a].(*tablespaceDescriptor)
}

func (s *catalogState) TSDictionary(a ids.ObjectAddress) (*tsDictionaryDescriptor, bool) {
	return descriptorAs[*tsDictionaryDescriptor](s, a)
}

func (s *catalogState) MustTSDictionary(a ids.ObjectAddress) *tsDictionaryDescriptor {
	return s.descriptors[a].(*tsDictionaryDescriptor)
}

func (s *catalogState) TSTemplate(a ids.ObjectAddress) (*tsTemplateDescriptor, bool) {
	return descriptorAs[*tsTemplateDescriptor](s, a)
}

// CatalogRead delegates to its state; see the accessor contract above.

func (r CatalogRead) Get(a ids.ObjectAddress) Descriptor          { return r.state.Get(a) }
func (r CatalogRead) Find(a ids.ObjectAddress) (Descriptor, bool) { return r.state.Find(a) }
func (r CatalogRead) Descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return r.state.Descriptors()
}
func (r CatalogRead) DescriptorCount() int                        { return r.state.DescriptorCount() }
func (r CatalogRead) NameAddress(k ids.NameKey) ids.ObjectAddress { return r.state.NameAddress(k) }
func (r CatalogRead) LookupName(k ids.NameKey) (ids.ObjectAddress, bool) {
	return r.state.LookupName(k)
}
func (r CatalogRead) Names() iter.Seq2[ids.NameKey, ids.ObjectAddress] { return r.state.Names() }
func (r CatalogRead) NameCount() int                                   { return r.state.NameCount() }

func (r CatalogRead) Relation(a ids.ObjectAddress) (*relationDescriptor, bool) {
	return r.state.Relation(a)
}

func (r CatalogRead) MustRelation(a ids.ObjectAddress) *relationDescriptor {
	return r.state.MustRelation(a)
}

func (r CatalogRead) Namespace(a ids.ObjectAddress) (*namespaceDescriptor, bool) {
	return r.state.Namespace(a)
}

func (r CatalogRead) MustNamespace(a ids.ObjectAddress) *namespaceDescriptor {
	return r.state.MustNamespace(a)
}

func (r CatalogRead) Type(a ids.ObjectAddress) (*typeDescriptor, bool) { return r.state.Type(a) }

func (r CatalogRead) MustType(a ids.ObjectAddress) *typeDescriptor { return r.state.MustType(a) }

func (r CatalogRead) Procedure(a ids.ObjectAddress) (*procedureDescriptor, bool) {
	return r.state.Procedure(a)
}

func (r CatalogRead) MustProcedure(a ids.ObjectAddress) *procedureDescriptor {
	return r.state.MustProcedure(a)
}

func (r CatalogRead) Operator(a ids.ObjectAddress) (*operatorDescriptor, bool) {
	return r.state.Operator(a)
}

func (r CatalogRead) MustOperator(a ids.ObjectAddress) *operatorDescriptor {
	return r.state.MustOperator(a)
}

func (r CatalogRead) Collation(a ids.ObjectAddress) (*collationDescriptor, bool) {
	return r.state.Collation(a)
}

func (r CatalogRead) MustCollation(a ids.ObjectAddress) *collationDescriptor {
	return r.state.MustCollation(a)
}

func (r CatalogRead) Conversion(a ids.ObjectAddress) (*conversionDescriptor, bool) {
	return r.state.Conversion(a)
}

func (r CatalogRead) MustConversion(a ids.ObjectAddress) *conversionDescriptor {
	return r.state.MustConversion(a)
}

func (r CatalogRead) Database(a ids.ObjectAddress) (*databaseDescriptor, bool) {
	return r.state.Database(a)
}

func (r CatalogRead) MustDatabase(a ids.ObjectAddress) *databaseDescriptor {
	return r.state.MustDatabase(a)
}

func (r CatalogRead) Extension(a ids.ObjectAddress) (*extensionDescriptor, bool) {
	return r.state.Extension(a)
}

func (r CatalogRead) MustExtension(a ids.ObjectAddress) *extensionDescriptor {
	return r.state.MustExtension(a)
}

func (r CatalogRead) Foreign(a ids.ObjectAddress) (*foreignDescriptor, bool) {
	return r.state.Foreign(a)
}

func (r CatalogRead) MustForeign(a ids.ObjectAddress) *foreignDescriptor {
	return r.state.MustForeign(a)
}

func (r CatalogRead) Parameter(a ids.ObjectAddress) (*parameterDescriptor, bool) {
	return r.state.Parameter(a)
}

func (r CatalogRead) MustParameter(a ids.ObjectAddress) *parameterDescriptor {
	return r.state.MustParameter(a)
}

func (r CatalogRead) Tablespace(a ids.ObjectAddress) (*tablespaceDescriptor, bool) {
	return r.state.Tablespace(a)
}

func (r CatalogRead) MustTablespace(a ids.ObjectAddress) *tablespaceDescriptor {
	return r.state.MustTablespace(a)
}

func (r CatalogRead) TSDictionary(a ids.ObjectAddress) (*tsDictionaryDescriptor, bool) {
	return r.state.TSDictionary(a)
}

func (r CatalogRead) MustTSDictionary(a ids.ObjectAddress) *tsDictionaryDescriptor {
	return r.state.MustTSDictionary(a)
}

func (r CatalogRead) TSTemplate(a ids.ObjectAddress) (*tsTemplateDescriptor, bool) {
	return r.state.TSTemplate(a)
}
