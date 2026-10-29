package session

import (
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// JournalCapture records the committed observations and fences of a
// validated candidate into Journal: the retained transaction's journal takes
// over what the statement's stage observed. Committed is the published
// catalog and Current the transaction's private catalog the candidate was
// validated against. The root runs the methods under Engine.mu and the
// session's statement admission, interleaved with its own steps; they take no
// lock.
type JournalCapture struct {
	Journal   *Journal
	Candidate *catalog.Candidate
	Committed *catalog.State
	Current   *catalog.State
}

// ObserveDatabase fences the database descriptor at address: Current must
// still hold authority and Committed predecessor, which becomes the journal's
// original.
func (p JournalCapture) ObserveDatabase(address ids.ObjectAddress, authority, predecessor catalog.Descriptor) error {
	if p.Current.Get(address) != authority || p.Committed.Get(address) != predecessor {
		return ids.ErrCatalogReplaced
	}
	if original, ok := p.Journal.Originals[address]; ok && original != predecessor {
		return catalog.Conflict{}
	}
	p.Journal.Originals[address] = predecessor
	return nil
}

// Observe fails unless Current still holds d at a, then records Committed's
// descriptor at a as the journal's original unless it already has one.
func (p JournalCapture) Observe(a ids.ObjectAddress, d catalog.Descriptor) error {
	if p.Current.Get(a) != d {
		return catalog.Conflict{}
	}
	if _, ok := p.Journal.Originals[a]; !ok {
		p.Journal.Originals[a] = p.Committed.Get(a)
	}
	return nil
}

// ObserveBase observes the candidate's base descriptor at a, if it has one.
func (p JournalCapture) ObserveBase(a ids.ObjectAddress) error {
	if d := p.Candidate.Base.Get(a); d != nil {
		return p.Observe(a, d)
	}
	return nil
}

// ObserveCandidate observes the base descriptor of every candidate change and
// removal, then the namespaces of the changed descriptors and of their base
// descriptors.
func (p JournalCapture) ObserveCandidate() error {
	for address := range p.Candidate.InitialReads() {
		address.SubID = 0
		if err := p.ObserveBase(address); err != nil {
			return err
		}
	}
	for a := range p.Candidate.Descriptors() {
		if err := p.Observe(a, p.Candidate.Base.Get(a)); err != nil {
			return err
		}
	}
	for a := range p.Candidate.Removals() {
		if err := p.Observe(a, p.Candidate.Base.Get(a)); err != nil {
			return err
		}
	}
	for _, d := range p.Candidate.Descriptors() {
		if err := p.observeNamespace(d.Namespace()); err != nil {
			return err
		}
		if old := p.Candidate.Base.Get(d.Address()); old != nil {
			if err := p.observeNamespace(old.Namespace()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p JournalCapture) observeNamespace(ns ids.ObjectAddress) error {
	d := p.Candidate.Base.Get(ns)
	if d == nil {
		return nil
	}
	// TEMP creation can add its namespace to the candidate before
	// constructing the ordinary stage. That exact creation intent is part of
	// the stage's entry authority, not a later projection. Its committed
	// absence/name fences already belong to the journal.
	if p.Current.Get(ns) == nil {
		c := p.Journal
		original, created := c.Originals[ns]
		key := ids.NameKey{ClassID: ids.NamespaceClassID, Name: d.Name()}
		_, named := c.Names[key]
		if created && original == nil && c.Descriptors[ns] == d && named && p.Current.NameAddress(key) == (ids.ObjectAddress{}) {
			return nil
		}
	}
	return p.Observe(ns, d)
}

// FenceNames observes the candidate's touched names, then fences the
// committed index set of each parent in indexParents (nil means no index
// parents), observing the parent's current indexes first, and the committed
// members of each removed namespace.
func (p JournalCapture) FenceNames(indexParents iter.Seq[ids.ObjectAddress]) error {
	c := p.Journal
	for k, a := range p.Candidate.Touched() {
		if p.Current.NameAddress(k) != a {
			return catalog.Conflict{}
		}
		if _, ok := c.Names[k]; !ok {
			c.Names[k] = p.Committed.NameAddress(k)
		}
	}
	if indexParents != nil {
		for parent := range indexParents {
			for address, index := range p.Current.Relations() {
				if index.SQLIndex != nil && index.SQLIndex.Parent == parent {
					if err := p.Observe(address, index); err != nil {
						return err
					}
				}
			}
			c.AddFence(NewIndexSetFence(p.Committed, parent))
		}
	}
	for a := range p.Candidate.Removals() {
		if a.ClassID == ids.NamespaceClassID {
			c.AddFence(NewNamespaceFence(p.Committed, a))
		}
	}
	return nil
}
