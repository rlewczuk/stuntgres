package engine

import (
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// journalCapture records the committed observations and fences of a
// validated candidate into journal. committed is the published catalog and
// current the transaction's private catalog the candidate was validated
// against. Caller holds Engine.mu and the session's statement admission.
type journalCapture struct {
	journal   *session.Journal
	candidate *catalog.Candidate
	committed *catalog.State
	current   *catalog.State
}

// observeDatabase fences the database descriptor at address: current must
// still hold authority and committed predecessor, which becomes the journal's
// original.
func (p journalCapture) observeDatabase(address ids.ObjectAddress, authority, predecessor catalog.Descriptor) error {
	if p.current.Get(address) != authority || p.committed.Get(address) != predecessor {
		return ids.ErrCatalogReplaced
	}
	if original, ok := p.journal.Originals[address]; ok && original != predecessor {
		return catalog.Conflict{}
	}
	p.journal.Originals[address] = predecessor
	return nil
}

// observe fails unless current still holds d at a, then records committed's
// descriptor at a as the journal's original unless it already has one.
func (p journalCapture) observe(a ids.ObjectAddress, d catalog.Descriptor) error {
	if p.current.Get(a) != d {
		return catalog.Conflict{}
	}
	if _, ok := p.journal.Originals[a]; !ok {
		p.journal.Originals[a] = p.committed.Get(a)
	}
	return nil
}

// observeBase observes the candidate's base descriptor at a, if it has one.
func (p journalCapture) observeBase(a ids.ObjectAddress) error {
	if d := p.candidate.Base.Get(a); d != nil {
		return p.observe(a, d)
	}
	return nil
}

// observeCandidate observes the base descriptor of every candidate change and
// removal, then the namespaces of the changed descriptors and of their base
// descriptors.
func (p journalCapture) observeCandidate() error {
	for a := range p.candidate.Descriptors() {
		if err := p.observe(a, p.candidate.Base.Get(a)); err != nil {
			return err
		}
	}
	for a := range p.candidate.Removals() {
		if err := p.observe(a, p.candidate.Base.Get(a)); err != nil {
			return err
		}
	}
	for _, d := range p.candidate.Descriptors() {
		if err := p.observeNamespace(d.Namespace()); err != nil {
			return err
		}
		if old := p.candidate.Base.Get(d.Address()); old != nil {
			if err := p.observeNamespace(old.Namespace()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p journalCapture) observeNamespace(ns ids.ObjectAddress) error {
	d := p.candidate.Base.Get(ns)
	if d == nil {
		return nil
	}
	// TEMP creation can add its namespace to the candidate before
	// constructing the ordinary stage. That exact creation intent is part of
	// the stage's entry authority, not a later projection. Its committed
	// absence/name fences already belong to the journal.
	if p.current.Get(ns) == nil {
		c := p.journal
		original, created := c.Originals[ns]
		key := ids.NameKey{ClassID: ids.NamespaceClassID, Name: d.Name()}
		_, named := c.Names[key]
		if created && original == nil && c.Descriptors[ns] == d && named && p.current.NameAddress(key) == (ids.ObjectAddress{}) {
			return nil
		}
	}
	return p.observe(ns, d)
}

// fenceNames observes the candidate's touched names, then fences the
// committed index set of each parent in indexParents, observing the parent's
// current indexes first, and the committed members of each removed
// namespace.
func (p journalCapture) fenceNames(indexParents iter.Seq[ids.ObjectAddress]) error {
	c := p.journal
	for k, a := range p.candidate.Touched() {
		if p.current.NameAddress(k) != a {
			return catalog.Conflict{}
		}
		if _, ok := c.Names[k]; !ok {
			c.Names[k] = p.committed.NameAddress(k)
		}
	}
	for parent := range indexParents {
		for address, descriptor := range p.current.Descriptors() {
			index, ok := descriptor.(*catalog.Relation)
			if ok && index.SQLIndex != nil && index.SQLIndex.Parent == parent {
				if err := p.observe(address, index); err != nil {
					return err
				}
			}
		}
		c.AddFence(newCatalogIndexFence(p.committed, parent))
	}
	for a := range p.candidate.Removals() {
		if a.ClassID == ids.NamespaceClassID {
			c.AddFence(newCatalogNamespaceFence(p.committed, a))
		}
	}
	return nil
}

// mergeCandidate records candidate's changes and removals into c after
// checking that committed still holds, for each of them, the descriptor
// observed holds, or c's original where c already changed it.
func mergeCandidate(c *session.Journal, committed, observed *catalog.State, candidate *catalog.Candidate) error {
	for a := range candidate.Descriptors() {
		if !mergeObserved(c, committed, observed, a) {
			return catalog.Conflict{}
		}
	}
	for a := range candidate.Removals() {
		if !mergeObserved(c, committed, observed, a) {
			return catalog.Conflict{}
		}
	}
	for a, d := range candidate.Descriptors() {
		c.RecordDescriptor(committed, a, d)
	}
	for a := range candidate.Removals() {
		c.RecordDescriptor(committed, a, nil)
	}
	return nil
}

func mergeObserved(c *session.Journal, committed, observed *catalog.State, a ids.ObjectAddress) bool {
	expected := observed.Get(a)
	if original, private := c.Originals[a]; private {
		expected = original
	}
	return committed.Get(a) == expected
}
