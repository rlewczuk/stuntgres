package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
)

func (s *CatalogStage) MarkDrop(address ids.ObjectAddress, flags catalog.DropProvenance) {
	if s.statement != nil && !collectCommandDrops(s.Base, s.statement) {
		return
	}
	s.Candidate.MarkDrop(address, flags)
}

func collectCommandDrops(view catalog.Read, scope *StatementScope) bool {
	if scope == nil {
		return catalog.CollectDroppedObjects(view)
	}
	if scope.EventCommand == nil {
		return false
	}
	// Callers hold Engine.Mu; effective settings follow the authority lock order.
	auth, err := scope.RoutineAuthorizationLocked()
	if err != nil {
		return false
	}
	replica := auth.RuleReplica()
	for _, trigger := range view.EventTriggers() {
		if trigger.Matches("sql_drop", scope.EventCommand.Tag, replica) {
			return true
		}
	}
	return false
}

func (p *DropProvider) collectsDroppedObjects() bool {
	scope := p.Scope
	if scope == nil && p.Stage != nil {
		scope = p.Stage.statement
	}
	return collectCommandDrops(p.Base, scope)
}

func (p *DropProvider) dropFlags() map[ids.ObjectAddress]catalog.DropProvenance {
	if p.Stage != nil {
		return p.Stage.DropFlags
	}
	if p.Prepared != nil {
		return p.Prepared.DropFlags
	}
	return nil
}

// MarkDropRoots is called only by the SQL target owner, before closure broadens.
func (p *DropProvider) MarkDropRoots(roots []ids.ObjectAddress) {
	for _, address := range roots {
		p.MarkDrop(address, catalog.DropProvenance{Original: true})
	}
}

func (p *DropProvider) MarkDrop(address ids.ObjectAddress, flags catalog.DropProvenance) {
	if p.Stage != nil {
		p.Stage.MarkDrop(address, flags)
		return
	}
	if p.Prepared == nil || !p.collectsDroppedObjects() {
		return
	}
	if p.Prepared.DropFlags == nil {
		p.Prepared.DropFlags = make(map[ids.ObjectAddress]catalog.DropProvenance)
	}
	old := p.Prepared.DropFlags[address]
	old.Original = old.Original || flags.Original
	old.Normal = old.Normal || flags.Normal
	p.Prepared.DropFlags[address] = old
}

func (p *DropProvider) captureDropped(address ids.ObjectAddress, descriptor catalog.Descriptor, whole bool) ([]catalog.DroppedObject, error) {
	if p.InternalRemoval || !p.collectsDroppedObjects() {
		return nil, nil
	}
	switch address.ClassID {
	case ids.DatabaseClassID, ids.RoleClassID, ids.TablespaceClassID, ids.ParameterClassID, ids.EventTriggerClassID:
		return nil, nil
	}
	view := p.Base
	if descriptor != nil && descriptor != view.Get(descriptor.Address()) {
		retained := catalog.NewCandidate(view)
		retained.UpdateDescriptor(descriptor)
		view = view.WithState(retained.State())
	}
	roles := p.dropRoles
	if p.Stage != nil {
		roles = p.Stage.DropRoles
	} else if p.Scope != nil {
		roles = p.Scope.Inquiry.commentRoles
	}
	addresses := []ids.ObjectAddress{address}
	if whole {
		switch owner := descriptor.(type) {
		case *catalog.Relation:
			addresses = append(addresses, catalog.RelationPayloadAddresses(owner)...)
		case *catalog.Type:
			for _, check := range owner.DomainChecks {
				addresses = append(addresses, ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID})
			}
			if owner.DomainNotNull != nil {
				addresses = append(addresses, ids.ObjectAddress{ClassID: 2606, ObjectID: owner.DomainNotNull.OID})
			}
		}
	}
	var temporary ids.SessionToken
	if p.Scope != nil {
		temporary = p.Scope.Op.Session.SessionToken
	} else if p.Stage != nil && p.Stage.AuthSession != nil {
		temporary = p.Stage.AuthSession.SessionToken
	}
	records := make([]catalog.DroppedObject, 0, len(addresses))
	for _, a := range addresses {
		record, err := regobj.DroppedAddress(view, roles, p.SearchPath, a, temporary)
		if err != nil {
			return nil, err
		}
		flags := p.dropFlags()[a]
		record.Original, record.Normal = flags.Original, flags.Normal
		// A policy has a NORMAL dependency on its owning table.
		if whole && a.ClassID == ids.PolicyClassID {
			record.Normal = true
		}
		if record.Found {
			records = append(records, record)
		}
	}
	return records, nil
}

// CaptureRelationProjection captures only retired attributes and payload OIDs,
// never the surviving relation. before/after are the actual owner-step values.
func (p *DropProvider) CaptureRelationProjection(before, after *catalog.Relation) ([]catalog.DroppedObject, error) {
	if !p.collectsDroppedObjects() {
		return nil, nil
	}
	var records []catalog.DroppedObject
	for _, sub := range before.Attributes {
		if after.HasAttribute(sub) {
			continue
		}
		a := before.Address()
		a.SubID = sub
		captured, err := p.captureDropped(a, before, false)
		if err != nil {
			return nil, err
		}
		records = append(records, captured...)
	}
	live := make(map[ids.ObjectAddress]bool)
	for _, a := range catalog.RelationPayloadAddresses(after) {
		live[a] = true
	}
	for _, a := range catalog.RelationPayloadAddresses(before) {
		if live[a] {
			continue
		}
		captured, err := p.captureDropped(a, before, false)
		if err != nil {
			return nil, err
		}
		records = append(records, captured...)
	}
	return records, nil
}

// RecordRelationDeletion is for an explicit successful ALTER payload action.
// auth is the owner's immutable pre-step authority, not a lazy query snapshot.
func (s *StatementScope) RecordRelationDeletion(auth AuthorizationView, before *catalog.Relation, address ids.ObjectAddress) {
	p, err := s.alterDropProvider(auth)
	if err != nil {
		s.EventCommand.captureError = err
		return
	}
	if p == nil {
		return
	}
	records, err := p.captureDropped(address, before, false)
	if err != nil {
		s.EventCommand.captureError = err
		return
	}
	for i := range records {
		records[i].Original = true
	}
	s.RecordDroppedObjects(records)
}

func (s *StatementScope) alterDropProvider(auth AuthorizationView) (*DropProvider, error) {
	if !collectCommandDrops(auth.Catalog, s) {
		return nil, nil
	}
	path, err := ReferencePath(auth)
	if err != nil {
		return nil, err
	}
	// No Scope: its query inquiry may be uninitialized or from an earlier step.
	return &DropProvider{Base: auth.Catalog, Owner: s.Engine(), SearchPath: path, dropRoles: auth.Roles}, nil
}

func (s *StatementScope) recordDroppedProvenance(records []catalog.DroppedObject, flags map[ids.ObjectAddress]catalog.DropProvenance) {
	for _, record := range records {
		provenance := flags[record.Address]
		record.Original = record.Original || provenance.Original
		record.Normal = record.Normal || provenance.Normal
		s.RecordDroppedObjects([]catalog.DroppedObject{record})
	}
}

// CaptureTypeProjection retains only constraints retired by this type step.
func (p *DropProvider) CaptureTypeProjection(before, after *catalog.Type) ([]catalog.DroppedObject, error) {
	if !p.collectsDroppedObjects() {
		return nil, nil
	}
	var records []catalog.DroppedObject
	capture := func(oid ids.OID) error {
		captured, err := p.captureDropped(ids.ObjectAddress{ClassID: 2606, ObjectID: oid}, before, false)
		records = append(records, captured...)
		return err
	}
	for _, check := range before.DomainChecks {
		present := false
		for _, live := range after.DomainChecks {
			present = present || live.OID == check.OID
		}
		if !present {
			if err := capture(check.OID); err != nil {
				return nil, err
			}
		}
	}
	if before.DomainNotNull != nil && (after.DomainNotNull == nil || after.DomainNotNull.OID != before.DomainNotNull.OID) {
		if err := capture(before.DomainNotNull.OID); err != nil {
			return nil, err
		}
	}
	return records, nil
}
