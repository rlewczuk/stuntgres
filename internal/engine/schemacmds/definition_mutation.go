package schemacmds

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Use the genuine catalog relation descriptor, not a synthetic lock identity.
func acquireCollationCatalog(s *core.StatementScope, base catalog.Read) error {
	if base.Domain() != s.Engine().Catalog.Domain || base.Epoch() != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "pg_collation"}
	relation, ok := base.Relation(base.NameAddress(key))
	if !ok || relation.RelationKind != catalog.CatalogRelation {
		return fmt.Errorf("stuntgres: missing collation catalog lock authority")
	}
	ref := s.Engine().Catalog.Domain.Ref(base.Epoch(), relation.Address())
	return s.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.ShareRowExclusive)
}

// Capture stages are effect-free catalog candidates. Hold their original object
// identities before dependency discovery, then construct at most one final stage
// from current authority. This is admission recapture, not a command retry.
func admitDefinitionMutation(s *core.StatementScope, stage *core.CatalogStage, stmt parser.Statement, input sqltype.InputEnvironment, noticeMark int) (*core.CatalogStage, error) {
	if stage == nil || stage.Owner != s.Engine() || stage.Base.Domain() != s.Engine().Catalog.Domain || stage.Base.Epoch() != s.TxRef.Epoch() {
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
		for _, original := range stage.DomainBases {
			if core.DefinitionObjectClass(original.Address().ClassID) {
				addresses = append(addresses, original.Address())
			}
		}
	}
	for address, descriptor := range stage.Descriptors() {
		if core.DefinitionObjectClass(address.ClassID) && stage.Base.Get(address) != nil && descriptor != stage.Base.Get(address) {
			addresses = append(addresses, address)
		}
	}
	for address := range stage.Removals() {
		if core.DefinitionObjectClass(address.ClassID) {
			addresses = append(addresses, address)
		}
	}
	for _, address := range stage.DomainTargets {
		if core.DefinitionObjectClass(address.ClassID) {
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		return stage, nil
	}
	slices.SortFunc(addresses, core.CompareDefinitionAddress)
	addresses = slices.Compact(addresses)
	for _, address := range addresses {
		ref := s.Engine().Catalog.Domain.Ref(stage.Base.Epoch(), address)
		if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
			return stage, err
		}
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := schemaAuthorizationLocked(e, s.Op)
	if err != nil {
		return stage, err
	}
	if auth.Catalog.Domain() != stage.Base.Domain() || auth.Catalog.Epoch() != stage.Base.Epoch() {
		return stage, ids.ErrCatalogReplaced
	}
	if auth.Catalog.State() == stage.Base.State() && auth.Revision == stage.AuthRevision && auth.RoleRecord.Revision == stage.RoleRevision {
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
	s.Op.Notices = s.Op.Notices[:noticeMark]
	current, err := StageSchemaLocked(e, s.Op, stmt, input, s.Output)
	if err != nil {
		return stage, err
	}
	// Conditional multi-target drops cannot acquire a previously absent target
	// or a successor while another target's lock was pending.
	for _, address := range current.DomainTargets {
		if core.DefinitionObjectClass(address.ClassID) && !slices.Contains(stage.DomainTargets, address) {
			e.DiscardCatalogStage(current)
			return stage, ids.ErrCatalogReplaced
		}
	}
	// ALTER's resolved target is retained even for no-op actions. Replacements
	// and removals must all belong to the original admitted mutation set.
	for _, original := range current.DomainBases {
		if alter && core.DefinitionObjectClass(original.Address().ClassID) && !slices.Contains(addresses, original.Address()) {
			e.DiscardCatalogStage(current)
			return stage, ids.ErrCatalogReplaced
		}
	}
	for address, descriptor := range current.Descriptors() {
		if core.DefinitionObjectClass(address.ClassID) && current.Base.Get(address) != nil &&
			descriptor != current.Base.Get(address) && !slices.Contains(addresses, address) {
			e.DiscardCatalogStage(current)
			return stage, ids.ErrCatalogReplaced
		}
	}
	for address := range current.Removals() {
		if core.DefinitionObjectClass(address.ClassID) && !slices.Contains(addresses, address) {
			e.DiscardCatalogStage(current)
			return stage, ids.ErrCatalogReplaced
		}
	}
	e.DiscardCatalogStage(stage)
	return current, nil
}
