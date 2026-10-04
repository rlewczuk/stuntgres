package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"

	"stuntgres/internal/parser"
)

type OID uint32
type CatalogEpoch uint64
type DefinitionRevision uint64
type ObjectAddress struct {
	ClassID  OID
	ObjectID OID
	SubID    int32
}
// catalogOwner has nonzero size so distinct live engines have distinct tokens.
type catalogOwner struct{ marker byte }

type ObjectRef struct {
	owner   *catalogOwner
	Epoch   CatalogEpoch
	Address ObjectAddress
}
type relationStorage struct{ rows [][]driver.Value }

const (
	NamespaceClassID      OID = 2615
	RelationClassID       OID = 1259
	PGCatalogNamespaceOID OID = 11
	PublicNamespaceOID    OID = 2200
)

type ObjectKind uint8

const (
	NamespaceObject ObjectKind = iota + 1
	RelationObject
)

type Descriptor interface {
	Address() ObjectAddress
	Revision() DefinitionRevision
	Namespace() ObjectAddress
	Name() string
	Kind() ObjectKind
}
type namespaceDescriptor struct {
	address  ObjectAddress
	name     string
	revision DefinitionRevision
}

func (d *namespaceDescriptor) Address() ObjectAddress       { return d.address }
func (d *namespaceDescriptor) Revision() DefinitionRevision { return d.revision }
func (*namespaceDescriptor) Namespace() ObjectAddress       { return ObjectAddress{} }
func (d *namespaceDescriptor) Name() string                 { return d.name }
func (*namespaceDescriptor) Kind() ObjectKind               { return NamespaceObject }

type relationDescriptor struct {
	address, namespace ObjectAddress
	name               string
	revision           DefinitionRevision
	columns            []parser.Column
	index              map[string]int
	attributes         []int32
}

func (d *relationDescriptor) Address() ObjectAddress       { return d.address }
func (d *relationDescriptor) Revision() DefinitionRevision { return d.revision }
func (d *relationDescriptor) Namespace() ObjectAddress     { return d.namespace }
func (d *relationDescriptor) Name() string                 { return d.name }
func (*relationDescriptor) Kind() ObjectKind               { return RelationObject }

type NameKey struct {
	ClassID   OID
	Namespace ObjectAddress
	Name      string
}
type catalogState struct {
	epoch       CatalogEpoch
	descriptors map[ObjectAddress]Descriptor
	names       map[NameKey]ObjectAddress
}
type CatalogRead struct {
	owner *Engine
	state *catalogState
}

func (r CatalogRead) Epoch() CatalogEpoch {
	if r.state == nil {
		return 0
	}
	return r.state.epoch
}
func (r CatalogRead) Descriptor(a ObjectAddress) (Descriptor, bool) {
	if r.state == nil {
		return nil, false
	}
	d, ok := r.state.descriptors[a]
	return d, ok
}
func (r CatalogRead) Lookup(k NameKey) []ObjectAddress {
	if r.state == nil {
		return nil
	}
	a, ok := r.state.names[k]
	if !ok {
		return nil
	}
	return []ObjectAddress{a}
}

type CatalogProvider interface {
	ClassID() OID
	Validate(CatalogRead, Descriptor) error
}
type namespaceProvider struct{}

func (namespaceProvider) ClassID() OID { return NamespaceClassID }
func (namespaceProvider) Validate(v CatalogRead, d Descriptor) error {
	a := d.Address()
	key := NameKey{ClassID: NamespaceClassID, Name: d.Name()}
	found := v.Lookup(key)
	if d.Kind() != NamespaceObject || a.ClassID != NamespaceClassID || a.ObjectID == 0 || a.SubID != 0 || d.Revision() == 0 || d.Namespace() != (ObjectAddress{}) || len(found) != 1 || found[0] != a {
		return fmt.Errorf("stuntgres: invalid namespace descriptor")
	}
	return nil
}

type relationProvider struct{}

func (relationProvider) ClassID() OID { return RelationClassID }
func (relationProvider) Validate(v CatalogRead, d Descriptor) error {
	r, ok := d.(*relationDescriptor)
	if !ok || d.Kind() != RelationObject || d.Address().ClassID != RelationClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 {
		return fmt.Errorf("stuntgres: invalid relation descriptor")
	}
	ns, ok := v.Descriptor(r.namespace)
	if !ok || ns.Kind() != NamespaceObject {
		return fmt.Errorf("stuntgres: relation namespace is missing")
	}
	found := v.Lookup(NameKey{ClassID: RelationClassID, Namespace: r.namespace, Name: r.name})
	if len(found) != 1 || found[0] != r.address || len(r.columns) != len(r.attributes) || len(r.index) != len(r.columns) {
		return fmt.Errorf("stuntgres: invalid relation identity")
	}
	for i, c := range r.columns {
		offset, exists := r.index[c.Name]
		if r.attributes[i] != int32(i+1) || !exists || offset != i {
			return fmt.Errorf("stuntgres: invalid relation column identity")
		}
	}
	return nil
}

var ErrCatalogReplaced = errors.New("stuntgres: catalog replaced before publication")

type PublicationToken struct{ Epoch CatalogEpoch }
type ChangeSet struct {
	Definitions []ObjectAddress
	Statistics  []ObjectAddress
	Namespaces  []ObjectAddress
	Replacement bool
}
type CatalogConflict struct{}

func (CatalogConflict) Error() string { return "stuntgres: catalog publication conflict" }

type catalogLifecycle uint8

const (
	catalogOpen catalogLifecycle = iota
	catalogValidated
	catalogPublished
	catalogDiscarded
)

type CatalogStage struct {
	owner       *Engine
	base        CatalogRead
	descriptors map[ObjectAddress]Descriptor
	names       map[NameKey]ObjectAddress
	touched     map[NameKey]bool
	lifecycle   catalogLifecycle
	invalid     error
}
type PreparedCatalogPublication struct {
	stage *CatalogStage
	epoch CatalogEpoch
	ctx   context.Context
}

func (e *Engine) BeginCatalogStage(base CatalogRead) *CatalogStage {
	s := &CatalogStage{owner: e, base: base, descriptors: make(map[ObjectAddress]Descriptor), names: make(map[NameKey]ObjectAddress), touched: make(map[NameKey]bool)}
	if base.owner != e || base.state == nil {
		s.invalid = fmt.Errorf("stuntgres: foreign catalog view")
	}
	return s
}
func (s *CatalogStage) Create(d Descriptor) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	if d == nil {
		return fmt.Errorf("stuntgres: nil catalog descriptor")
	}
	a := d.Address()
	if a.ObjectID == 0 || a.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid catalog object address")
	}
	if _, ok := s.base.state.descriptors[a]; ok {
		return CatalogConflict{}
	}
	if _, ok := s.descriptors[a]; ok {
		return CatalogConflict{}
	}
	k := NameKey{ClassID: a.ClassID, Namespace: d.Namespace(), Name: d.Name()}
	s.touched[k] = true
	if _, ok := s.base.state.names[k]; ok {
		return CatalogConflict{}
	}
	if _, ok := s.names[k]; ok {
		return CatalogConflict{}
	}
	s.descriptors[a] = d
	s.names[k] = a
	return nil
}
func (e *Engine) stageRead(s *CatalogStage) CatalogRead {
	ds := make(map[ObjectAddress]Descriptor, len(s.base.state.descriptors)+len(s.descriptors))
	ns := make(map[NameKey]ObjectAddress, len(s.base.state.names)+len(s.names))
	for k, v := range s.base.state.descriptors {
		ds[k] = v
	}
	for k, v := range s.base.state.names {
		ns[k] = v
	}
	for k, v := range s.descriptors {
		ds[k] = v
	}
	for k, v := range s.names {
		ns[k] = v
	}
	return CatalogRead{owner: e, state: &catalogState{epoch: s.base.Epoch(), descriptors: ds, names: ns}}
}
func (e *Engine) ValidateCatalogStage(ctx context.Context, s *CatalogStage) (*PreparedCatalogPublication, error) {
	if s == nil || s.owner != e || s.lifecycle != catalogOpen || s.invalid != nil {
		return nil, fmt.Errorf("stuntgres: invalid catalog stage")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	view := e.stageRead(s)
	for _, d := range s.descriptors {
		p := e.providers[d.Address().ClassID]
		if p == nil {
			return nil, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := p.Validate(view, d); err != nil {
			return nil, err
		}
	}
	s.lifecycle = catalogValidated
	return &PreparedCatalogPublication{stage: s, epoch: s.base.Epoch(), ctx: ctx}, nil
}
func (e *Engine) PublishCatalog(p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if p == nil || p.stage == nil || p.stage.owner != e || p.stage.lifecycle != catalogValidated {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
	}
	s := p.stage
	changes := ChangeSet{Definitions: make([]ObjectAddress, 0, len(s.descriptors))}
	for a := range s.descriptors {
		changes.Definitions = append(changes.Definitions, a)
	}
	for {
		e.mu.Lock()
		if s.lifecycle != catalogValidated {
			e.mu.Unlock()
			return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
		}
		current := e.state
		currentStorage := e.storage
		if p.epoch != current.epoch || token.Epoch != current.epoch {
			e.mu.Unlock()
			return ChangeSet{}, ErrCatalogReplaced
		}
		for a := range s.descriptors {
			if _, ok := current.descriptors[a]; ok {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for k := range s.touched {
			if _, ok := current.names[k]; ok {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		e.mu.Unlock()
		next := cloneCatalog(current)
		storage := make(map[ObjectAddress]*relationStorage, len(currentStorage)+len(s.descriptors))
		for a, v := range currentStorage {
			storage[a] = v
		}
		for a, d := range s.descriptors {
			next.descriptors[a] = d
			if _, ok := d.(*relationDescriptor); ok {
				storage[a] = &relationStorage{}
			}
		}
		for k, a := range s.names {
			next.names[k] = a
		}
		e.mu.Lock()
		if e.state != current {
			e.mu.Unlock()
			continue
		}
		if err := p.ctx.Err(); err != nil {
			e.mu.Unlock()
			return ChangeSet{}, err
		}
		e.state = next
		e.storage = storage
		s.lifecycle = catalogPublished
		e.mu.Unlock()
		return changes, nil
	}
}
func (e *Engine) DiscardCatalogStage(s *CatalogStage) {
	if s != nil && s.owner == e && s.lifecycle != catalogPublished {
		s.lifecycle = catalogDiscarded
	}
}
func (e *Engine) ValidateObjectRef(view CatalogRead, ref ObjectRef) error {
	if e.owner == nil || view.owner != e || view.state == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if ref.owner == nil || ref.owner != e.owner {
		return fmt.Errorf("stuntgres: foreign catalog reference")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if view.state.epoch != e.state.epoch || ref.Epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	d, ok := view.state.descriptors[ObjectAddress{ClassID: ref.Address.ClassID, ObjectID: ref.Address.ObjectID}]
	if !ok {
		return fmt.Errorf("stuntgres: catalog object is missing")
	}
	if ref.Address.SubID < 0 {
		return fmt.Errorf("stuntgres: invalid catalog subobject")
	}
	if ref.Address.SubID > 0 {
		r, ok := d.(*relationDescriptor)
		if !ok || int(ref.Address.SubID) > len(r.attributes) {
			return fmt.Errorf("stuntgres: invalid catalog subobject")
		}
	}
	return nil
}
func (e *Engine) ReplaceCatalog(ctx context.Context, replacement CatalogRead) (ChangeSet, error) {
	if replacement.state == nil || replacement.owner == nil {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid replacement catalog")
	}
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	for _, d := range replacement.state.descriptors {
		p := e.providers[d.Address().ClassID]
		if p == nil {
			return ChangeSet{}, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := p.Validate(replacement, d); err != nil {
			return ChangeSet{}, err
		}
	}
	next := cloneCatalog(replacement.state)
	relationAddresses := make([]ObjectAddress, 0, len(next.descriptors))
	for a, d := range next.descriptors {
		if _, ok := d.(*relationDescriptor); ok {
			relationAddresses = append(relationAddresses, a)
		}
	}
	storage := make(map[ObjectAddress]*relationStorage, len(relationAddresses))
	copyRows := func(source map[ObjectAddress]*relationStorage) error {
		for _, a := range relationAddresses {
			if source[a] == nil {
				return fmt.Errorf("stuntgres: relation storage is missing")
			}
			rows := source[a].rows
			if replacement.owner != e {
				// Row values are immutable, but live engines must not share the
				// backing array into which INSERT appends new rows.
				rows = append([][]driver.Value(nil), rows...)
			}
			storage[a] = &relationStorage{rows: rows}
		}
		return nil
	}
	if replacement.owner == e {
		e.mu.Lock()
		defer e.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return ChangeSet{}, err
		}
		if replacement.Epoch() != e.state.epoch {
			return ChangeSet{}, ErrCatalogReplaced
		}
		if err := copyRows(e.storage); err != nil {
			return ChangeSet{}, err
		}
		if e.state.epoch == CatalogEpoch(math.MaxUint64) {
			return ChangeSet{}, fmt.Errorf("stuntgres: catalog epoch exhausted")
		}
		if err := ctx.Err(); err != nil {
			return ChangeSet{}, err
		}
		next.epoch = e.state.epoch + 1
		e.state = next
		e.storage = storage
		return ChangeSet{Replacement: true}, nil
	}
	replacement.owner.mu.Lock()
	if replacement.Epoch() != replacement.owner.state.epoch {
		replacement.owner.mu.Unlock()
		return ChangeSet{}, ErrCatalogReplaced
	}
	err := copyRows(replacement.owner.storage)
	replacement.owner.mu.Unlock()
	if err != nil {
		return ChangeSet{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	if e.state.epoch == CatalogEpoch(math.MaxUint64) {
		return ChangeSet{}, fmt.Errorf("stuntgres: catalog epoch exhausted")
	}
	next.epoch = e.state.epoch + 1
	e.state = next
	e.storage = storage
	return ChangeSet{Replacement: true}, nil
}
func cloneCatalog(s *catalogState) *catalogState {
	n := &catalogState{epoch: s.epoch, descriptors: make(map[ObjectAddress]Descriptor, len(s.descriptors)), names: make(map[NameKey]ObjectAddress, len(s.names))}
	for k, v := range s.descriptors {
		n.descriptors[k] = v
	}
	for k, v := range s.names {
		n.names[k] = v
	}
	return n
}
func (e *Engine) RegisterCatalogProvider(p CatalogProvider) error {
	if p == nil {
		return fmt.Errorf("stuntgres: nil catalog provider")
	}
	id := p.ClassID()
	if id == 0 {
		return fmt.Errorf("stuntgres: zero catalog class")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != nil {
		return fmt.Errorf("stuntgres: catalog providers are already initialized")
	}
	if e.providers == nil {
		return fmt.Errorf("stuntgres: engine is not initialized")
	}
	if _, ok := e.providers[id]; ok {
		return fmt.Errorf("stuntgres: duplicate catalog class")
	}
	e.providers[id] = p
	return nil
}
