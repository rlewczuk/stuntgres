package engine

import (
	"cmp"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func definitionObjectClass(class ids.OID) bool {
	return class == ids.CollationClassID || class == ids.ConversionClassID || class == ids.CastClassID || class == ids.ProcedureClassID
}

func compareDefinitionAddress(a, b ids.ObjectAddress) int {
	if n := cmp.Compare(a.ClassID, b.ClassID); n != 0 {
		return n
	}
	return cmp.Compare(a.ObjectID, b.ObjectID)
}

// Use the genuine catalog relation descriptor, not a synthetic lock identity.
func (s *StatementScope) acquireCollationCatalog(base catalog.Read) error {
	if base.Domain() != s.engine().catalog.Domain || base.Epoch() != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "pg_collation"}
	relation, ok := base.Relation(base.NameAddress(key))
	if !ok || relation.RelationKind != catalog.CatalogRelation {
		return fmt.Errorf("stuntgres: missing collation catalog lock authority")
	}
	ref := s.engine().catalog.Domain.Ref(base.Epoch(), relation.Address())
	return s.acquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.ShareRowExclusive)
}

// Capture stages are effect-free catalog candidates. Hold their original object
// identities before dependency discovery, then construct at most one final stage
// from current authority. This is admission recapture, not a command retry.
func (s *StatementScope) admitDefinitionMutation(stage *CatalogStage, stmt parser.Statement, input sqltype.InputEnvironment, noticeMark int) (*CatalogStage, error) {
	if stage == nil || stage.owner != s.engine() || stage.Base.Domain() != s.engine().catalog.Domain || stage.Base.Epoch() != s.txRef.Epoch() {
		return stage, ids.ErrCatalogReplaced
	}
	alter := false
	switch stmt.(type) {
	case *parser.AlterCollation, *parser.AlterConversion:
		alter = true
	}
	switch stmt.(type) {
	case *parser.AlterCollation, *parser.DropCollation,
		*parser.AlterConversion, *parser.DropConversion, *parser.DropCast, *parser.DropRoutine:
	default:
		return stage, nil
	}
	var scratch [8]ids.ObjectAddress
	addresses := scratch[:0]
	if alter {
		for _, original := range stage.domainBases {
			if definitionObjectClass(original.Address().ClassID) {
				addresses = append(addresses, original.Address())
			}
		}
	}
	for address, descriptor := range stage.Descriptors() {
		if definitionObjectClass(address.ClassID) && stage.Base.Get(address) != nil && descriptor != stage.Base.Get(address) {
			addresses = append(addresses, address)
		}
	}
	for address := range stage.Removals() {
		if definitionObjectClass(address.ClassID) {
			addresses = append(addresses, address)
		}
	}
	for _, address := range stage.domainTargets {
		if definitionObjectClass(address.ClassID) {
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		return stage, nil
	}
	slices.SortFunc(addresses, compareDefinitionAddress)
	addresses = slices.Compact(addresses)
	for _, address := range addresses {
		ref := s.engine().catalog.Domain.Ref(stage.Base.Epoch(), address)
		if err := s.acquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
			return stage, err
		}
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.schemaAuthorizationLocked(s.op)
	if err != nil {
		return stage, err
	}
	if auth.Catalog.Domain() != stage.Base.Domain() || auth.Catalog.Epoch() != stage.Base.Epoch() {
		return stage, ids.ErrCatalogReplaced
	}
	if auth.Catalog.State() == stage.Base.State() && auth.Revision == stage.authRevision && auth.RoleRecord.Revision == stage.roleRevision {
		return stage, nil
	}
	for _, address := range addresses {
		original := stage.Base.Get(address)
		if original == nil {
			return stage, ids.ErrCatalogReplaced
		}
		key := catalog.DescriptorNameKey(original)
		if alter {
			namespace := stage.Base.Get(key.Namespace)
			if auth.Catalog.NameAddress(key) != address || namespace == nil ||
				auth.Catalog.NameAddress(catalog.DescriptorNameKey(namespace)) != key.Namespace {
				return stage, ids.ErrCatalogReplaced
			}
		}
		if current := auth.Catalog.NameAddress(key); current != (ids.ObjectAddress{}) && current != address {
			return stage, ids.ErrCatalogReplaced
		}
	}
	// No notice from provisional construction is delivered twice. Notices from
	// earlier statements in a retained message remain owned by that message.
	s.op.Notices = s.op.Notices[:noticeMark]
	current, err := e.stageSchemaLocked(s.op, stmt, input, s.output)
	if err != nil {
		return stage, err
	}
	// Conditional multi-target drops cannot acquire a previously absent target
	// or a successor while another target's lock was pending.
	for _, address := range current.domainTargets {
		if definitionObjectClass(address.ClassID) && !slices.Contains(stage.domainTargets, address) {
			e.DiscardCatalogStage(current)
			return stage, ids.ErrCatalogReplaced
		}
	}
	// ALTER's resolved target is retained even for no-op actions. Replacements
	// and removals must all belong to the original admitted mutation set.
	for _, original := range current.domainBases {
		if alter && definitionObjectClass(original.Address().ClassID) && !slices.Contains(addresses, original.Address()) {
			e.DiscardCatalogStage(current)
			return stage, ids.ErrCatalogReplaced
		}
	}
	for address, descriptor := range current.Descriptors() {
		if definitionObjectClass(address.ClassID) && current.Base.Get(address) != nil &&
			descriptor != current.Base.Get(address) && !slices.Contains(addresses, address) {
			e.DiscardCatalogStage(current)
			return stage, ids.ErrCatalogReplaced
		}
	}
	for address := range current.Removals() {
		if definitionObjectClass(address.ClassID) && !slices.Contains(addresses, address) {
			e.DiscardCatalogStage(current)
			return stage, ids.ErrCatalogReplaced
		}
	}
	e.DiscardCatalogStage(stage)
	return current, nil
}

// Declaration consumers retain only actual dependencies, never runtime lookup
// choices. Resources use the same ticket/checkpoint/epoch lifetime as names.
func (s *StatementScope) acquireDefinitionObjectDependencies(stage *CatalogStage) error {
	if stage == nil || stage.owner != s.engine() || stage.Base.Domain() != s.engine().catalog.Domain || stage.Base.Epoch() != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	var scratch [16]ids.ObjectAddress
	addresses := scratch[:0]
	for _, descriptor := range stage.Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.Cast:
			addresses = append(addresses, catalog.CastDependencies(d)...)
		case *catalog.Conversion:
			addresses = append(addresses, d.Handler)
		case *catalog.Relation:
			previous, _ := stage.Base.Relation(d.Address())
			for slot, column := range d.Columns {
				if column.Usage.Collation.ID == 0 {
					continue
				}
				if previous != nil && slot < len(d.Attributes) {
					old := previous.IndexForAttribute(d.Attributes[slot])
					if old >= 0 && previous.Columns[old].Usage.Collation == column.Usage.Collation {
						continue
					}
				}
				address := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(column.Usage.Collation.ID)}
				if provider, ok := stage.Base.Collation(address); ok && provider.Seeded() {
					continue
				}
				addresses = append(addresses, address)
			}
		}
	}
	slices.SortFunc(addresses, compareDefinitionAddress)
	addresses = slices.Compact(addresses)
	for _, address := range addresses {
		// A dependency created by this very stage has no incumbent to retire.
		original := stage.Base.Get(address)
		if original == nil {
			continue
		}
		// Type identity is a captured publication precondition; taking its
		// mutation-only TypeExclusive lock would serialize unrelated cast pairs.
		if address.ClassID != ids.TypeClassID {
			ref := s.engine().catalog.Domain.Ref(stage.Base.Epoch(), address)
			if err := s.acquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
				return err
			}
		}
		e := s.engine()
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		if err == nil && auth.Catalog.Get(address) != original {
			err = ids.ErrCatalogReplaced
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if !slices.Contains(stage.domainBases, original) {
			stage.domainBases = append(stage.domainBases, original)
		}
	}
	return nil
}

// Cascaded retirement uses the same resources as direct mutations. The closure
// keeps its captured identities; a wait never adopts a successor descriptor.
func (s *StatementScope) acquireRetiredDefinitionObjects(base catalog.Read, targets []ids.ObjectAddress) error {
	if base.Domain() != s.engine().catalog.Domain || base.Epoch() != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	var scratch [8]ids.ObjectAddress
	addresses := scratch[:0]
	for _, address := range targets {
		if definitionObjectClass(address.ClassID) {
			addresses = append(addresses, address)
		}
	}
	slices.SortFunc(addresses, compareDefinitionAddress)
	addresses = slices.Compact(addresses)
	for _, address := range addresses {
		resource := lock.Resource{ObjectRef: s.engine().catalog.Domain.Ref(base.Epoch(), address)}
		if s.engine().locks.Holds(s.ticket, resource, lock.AccessExclusive) {
			continue
		}
		if err := s.acquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
		e := s.engine()
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		if err == nil && auth.Catalog.Get(address) != base.Get(address) {
			err = ids.ErrCatalogReplaced
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
