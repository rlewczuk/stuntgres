package core

import (
	"cmp"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func DefinitionObjectClass(class ids.OID) bool {
	return class == ids.TransformClassID || class == ids.PublicationClassID || class == ids.LanguageClassID || class == ids.SubscriptionClassID || class == ids.StatisticExtClassID || class == ids.EventTriggerClassID || class == ids.CollationClassID || class == ids.ConversionClassID || class == ids.CastClassID || class == ids.ProcedureClassID || class == ids.TSDictionaryClassID || class == ids.TSConfigurationClassID || class == ids.TSParserClassID
}

func CompareDefinitionAddress(a, b ids.ObjectAddress) int {
	if n := cmp.Compare(a.ClassID, b.ClassID); n != 0 {
		return n
	}
	return cmp.Compare(a.ObjectID, b.ObjectID)
}

// Declaration consumers retain only actual dependencies, never runtime lookup
// choices. Resources use the same ticket/checkpoint/epoch lifetime as names.
func (s *StatementScope) acquireDefinitionObjectDependencies(stage *CatalogStage) error {
	if stage == nil || stage.Owner != s.Engine() || stage.Base.Domain() != s.Engine().Catalog.Domain || stage.Base.Epoch() != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	var scratch [16]ids.ObjectAddress
	addresses := scratch[:0]
	for _, descriptor := range stage.Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.Transform:
			d.Dependencies(func(a ids.ObjectAddress) bool { addresses = append(addresses, a); return true })
		case *catalog.Language:
			if d.Procedural {
				for _, handler := range [...]ids.ObjectAddress{d.Call, d.Inline, d.Validator} {
					if handler != (ids.ObjectAddress{}) {
						addresses = append(addresses, handler)
					}
				}
			}
		case *catalog.Procedure:
			if d.SQL != nil {
				addresses = append(addresses, d.Language)
			}
		case *catalog.EventTrigger:
			addresses = append(addresses, d.Function.Address)
		case *catalog.Cast:
			addresses = append(addresses, catalog.CastDependencies(d)...)
		case *catalog.Statistics:
			addresses = append(addresses, d.Relation.Address)
			d.VisitDependencies(func(a ids.ObjectAddress, _ byte) { a.SubID = 0; addresses = append(addresses, a) })
		case *catalog.PublicationRelation:
			d.VisitDependencies(func(a ids.ObjectAddress, _ byte) { a.SubID = 0; addresses = append(addresses, a) })
		case *catalog.PublicationNamespace:
			d.VisitDependencies(func(a ids.ObjectAddress, _ byte) { a.SubID = 0; addresses = append(addresses, a) })
		case *catalog.Conversion:
			addresses = append(addresses, d.Handler)
		case *catalog.TSParser:
			addresses = append(addresses, d.Dependencies()...)
		case *catalog.TSConfiguration:
			for _, dependency := range d.Dependencies() {
				if dependency.ClassID == ids.TSDictionaryClassID || dependency.ClassID == ids.TSParserClassID {
					addresses = append(addresses, dependency)
				}
			}
		case *catalog.TSDictionary:
			addresses = append(addresses, d.Template)
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
	slices.SortFunc(addresses, CompareDefinitionAddress)
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
			ref := s.Engine().Catalog.Domain.Ref(stage.Base.Epoch(), address)
			if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
				return err
			}
		}
		e := s.Engine()
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(s.Op)
		if err == nil && auth.Catalog.Get(address) != original {
			err = ids.ErrCatalogReplaced
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		if !slices.Contains(stage.DomainBases, original) {
			stage.DomainBases = append(stage.DomainBases, original)
		}
	}
	return nil
}

// Cascaded retirement uses the same resources as direct mutations. The closure
// keeps its captured identities; a wait never adopts a successor descriptor.
func (s *StatementScope) acquireRetiredDefinitionObjects(base catalog.Read, targets []ids.ObjectAddress) error {
	if base.Domain() != s.Engine().Catalog.Domain || base.Epoch() != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	var scratch [8]ids.ObjectAddress
	addresses := scratch[:0]
	for _, address := range targets {
		if DefinitionObjectClass(address.ClassID) {
			addresses = append(addresses, address)
		}
	}
	slices.SortFunc(addresses, CompareDefinitionAddress)
	addresses = slices.Compact(addresses)
	for _, address := range addresses {
		resource := lock.Resource{ObjectRef: s.Engine().Catalog.Domain.Ref(base.Epoch(), address)}
		if s.Engine().Locks.Holds(s.LockTicket, resource, lock.AccessExclusive) {
			continue
		}
		if err := s.AcquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
		e := s.Engine()
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(s.Op)
		if err == nil && auth.Catalog.Get(address) != base.Get(address) {
			err = ids.ErrCatalogReplaced
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
