package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"stuntgres/internal/sqltype"
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
type relationStorage struct{ versions []rowVersion }

const (
	NamespaceClassID      OID = 2615
	RelationClassID       OID = 1259
	TypeClassID           OID = 1247
	PGCatalogNamespaceOID OID = 11
	PublicNamespaceOID    OID = 2200
)

type ObjectKind uint8

const (
	NamespaceObject ObjectKind = iota + 1
	RelationObject
	TypeObject
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
	owner    PrincipalRef
	acl      ACLValue
	comment  string
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
	columns            []ColumnDescriptor
	index              map[string]int
	attributes         []int32
	owner              PrincipalRef
	acl                ACLValue
	columnACL          map[int32]ACLValue
	comment            string
	columnComments     map[int32]string
}

func (d *relationDescriptor) Address() ObjectAddress       { return d.address }
func (d *relationDescriptor) Revision() DefinitionRevision { return d.revision }
func (d *relationDescriptor) Namespace() ObjectAddress     { return d.namespace }
func (d *relationDescriptor) Name() string                 { return d.name }
func (*relationDescriptor) Kind() ObjectKind               { return RelationObject }

func (d *relationDescriptor) hasAttribute(sub int32) bool {
	for _, live := range d.attributes {
		if live == sub { return true }
	}
	return false
}

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
		if r.attributes[i] <= 0 || i > 0 && r.attributes[i] <= r.attributes[i-1] || !exists || offset != i {
			return fmt.Errorf("stuntgres: invalid relation column identity")
		}
		if !c.Usage.Type.Concrete() || c.Usage.Type.ID() == sqltype.Name {
			return fmt.Errorf("stuntgres: invalid relation column type")
		}
		typ, exists := v.LookupType(c.Usage.Type.ID())
		if !exists || typ != c.Usage.Type || !typ.Concrete() || !c.Usage.Valid() {
			return fmt.Errorf("stuntgres: invalid relation column type")
		}
	}
	for sub, text := range r.columnComments {
		if !r.hasAttribute(sub) || text == "" {
			return fmt.Errorf("stuntgres: invalid relation column comment")
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

type catalogRemoval struct {
	ref        ObjectRef
	descriptor Descriptor
}

type catalogRowShape struct {
	original *relationStorage
	replacement *relationStorage
}

type CatalogStage struct {
	owner              *Engine
	base               CatalogRead
	descriptors        map[ObjectAddress]Descriptor
	names              map[NameKey]ObjectAddress
	touched            map[NameKey]ObjectAddress
	removals           map[ObjectAddress]catalogRemoval
	rowShapes map[ObjectAddress]catalogRowShape
	oldNames           map[NameKey]bool
	lifecycle          catalogLifecycle
	invalid            error
	authSession        *Session
	authRevision       uint64
	roleRevision       uint64
	schemaCascadeNames []string
}
type PreparedCatalogPublication struct {
	stage *CatalogStage
	epoch CatalogEpoch
	ctx   context.Context
}

func (e *Engine) BeginCatalogStage(base CatalogRead) *CatalogStage {
	s := &CatalogStage{owner: e, base: base, descriptors: make(map[ObjectAddress]Descriptor), names: make(map[NameKey]ObjectAddress), touched: make(map[NameKey]ObjectAddress)}
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
	s.touched[k] = ObjectAddress{}
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
func (s *CatalogStage) Replace(d Descriptor) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil || d == nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	old, ok := s.base.Descriptor(d.Address())
	if !ok {
		return CatalogConflict{}
	}
	oldKey := NameKey{ClassID: old.Address().ClassID, Namespace: old.Namespace(), Name: old.Name()}
	newKey := NameKey{ClassID: d.Address().ClassID, Namespace: d.Namespace(), Name: d.Name()}
	if a, exists := s.base.state.names[newKey]; exists && a != d.Address() {
		return CatalogConflict{}
	}
	if s.oldNames == nil {
		s.oldNames = make(map[NameKey]bool)
	}
	s.oldNames[oldKey] = true
	s.touched[oldKey] = s.base.state.names[oldKey]
	s.touched[newKey] = s.base.state.names[newKey]
	s.descriptors[d.Address()] = d
	s.names[newKey] = d.Address()
	return nil
}

// alterRelation couples a same-identity revision with an immutable storage root.
func (s *CatalogStage) alterRelation(ctx context.Context, ref ObjectRef, d *relationDescriptor, original, replacement *relationStorage) error {
	if s == nil || s.owner == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	e := s.owner
	e.mu.Lock()
	if err := e.validateObjectRefLocked(s.base, ref); err != nil { e.mu.Unlock(); return err }
	old, ok := s.base.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.Address.SubID != 0 || d == nil || d.address != old.address || d.namespace != old.namespace || d.name != old.name || old.revision == DefinitionRevision(^uint64(0)) || d.revision != old.revision+1 || original == nil || replacement == nil || e.storage[ref.Address] != original {
		e.mu.Unlock()
		return CatalogConflict{}
	}
	if len(original.versions) != len(replacement.versions) { e.mu.Unlock(); return CatalogConflict{} }
	beforeRows := original.versions
	e.mu.Unlock()
	for i, row := range replacement.versions {
		if err := ctx.Err(); err != nil { return err }
		before := beforeRows[i]
		if len(row.values) != len(d.columns) || row.row != before.row || row.version != before.version || row.born != before.born {
			return fmt.Errorf("stuntgres: invalid altered row identity")
		}
	}
	if err := s.Replace(d); err != nil { return err }
	if s.rowShapes == nil { s.rowShapes = make(map[ObjectAddress]catalogRowShape) }
	s.rowShapes[ref.Address] = catalogRowShape{original, replacement}
	return nil
}

// Remove stages a whole user relation with its coherent observed identity.
func (s *CatalogStage) Remove(ref ObjectRef) error {
	return s.removeObject(ref, false)
}

// Schema closure uses the same removal authority for namespaces and relations.
func (s *CatalogStage) removeObject(ref ObjectRef, namespace bool) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	return s.removeObjectLocked(ref, namespace)
}

// removeObjectLocked requires the owning engine mutex, including validation.
func (s *CatalogStage) removeObjectLocked(ref ObjectRef, namespace bool) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	if ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: whole object required")
	}
	if err := s.owner.validateObjectRefLocked(s.base, ref); err != nil {
		return err
	}
	d := s.base.state.descriptors[ref.Address]
	switch d.(type) {
	case *relationDescriptor:
		if ref.Address.ClassID != RelationClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	case *namespaceDescriptor:
		if !namespace || ref.Address.ClassID != NamespaceClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	default:
		return fmt.Errorf("stuntgres: only user relations can be removed")
	}
	if _, exists := s.removals[ref.Address]; exists {
		return nil
	}
	key := NameKey{ClassID: ref.Address.ClassID, Namespace: d.Namespace(), Name: d.Name()}
	if s.base.state.names[key] != ref.Address {
		return CatalogConflict{}
	}
	if s.removals == nil {
		s.removals = make(map[ObjectAddress]catalogRemoval)
	}
	s.removals[ref.Address] = catalogRemoval{ref: ref, descriptor: d}
	s.touched[key] = ref.Address
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
	for a, removal := range s.removals {
		d := removal.descriptor
		delete(ds, a)
		delete(ns, NameKey{ClassID: a.ClassID, Namespace: d.Namespace(), Name: d.Name()})
	}
	for k := range s.oldNames {
		delete(ns, k)
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
	var view CatalogRead
	if len(s.descriptors) != 0 {
		view = e.stageRead(s)
	}
	for _, d := range s.descriptors {
		p := e.providers[d.Address().ClassID]
		if p == nil {
			return nil, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := p.Validate(view, d); err != nil {
			return nil, err
		}
		e.mu.Lock()
		authErr := validateACL(RoleRead{e, e.roles}, d)
		e.mu.Unlock()
		if authErr != nil {
			return nil, authErr
		}
	}
	s.lifecycle = catalogValidated
	return &PreparedCatalogPublication{stage: s, epoch: s.base.Epoch(), ctx: ctx}, nil
}
func (e *Engine) PublishCatalog(p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	return e.publishCatalog(nil, p, token)
}
func (e *Engine) publishCatalogOperation(op *Operation, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	return e.publishCatalog(op, p, token)
}
func (e *Engine) publishCatalog(op *Operation, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if p == nil || p.stage == nil || p.stage.owner != e || p.stage.lifecycle != catalogValidated {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
	}
	s := p.stage
	changes := ChangeSet{Definitions: make([]ObjectAddress, 0, len(s.descriptors)+len(s.removals))}
	for a := range s.descriptors {
		changes.Definitions = append(changes.Definitions, a)
	}
	for a := range s.removals {
		changes.Definitions = append(changes.Definitions, a)
	}
	sort.Slice(changes.Definitions, func(i, j int) bool {
		a, b := changes.Definitions[i], changes.Definitions[j]
		if a.ClassID != b.ClassID {
			return a.ClassID < b.ClassID
		}
		if a.ObjectID != b.ObjectID {
			return a.ObjectID < b.ObjectID
		}
		return a.SubID < b.SubID
	})
	for _, a := range changes.Definitions {
		if a.ClassID == NamespaceClassID {
			changes.Namespaces = append(changes.Namespaces, a)
		}
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
		for _, d := range s.descriptors {
			ns := d.Namespace()
			if ns != (ObjectAddress{}) && s.descriptors[ns] == nil && current.descriptors[ns] != s.base.state.descriptors[ns] {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for a := range s.removals {
			if a.ClassID == NamespaceClassID {
				for address, d := range current.descriptors {
					if _, removed := s.removals[address]; d.Namespace() == a && (!removed || d != s.base.state.descriptors[address]) {
						e.mu.Unlock()
						return ChangeSet{}, CatalogConflict{}
					}
				}
			}
		}
		for a := range s.descriptors {
			if current.descriptors[a] != s.base.state.descriptors[a] {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for a := range s.removals {
			if current.descriptors[a] != s.base.state.descriptors[a] {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for k, observed := range s.touched {
			if current.names[k] != observed {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for a, removal := range s.removals {
			observed := removal.descriptor
			d := current.descriptors[a]
			if removal.ref.owner != e.owner || removal.ref.Epoch != current.epoch || removal.ref.Address != a || d == nil || d != observed || d.Revision() != observed.Revision() {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for a, shape := range s.rowShapes {
			if currentStorage[a] != shape.original || len(shape.original.versions) != len(shape.replacement.versions) {
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
		for a, removal := range s.removals {
			d := removal.descriptor
			delete(next.descriptors, a)
			delete(next.names, NameKey{ClassID: a.ClassID, Namespace: d.Namespace(), Name: d.Name()})
			delete(storage, a)
		}
		for k := range s.oldNames {
			delete(next.names, k)
		}
		for a, d := range s.descriptors {
			next.descriptors[a] = d
			if _, ok := d.(*relationDescriptor); ok && storage[a] == nil {
				storage[a] = &relationStorage{}
			}
		}
		for a, shape := range s.rowShapes { storage[a] = shape.replacement }
		for k, a := range s.names {
			next.names[k] = a
		}
		e.mu.Lock()
		if op != nil {
			op.session.mu.Lock()
		}
		if e.state != current {
			if op != nil {
				op.session.mu.Unlock()
			}
			e.mu.Unlock()
			continue
		}
		for a, shape := range s.rowShapes {
			if e.storage[a] != shape.original || len(shape.original.versions) != len(shape.replacement.versions) {
				if op != nil { op.session.mu.Unlock() }
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		err := p.ctx.Err()
		if err == nil && op != nil {
			err = publicationOperationLocked(e, op)
		}
		if err == nil {
			for _, d := range s.descriptors {
				if err = validateACL(RoleRead{e, e.roles}, d); err != nil {
					break
				}
			}
		}
		if err == nil && s.authSession != nil {
			// SQL stages use the operation's session, already locked above.
			if op == nil || s.authSession != op.session {
				s.authSession.mu.Lock()
			}
			auth := s.authSession.authorization
			valid := auth != nil && auth.revision == s.authRevision && e.roles.revision == s.roleRevision
			if op == nil || s.authSession != op.session {
				s.authSession.mu.Unlock()
			}
			if !valid {
				err = fmt.Errorf("stuntgres: authorization changed before publication")
			}
		}
		if err == nil {
			err = p.ctx.Err()
		}
		if err == nil {
			// Staged replacements may advance revisions without reserving from
			// the allocator. Commit its frontier with the successful publication.
			nextRevision := e.nextRevision
			for _, d := range s.descriptors {
				if revision := d.Revision(); revision >= nextRevision {
					nextRevision = revision
					if nextRevision != DefinitionRevision(math.MaxUint64) {
						nextRevision++
					}
				}
			}
			e.state = next
			e.storage = storage
			e.nextRevision = nextRevision
			s.lifecycle = catalogPublished
		}
		if op != nil {
			op.session.mu.Unlock()
		}
		e.mu.Unlock()
		if err != nil {
			return ChangeSet{}, err
		}
		return changes, nil
	}
}
func (e *Engine) DiscardCatalogStage(s *CatalogStage) {
	if s != nil && s.owner == e && s.lifecycle != catalogPublished {
		s.lifecycle = catalogDiscarded
	}
}
func (e *Engine) ValidateObjectRef(view CatalogRead, ref ObjectRef) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.validateObjectRefLocked(view, ref)
}

// validateObjectRefLocked preserves live epoch and provenance checks without
// reacquiring synchronization when called from catalog mutation preflight.
func (e *Engine) validateObjectRefLocked(view CatalogRead, ref ObjectRef) error {
	if e.owner == nil || view.owner != e || view.state == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if ref.owner == nil || ref.owner != e.owner {
		return fmt.Errorf("stuntgres: foreign catalog reference")
	}
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
		if !ok || !r.hasAttribute(ref.Address.SubID) {
			return fmt.Errorf("stuntgres: invalid catalog subobject")
		}
	}
	return nil
}
func (e *Engine) ReplaceCatalog(ctx context.Context, replacement CatalogRead) (ChangeSet, error) {
	var installed CatalogEpoch
	defer func() {
		if installed != 0 {
			e.locks.fence(installed)
		}
	}()
	if replacement.state == nil || replacement.owner == nil {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid replacement catalog")
	}
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	targetRoles := e.Roles()
	for _, d := range replacement.state.descriptors {
		p := e.providers[d.Address().ClassID]
		if p == nil {
			return ChangeSet{}, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := p.Validate(replacement, d); err != nil {
			return ChangeSet{}, err
		}
	}
	var next *catalogState
	relationAddresses := make([]ObjectAddress, 0, len(replacement.state.descriptors))
	for a, d := range replacement.state.descriptors {
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
			versions := source[a].versions
			if replacement.owner != e {
				versions = append([]rowVersion(nil), versions...)
			}
			// Captured prefixes/payloads are immutable. Foreign stores need their
			// own append capacity because their engines publish independently.
			storage[a] = &relationStorage{versions: versions}
		}
		return nil
	}
	if replacement.owner == e {
		e.replacementMu.Lock()
		defer e.replacementMu.Unlock()
		e.mu.Lock()
		defer e.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return ChangeSet{}, err
		}
		if replacement.Epoch() != e.state.epoch {
			return ChangeSet{}, ErrCatalogReplaced
		}
		next = cloneCatalog(replacement.state)
		if err := copyRows(e.storage); err != nil {
			return ChangeSet{}, err
		}
		nextRevision := e.nextRevision
		for _, d := range next.descriptors {
			if err := validateACL(RoleRead{e, e.roles}, d); err != nil {
				return ChangeSet{}, err
			}
			if revision := d.Revision(); revision >= nextRevision {
				nextRevision = revision
				if nextRevision != DefinitionRevision(math.MaxUint64) {
					nextRevision++
				}
			}
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
		e.nextRevision = nextRevision
		installed = next.epoch
		return ChangeSet{Replacement: true}, nil
	}
	replacement.owner.mu.Lock()
	if replacement.Epoch() != replacement.owner.state.epoch {
		replacement.owner.mu.Unlock()
		return ChangeSet{}, ErrCatalogReplaced
	}
	err := copyRows(replacement.owner.storage)
	sourceRoles := RoleRead{replacement.owner, replacement.owner.roles}
	replacement.owner.mu.Unlock()
	if err != nil {
		return ChangeSet{}, err
	}
	var imported uint64
	for _, relation := range storage {
		imported += uint64(len(relation.versions))
	}
	e.replacementMu.Lock()
	defer e.replacementMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	if e.state.epoch == CatalogEpoch(math.MaxUint64) {
		return ChangeSet{}, fmt.Errorf("stuntgres: catalog epoch exhausted")
	}
	if imported > ^uint64(0)-e.nextRow {
		return ChangeSet{}, fmt.Errorf("stuntgres: row identity exhausted")
	}
	if e.commitSeq == CommitSeq(^uint64(0)) {
		return ChangeSet{}, fmt.Errorf("stuntgres: commit sequence exhausted")
	}
	next, err = e.remapCatalog(replacement.state, sourceRoles, targetRoles, replacement.owner.importTargets)
	if err != nil {
		return ChangeSet{}, err
	}
	nextRevision := e.nextRevision
	for _, d := range next.descriptors {
		if err := validateACL(RoleRead{e, e.roles}, d); err != nil {
			return ChangeSet{}, err
		}
		if revision := d.Revision(); revision >= nextRevision {
			nextRevision = revision
			if nextRevision != DefinitionRevision(math.MaxUint64) {
				nextRevision++
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	// Fresh engine-local identities/frontier for this replacement. Old snapshots
	// retain source versions; no source payload or published entry is rewritten.
	for _, relation := range storage {
		for i := range relation.versions {
			e.nextRow++
			relation.versions[i].row = RowID(e.nextRow)
			relation.versions[i].version = VersionID(e.nextRow)
			relation.versions[i].born = e.commitSeq + 1
		}
	}
	e.commitSeq++
	next.epoch = e.state.epoch + 1
	e.state = next
	e.storage = storage
	e.nextRevision = nextRevision
	installed = next.epoch
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
