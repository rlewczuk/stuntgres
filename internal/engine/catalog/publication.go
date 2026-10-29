package catalog

import (
	"fmt"
	"iter"
	"slices"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// Publication is a database-local logical publication definition. Published
// descriptors and their retained expression payloads are immutable.
type Publication struct {
	ObjectAddress                                        ids.ObjectAddress
	ObjectRevision                                       ids.DefinitionRevision
	ObjectName                                           string
	Owner                                                authorization.PrincipalRef
	AllTables, Insert, Update, Delete, Truncate, ViaRoot bool
	GeneratedColumns                                     byte
}

func (d *Publication) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *Publication) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*Publication) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *Publication) Name() string                     { return d.ObjectName }
func (*Publication) Kind() ids.ObjectKind               { return ids.PublicationObject }

// PublicationRelation is one explicit edge, not an expanded partition or schema
// membership. ColumnsSpecified distinguishes no list from all current columns.
type PublicationRelation struct {
	ObjectAddress         ids.ObjectAddress
	ObjectRevision        ids.DefinitionRevision
	Publication, Relation ids.ObjectAddress
	ColumnsSpecified      bool
	Columns               []int32
	Filter                PolicyPayload
	filterDependencies    []ids.ObjectAddress
}

func (d *PublicationRelation) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *PublicationRelation) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*PublicationRelation) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (*PublicationRelation) Name() string                       { return "" }
func (*PublicationRelation) Kind() ids.ObjectKind               { return ids.PublicationRelationObject }
func (d *PublicationRelation) SetColumns(columns []int32)       { d.Columns = slices.Clone(columns) }
func (d *PublicationRelation) SetFilterDependencies(refs []ids.ObjectAddress) {
	d.filterDependencies = slices.Clone(refs)
}
func (d *PublicationRelation) FilterDependencies() []ids.ObjectAddress {
	return slices.Clone(d.filterDependencies)
}
func (d *PublicationRelation) VisitDependencies(visit func(ids.ObjectAddress, byte)) {
	visit(d.Publication, 'a')
	visit(d.Relation, 'a')
	for _, sub := range d.Columns {
		a := d.Relation
		a.SubID = sub
		visit(a, 'n')
	}
	for _, a := range d.filterDependencies {
		visit(a, 'n')
	}
}

type PublicationNamespace struct {
	ObjectAddress       ids.ObjectAddress
	ObjectRevision      ids.DefinitionRevision
	Publication, Schema ids.ObjectAddress
}

func (d *PublicationNamespace) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *PublicationNamespace) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*PublicationNamespace) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (*PublicationNamespace) Name() string                       { return "" }
func (*PublicationNamespace) Kind() ids.ObjectKind               { return ids.PublicationNamespaceObject }
func (d *PublicationNamespace) VisitDependencies(visit func(ids.ObjectAddress, byte)) {
	visit(d.Publication, 'a')
	visit(d.Schema, 'a')
}

type PublicationProvider struct{}
type PublicationRelationProvider struct{}
type PublicationNamespaceProvider struct{}

func (PublicationProvider) ClassID() ids.OID          { return ids.PublicationClassID }
func (PublicationRelationProvider) ClassID() ids.OID  { return ids.PublicationRelationClassID }
func (PublicationNamespaceProvider) ClassID() ids.OID { return ids.PublicationNamespaceClassID }
func publicationAuthority(v Read, d Descriptor, class ids.OID) bool {
	return v.Owner() != nil && v.State() != nil && d != nil && d.Address().ClassID == class && d.Address().ObjectID != 0 && d.Address().SubID == 0 && d.Revision() != 0 && v.Get(d.Address()) == d
}
func publicationInvalid() error {
	return fmt.Errorf("stuntgres: invalid publication descriptor authority")
}
func (PublicationProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*Publication)
	if !ok || d == nil || !publicationAuthority(v, d, ids.PublicationClassID) || d.Name() == "" || len(d.Name()) > 63 || !utf8.ValidString(d.Name()) || strings.IndexByte(d.Name(), 0) >= 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return publicationInvalid()
	}
	if v.Owner().Principals == nil || d.Owner.ID() == 0 || v.Owner().Principals.Principal(d.Owner.ID()) != d.Owner || d.GeneratedColumns != 'n' && d.GeneratedColumns != 's' {
		return publicationInvalid()
	}
	return nil
}
func (PublicationRelationProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*PublicationRelation)
	if !ok || d == nil || !publicationAuthority(v, d, ids.PublicationRelationClassID) {
		return publicationInvalid()
	}
	p, ok := v.Get(d.Publication).(*Publication)
	r, exists := v.Relation(d.Relation)
	if !ok || p == nil || !exists || d.Relation.SubID != 0 || p.AllTables || v.NameAddress(DescriptorNameKey(d)) != d.Address() || !d.ColumnsSpecified && len(d.Columns) != 0 || d.ColumnsSpecified && len(d.Columns) == 0 {
		return publicationInvalid()
	}
	ns, namespaceExists := v.Namespace(r.Namespace())
	if !namespaceExists || ns.Temporary != (ids.SessionToken{}) || ns.TemporaryToast || r.Persistence == "unlogged" || (r.RelationKind != TableRelation && r.RelationKind != PartitionedRelation) || r.Address().ObjectID < 16384 {
		return publicationInvalid()
	}
	if r.Partition != nil && !p.ViaRoot && (d.Filter != nil || d.ColumnsSpecified) {
		return publicationInvalid()
	}
	last := -1
	for _, sub := range d.Columns {
		i := slices.Index(r.Attributes, sub)
		if sub <= 0 || i <= last {
			return publicationInvalid()
		}
		last = i
		if generation, ok := r.ColumnMeta.Generations[sub]; ok && generation.Kind == GenerationVirtual {
			return publicationInvalid()
		}
	}
	if d.Filter == nil && len(d.filterDependencies) != 0 {
		return publicationInvalid()
	}
	if d.Filter != nil {
		if err := d.Filter.Validate(v, r); err != nil {
			return err
		}
	}
	for _, a := range d.filterDependencies {
		whole := a
		whole.SubID = 0
		if v.Get(whole) == nil {
			return publicationInvalid()
		}
		if a.SubID != 0 {
			relation, ok := v.Relation(whole)
			if !ok || !relation.HasAttribute(a.SubID) {
				return publicationInvalid()
			}
		}
	}
	return nil
}
func (PublicationNamespaceProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*PublicationNamespace)
	if !ok || d == nil || !publicationAuthority(v, d, ids.PublicationNamespaceClassID) {
		return publicationInvalid()
	}
	p, ok := v.Get(d.Publication).(*Publication)
	ns, exists := v.Namespace(d.Schema)
	if !ok || p == nil || p.AllTables || !exists || d.Schema.SubID != 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() || ns.Temporary != (ids.SessionToken{}) || ns.TemporaryToast || ns.Address().ObjectID == ids.PGCatalogNamespaceOID || ns.Address().ObjectID == ids.PGToastNamespaceOID {
		return publicationInvalid()
	}
	for _, mapping := range v.PublicationRelations() {
		if mapping.Publication == d.Publication && mapping.ColumnsSpecified {
			return publicationInvalid()
		}
	}
	return nil
}
func (v Read) Publications() iter.Seq2[ids.ObjectAddress, *Publication] {
	if v.state == nil {
		return func(func(ids.ObjectAddress, *Publication) bool) {}
	}
	return v.state.publications.All()
}
func (v Read) PublicationRelations() iter.Seq2[ids.ObjectAddress, *PublicationRelation] {
	if v.state == nil {
		return func(func(ids.ObjectAddress, *PublicationRelation) bool) {}
	}
	return v.state.publicationRelations.All()
}
func (v Read) PublicationNamespaces() iter.Seq2[ids.ObjectAddress, *PublicationNamespace] {
	if v.state == nil {
		return func(func(ids.ObjectAddress, *PublicationNamespace) bool) {}
	}
	return v.state.publicationNamespaces.All()
}
func (s *State) updatePublications(old, next Descriptor) {
	if d, ok := old.(*Publication); ok {
		s.publications = s.publications.Delete(d.Address())
	}
	if d, ok := old.(*PublicationRelation); ok {
		s.publicationRelations = s.publicationRelations.Delete(d.Address())
	}
	if d, ok := old.(*PublicationNamespace); ok {
		s.publicationNamespaces = s.publicationNamespaces.Delete(d.Address())
	}
	if d, ok := next.(*Publication); ok {
		s.publications = s.publications.Set(d.Address(), d)
	}
	if d, ok := next.(*PublicationRelation); ok {
		s.publicationRelations = s.publicationRelations.Set(d.Address(), d)
	}
	if d, ok := next.(*PublicationNamespace); ok {
		s.publicationNamespaces = s.publicationNamespaces.Set(d.Address(), d)
	}
}
