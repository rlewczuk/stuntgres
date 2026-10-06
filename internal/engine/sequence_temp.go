package engine

import (
	"fmt"
	"stuntgres/internal/parser"
)

func temporaryNamespace(auth AuthorizationView) ObjectAddress {
	if auth.session == nil {
		return ObjectAddress{}
	}
	a := auth.session.temporaryNamespace
	if a != (ObjectAddress{}) {
		if d, ok := auth.catalog.state.descriptors[a].(*namespaceDescriptor); ok && d.temporary == auth.session.token {
			return a
		}
	}
	if owner := auth.session.transactions; owner != nil && owner.sequences != nil {
		for a, d := range owner.sequences.descriptors {
			if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == auth.session.token {
				return a
			}
		}
	}
	return ObjectAddress{}
}
func (e *Engine) sequenceCreationNamespaceLocked(auth AuthorizationView, s *parser.CreateSequence, c *sequenceChanges) (ObjectRef, error) {
	temp := s.Persistence == "temporary" || len(s.Name.Parts) == 2 && s.Name.Parts[0].Text == "pg_temp"
	if !temp && len(s.Name.Parts) == 1 {
		_, temp, _ = e.explicitPath(auth)
	}
	if !temp {
		return e.creationNamespace(auth, s.Name)
	}
	if len(s.Name.Parts) == 2 && s.Name.Parts[0].Text != "pg_temp" {
		return ObjectRef{}, sqlError("42P16", "cannot create temporary relation in non-temporary schema")
	}
	if a := temporaryNamespace(auth); a != (ObjectAddress{}) {
		return ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: a}, nil
	}
	oid, err := e.allocateOIDLocked()
	if err != nil {
		return ObjectRef{}, err
	}
	rev, err := e.revisionLocked()
	if err != nil {
		return ObjectRef{}, err
	}
	ns := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: oid}, name: fmt.Sprintf("pg_temp_%d", auth.session.token.id), revision: rev, owner: auth.state.CurrentUser, temporary: auth.session.token}
	e.sequenceChangeLocked(c, ns.address, ns)
	return ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: ns.address}, nil
}

// Physical-end cleanup occurs after command drain. Catalog edits remain coherent
// and remove only this session's temporary objects, never a peer's namespace.
// Logical namespace retirement is not a PostgreSQL namespace tuple deletion:
// physical backend exit preserves pg_namespace and cleans only temp relations.
// Keep peer analysis generation/epoch unchanged, including at scalar exhaustion.
func (e *Engine) endSequenceSession(s *Session) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var next *catalogState
	for _, d := range e.state.descriptors {
		if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == s.token {
			next = cloneCatalog(e.state)
			break
		}
	}
	if next == nil {
		s.sequences = nil
		s.lastSequence = ObjectAddress{}
		s.temporaryNamespace = ObjectAddress{}
		return
	}
	sequences := make(map[ObjectAddress]*sequenceGenerator, len(e.sequences))
	for a, g := range e.sequences {
		sequences[a] = g
	}
	storage := make(map[ObjectAddress]*relationStorage, len(e.storage))
	for a, rows := range e.storage {
		storage[a] = rows
	}
	for a, d := range next.descriptors {
		ns, ok := d.(*namespaceDescriptor)
		if !ok || ns.temporary != s.token {
			continue
		}
		for r, relation := range next.descriptors {
			if relation.Namespace() == a {
				delete(next.descriptors, r)
				delete(next.names, relationNameKey(relation))
				delete(sequences, r)
				delete(storage, r)
			}
		}
		delete(next.descriptors, a)
		delete(next.names, relationNameKey(ns))
	}
	e.state = next
	e.sequences = sequences
	e.storage = storage
	s.sequences = nil
	s.lastSequence = ObjectAddress{}
	s.temporaryNamespace = ObjectAddress{}
}
