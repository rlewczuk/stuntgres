package session

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (c *Journal) RecordCandidateSecurityLabels(base *catalog.State, candidate *catalog.Candidate) {
	for key, record := range candidate.SecurityLabelReads() {
		if _, private := c.SecurityLabels[key]; private {
			continue
		}
		if c.LabelReads == nil {
			c.LabelReads = make(map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord)
		}
		if _, exists := c.LabelReads[key]; !exists {
			c.LabelReads[key] = record
		}
	}
	for key, record := range candidate.SecurityLabelChanges() {
		c.RecordSecurityLabel(base, key, record)
	}
}

// RecordSecurityLabel retains the first committed predecessor, even when a
// later command replaces or removes the same transaction-private association.
// The caller owns the journal and holds Engine.Mu; records are immutable.
func (c *Journal) RecordSecurityLabel(committed *catalog.State, key catalog.SecurityLabelKey, record *catalog.SecurityLabelRecord) {
	if c.LabelOriginals == nil {
		c.LabelOriginals = make(map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord)
		c.SecurityLabels = make(map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord)
	}
	if _, exists := c.LabelOriginals[key]; !exists {
		c.LabelOriginals[key] = committed.SecurityLabel(key)
	}
	c.SecurityLabels[key] = record
	c.CachedBase = nil
	c.Cached = catalog.Read{}
}
func (c *Journal) mergeSecurityLabels(base, observed *catalog.State, candidate *catalog.Candidate) error {
	for key, record := range candidate.SecurityLabelReads() {
		expected := record
		if original, private := c.LabelOriginals[key]; private {
			expected = original
		}
		object := key.Object
		object.SubID = 0
		if base.SecurityLabel(key) != expected || !c.mergeObserved(base, observed, object) {
			return catalog.Conflict{}
		}
	}
	return nil
}
func (c *Journal) observeSecurityLabelObjects(base *catalog.State, candidate *catalog.Candidate) {
	for key := range candidate.SecurityLabelReads() {
		object := key.Object
		object.SubID = 0
		if _, exists := c.Originals[object]; !exists {
			c.Originals[object] = base.Get(object)
		}
	}
	c.RecordCandidateSecurityLabels(base, candidate)
}

// ObserveSecurityLabelPredecessor transfers only catalog reads, not the actual
// decision's subject, phase, policy or permission result, into a TX journal.
func (c *Journal) ObserveSecurityLabelPredecessor(committed *catalog.State, key catalog.SecurityLabelKey, record *catalog.SecurityLabelRecord, object catalog.Descriptor) error {
	address := key.Object
	address.SubID = 0
	if committed == nil || committed.Get(address) != object || committed.SecurityLabel(key) != record {
		return catalog.Conflict{}
	}
	if previous, present := c.Originals[address]; present && previous != object {
		return catalog.Conflict{}
	}
	if previous, present := c.LabelReads[key]; present && previous != record {
		return catalog.Conflict{}
	}
	if key.Object.ClassID == 0 || key.Object.ObjectID == 0 {
		return ids.ErrCatalogReplaced
	}
	c.Originals[address] = object
	if c.LabelReads == nil {
		c.LabelReads = make(map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord)
	}
	c.LabelReads[key] = record
	return nil
}
