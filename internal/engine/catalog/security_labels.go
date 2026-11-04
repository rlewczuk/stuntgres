package catalog

import (
	"iter"
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// SecurityLabelKey includes the signed stable attribute number, not a dense slot.
type SecurityLabelKey struct {
	Object   ids.ObjectAddress
	Provider string
}

// SecurityLabelRecord is immutable once associated with any state or candidate.
type SecurityLabelRecord struct{ Label, PolicyID, PolicyVersion string }

// SecurityLabelRoot is a comparable opaque identity of immutable associations,
// not label authority or a mutable side map.
type SecurityLabelRoot struct {
	root util.PersistentMap[SecurityLabelKey, *SecurityLabelRecord]
}

func (s *State) SecurityLabelRoot() SecurityLabelRoot {
	return SecurityLabelRoot{root: s.securityLabels}
}
func (s *State) SecurityLabel(key SecurityLabelKey) *SecurityLabelRecord {
	record, _ := s.securityLabels.Get(key)
	return record
}
func (s *State) SecurityLabels() iter.Seq2[SecurityLabelKey, *SecurityLabelRecord] {
	return s.securityLabels.All()
}
func (s *State) SecurityLabelCount() int { return s.securityLabels.Len() }

// SetSecurityLabel writes only to unpublished state, preserving retained roots.
func (s *State) SetSecurityLabel(key SecurityLabelKey, record *SecurityLabelRecord) {
	if record == nil {
		s.securityLabels = s.securityLabels.Delete(key)
	} else {
		s.securityLabels = s.securityLabels.Set(key, record)
	}
}
func (s *State) OverlaySecurityLabels(base *State, changes map[SecurityLabelKey]*SecurityLabelRecord) {
	s.securityLabels = base.securityLabels
	for key, record := range changes {
		s.SetSecurityLabel(key, record)
	}
}
func (s *State) ClearSecurityLabels() {
	s.securityLabels = util.PersistentMap[SecurityLabelKey, *SecurityLabelRecord]{}
}
func (c *Candidate) ObserveSecurityLabel(key SecurityLabelKey) {
	if _, ok := c.labelReads[key]; ok {
		return
	}
	if c.labelReads == nil {
		c.labelReads = make(map[SecurityLabelKey]*SecurityLabelRecord)
	}
	c.labelReads[key] = c.Base.State().SecurityLabel(key)
}
func (c *Candidate) SecurityLabel(key SecurityLabelKey) *SecurityLabelRecord {
	if record, ok := c.securityLabels[key]; ok {
		return record
	}
	return c.Base.State().SecurityLabel(key)
}
func (c *Candidate) RecordSecurityLabel(key SecurityLabelKey, record *SecurityLabelRecord) {
	c.ObserveSecurityLabel(key)
	if previous := c.SecurityLabel(key); previous == record || previous != nil && record != nil && *previous == *record {
		return
	}
	if c.securityLabels == nil {
		c.securityLabels = make(map[SecurityLabelKey]*SecurityLabelRecord)
	}
	if record != nil {
		copy := *record
		record = &copy
	}
	c.securityLabels[key] = record
}
func (c *Candidate) SecurityLabelChanges() iter.Seq2[SecurityLabelKey, *SecurityLabelRecord] {
	return maps.All(c.securityLabels)
}
func (c *Candidate) SecurityLabelReads() iter.Seq2[SecurityLabelKey, *SecurityLabelRecord] {
	return maps.All(c.labelReads)
}
func (c *Candidate) SecurityLabelChangeCount() int { return len(c.securityLabels) }
func (c *Candidate) ApplySecurityLabels(source, next *State) {
	next.OverlaySecurityLabels(source, c.securityLabels)
}
func SecurityLabelsUnchanged(current *State, reads map[SecurityLabelKey]*SecurityLabelRecord) bool {
	for key, record := range reads {
		if current.SecurityLabel(key) != record {
			return false
		}
	}
	return true
}
func (c *Candidate) SecurityLabelsUnchanged(current *State) bool {
	return c.securityLabelsUnchanged(current, nil)
}

func (c *Candidate) securityLabelsUnchanged(current *State, namespaces []*Namespace) bool {
	for key, record := range c.labelReads {
		object := key.Object
		object.SubID = 0
		base := c.Base.Get(object)
		if current.Get(object) != base || current.SecurityLabel(key) != record {
			created := false
			for _, ns := range namespaces {
				if key.Object == ns.Address() && base == ns && current.Get(object) == nil &&
					current.SecurityLabel(key) == nil && c.Base.State().SecurityLabel(key) == record {
					created = true
					break
				}
			}
			if created {
				continue
			}
			return false
		}
	}
	for key := range current.SecurityLabels() {
		object := key.Object
		object.SubID = 0
		if _, removed := c.Removal(object); removed {
			if _, observed := c.labelReads[key]; !observed {
				return false
			}
		}
	}
	return true
}
func (c *Candidate) RemoveSecurityLabels(object ids.ObjectAddress) {
	matches := func(key SecurityLabelKey) bool {
		a := key.Object
		return a.ClassID == object.ClassID && a.ObjectID == object.ObjectID && (object.SubID == 0 || object.SubID == a.SubID)
	}
	for key := range c.Base.State().SecurityLabels() {
		if matches(key) {
			c.RecordSecurityLabel(key, nil)
		}
	}
	for key, record := range c.securityLabels {
		if record != nil && matches(key) {
			c.RecordSecurityLabel(key, nil)
		}
	}
}
func (s *State) sharedLabelsEqual(other *State) bool {
	for key, record := range s.SecurityLabels() {
		if sharedClass(key.Object) && other.SecurityLabel(key) != record {
			return false
		}
	}
	for key, record := range other.SecurityLabels() {
		if sharedClass(key.Object) && s.SecurityLabel(key) != record {
			return false
		}
	}
	return true
}
func (s *State) projectSharedLabels(source *State) {
	for key := range s.SecurityLabels() {
		if sharedClass(key.Object) {
			s.SetSecurityLabel(key, nil)
		}
	}
	for key, record := range source.SecurityLabels() {
		if sharedClass(key.Object) {
			s.SetSecurityLabel(key, record)
		}
	}
}

// InheritDatabaseSecurityLabelsLocked restores destination shared database
// associations while attaching a privately copied template. The new
// database's generated label is added separately at publication.
func (s *Store) InheritDatabaseSecurityLabelsLocked(next *State) {
	for key := range next.SecurityLabels() {
		if key.Object.ClassID == ids.DatabaseClassID {
			next.SetSecurityLabel(key, nil)
		}
	}
	for key, record := range s.State.SecurityLabels() {
		if key.Object.ClassID == ids.DatabaseClassID {
			next.SetSecurityLabel(key, record)
		}
	}
}
