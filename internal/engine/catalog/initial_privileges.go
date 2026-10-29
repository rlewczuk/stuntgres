package catalog

import (
	"fmt"
	"iter"
	"maps"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// InitialPrivilegeRecord is an immutable historical ACL, not an effective ACL.
// Its identity is the complete object address; it has no separately allocated OID.
type InitialPrivilegeRecord struct {
	PrivType byte
	ACL      authorization.ACLValue
}

func CloneInitialPrivileges(source map[ids.ObjectAddress]*InitialPrivilegeRecord) map[ids.ObjectAddress]*InitialPrivilegeRecord {
	next := make(map[ids.ObjectAddress]*InitialPrivilegeRecord, len(source))
	maps.Copy(next, source)
	return next
}

func overlayInitialPrivileges(source, changes map[ids.ObjectAddress]*InitialPrivilegeRecord) map[ids.ObjectAddress]*InitialPrivilegeRecord {
	if len(changes) == 0 {
		return source
	}
	next := CloneInitialPrivileges(source)
	for address, record := range changes {
		if record == nil {
			delete(next, address)
		} else {
			next[address] = record
		}
	}
	return next
}

func (s *State) InitialPrivilege(address ids.ObjectAddress) *InitialPrivilegeRecord {
	return s.initialPrivileges[address]
}
func (s *State) InitialPrivileges() iter.Seq2[ids.ObjectAddress, *InitialPrivilegeRecord] {
	return maps.All(s.initialPrivileges)
}
func (s *State) InitialPrivilegeCount() int { return len(s.initialPrivileges) }

// InitialPrivilegeBaseline is immutable canonical seed comparison provenance.
// Its entries are not SQL rows or live object/principal dependencies.
func (s *State) InitialPrivilegeBaseline() iter.Seq2[ids.ObjectAddress, *InitialPrivilegeRecord] {
	return maps.All(s.initialPrivilegeBaseline)
}

func (s *State) InitialPrivilegeBaselineCount() int { return len(s.initialPrivilegeBaseline) }

// SetInitialPrivilegeBaseline transfers ownership during bootstrap or native
// identity remapping only. Current-history replacement never calls this.
func (s *State) SetInitialPrivilegeBaseline(records map[ids.ObjectAddress]*InitialPrivilegeRecord) {
	s.initialPrivilegeBaseline = records
}

// SetInitialPrivileges transfers ownership of an unpublished map to the state.
func (s *State) SetInitialPrivileges(records map[ids.ObjectAddress]*InitialPrivilegeRecord) {
	s.initialPrivileges = records
}
func (s *State) OverlayInitialPrivileges(source *State, changes map[ids.ObjectAddress]*InitialPrivilegeRecord) {
	s.initialPrivileges = overlayInitialPrivileges(source.initialPrivileges, changes)
}

func (c *Candidate) ObserveInitialPrivilege(address ids.ObjectAddress) {
	if _, exists := c.initialReads[address]; exists {
		return
	}
	if c.initialReads == nil {
		c.initialReads = make(map[ids.ObjectAddress]*InitialPrivilegeRecord)
	}
	c.initialReads[address] = c.Base.State().InitialPrivilege(address)
}

// RecordInitialPrivilege copies caller-owned ACL backing and preserves an
// existing record's origin on a script-equivalent privilege event.
func (c *Candidate) RecordInitialPrivilege(address ids.ObjectAddress, kind byte, acl authorization.ACLValue) {
	c.ObserveInitialPrivilege(address)
	previous, changed := c.initialPrivileges[address]
	if !changed {
		previous = c.Base.State().InitialPrivilege(address)
	}
	if !acl.Explicit || len(acl.Entries) == 0 {
		if previous == nil {
			return
		}
		if c.initialPrivileges == nil {
			c.initialPrivileges = make(map[ids.ObjectAddress]*InitialPrivilegeRecord)
		}
		c.initialPrivileges[address] = nil
		return
	}
	if previous != nil && authorization.EqualACL(previous.ACL, acl) {
		return
	}
	if c.initialPrivileges == nil {
		c.initialPrivileges = make(map[ids.ObjectAddress]*InitialPrivilegeRecord)
	}
	if previous != nil {
		kind = previous.PrivType
	}
	acl.Entries = slices.Clone(acl.Entries)
	c.initialPrivileges[address] = &InitialPrivilegeRecord{PrivType: kind, ACL: acl}
}

func (c *Candidate) InitialPrivileges() iter.Seq2[ids.ObjectAddress, *InitialPrivilegeRecord] {
	return maps.All(c.initialPrivileges)
}
func (c *Candidate) InitialReads() iter.Seq2[ids.ObjectAddress, *InitialPrivilegeRecord] {
	return maps.All(c.initialReads)
}
func (c *Candidate) InitialPrivilegeCount() int { return len(c.initialPrivileges) }
func (c *Candidate) ApplyInitialPrivileges(source, next *State) {
	next.OverlayInitialPrivileges(source, c.initialPrivileges)
}
func InitialPrivilegesUnchanged(current *State, reads map[ids.ObjectAddress]*InitialPrivilegeRecord) bool {
	for address, record := range reads {
		if current.InitialPrivilege(address) != record {
			return false
		}
	}
	return true
}
func (c *Candidate) InitialPrivilegesUnchanged(current *State) bool {
	if !InitialPrivilegesUnchanged(current, c.initialReads) {
		return false
	}
	for address := range c.initialReads {
		object := address
		object.SubID = 0
		if current.Get(object) != c.Base.Get(object) {
			return false
		}
	}
	if len(c.removals) == 0 {
		return true
	}
	for address := range current.InitialPrivileges() {
		object := address
		object.SubID = 0
		if _, removed := c.Removal(object); removed {
			if _, observed := c.initialReads[address]; !observed {
				return false
			}
		}
	}
	return true
}

// RemoveInitialPrivileges removes all column snapshots as well as the object.
func (c *Candidate) RemoveInitialPrivileges(object ids.ObjectAddress) {
	if object.SubID != 0 {
		record, changed := c.initialPrivileges[object]
		if !changed {
			record = c.Base.State().InitialPrivilege(object)
		}
		if record != nil {
			c.RecordInitialPrivilege(object, 'e', authorization.ACLValue{})
		}
		return
	}
	matches := func(address ids.ObjectAddress) bool {
		return address.ClassID == object.ClassID && address.ObjectID == object.ObjectID && (object.SubID == 0 || address.SubID == object.SubID)
	}
	for address := range c.Base.State().InitialPrivileges() {
		if matches(address) {
			c.RecordInitialPrivilege(address, 'e', authorization.ACLValue{})
		}
	}
	for address, record := range c.initialPrivileges {
		if record != nil && matches(address) {
			c.RecordInitialPrivilege(address, 'e', authorization.ACLValue{})
		}
	}
}

func InitialPrivilegeMask(view Read, address ids.ObjectAddress) (authorization.Privileges, error) {
	object := address
	object.SubID = 0
	d, ok := view.Descriptor(object)
	if !ok || d == nil || d.Address() != object {
		return 0, Conflict{}
	}
	if address.SubID != 0 {
		r, ok := d.(*Relation)
		if !ok || address.SubID < 0 {
			return 0, Conflict{}
		}
		if r.RelationKind == IndexRelation || r.RelationKind == CompositeRelation {
			return 0, fmt.Errorf("stuntgres: unsupported initial privilege column")
		}
		for _, attribute := range r.Attributes {
			if attribute == address.SubID {
				return authorization.ColumnGrantPrivileges, nil
			}
		}
		return 0, Conflict{}
	}
	switch d := d.(type) {
	case *Relation:
		if d.RelationKind == SequenceRelation {
			return authorization.DefaultMask(authorization.DefaultSequences), nil
		}
		if d.RelationKind == TableRelation || d.RelationKind == CatalogRelation || d.RelationKind == ViewRelation || d.RelationKind == MaterializedViewRelation || d.RelationKind == ForeignRelation || d.RelationKind == PartitionedRelation {
			return authorization.TableGrantPrivileges, nil
		}
	case *Namespace:
		return authorization.UsagePrivilege | authorization.CreatePrivilege, nil
	case *Procedure:
		return authorization.ExecutePrivilege, nil
	case *Type, *Language:
		return authorization.UsagePrivilege, nil
	case *LargeObject:
		return authorization.SelectPrivilege | authorization.UpdatePrivilege, nil
	case *ForeignObject:
		if address.ClassID == ids.ForeignWrapperClassID || address.ClassID == ids.ForeignServerClassID {
			return authorization.UsagePrivilege, nil
		}
	}
	return 0, fmt.Errorf("stuntgres: unsupported initial privilege address")
}

func ValidateInitialPrivileges(roles authorization.Read, view Read) error {
	for address, record := range view.State().InitialPrivileges() {
		if record == nil || (record.PrivType != 'i' && record.PrivType != 'e') || !record.ACL.Explicit || len(record.ACL.Entries) == 0 {
			return fmt.Errorf("stuntgres: invalid initial privilege record")
		}
		mask, err := InitialPrivilegeMask(view, address)
		if err != nil {
			return err
		}
		if err := authorization.ValidateACL(roles, record.ACL, mask); err != nil {
			return err
		}
	}
	return nil
}
