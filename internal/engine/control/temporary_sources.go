package control

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// TEMP replacement checks over the published catalog. The caller holds
// Engine.mu, so current is the catalog a replacement would replace.

// TemporaryInheritanceReplacementError rejects a replacement that would keep a
// live temporary child of a persistent parent.
const TemporaryInheritanceReplacementError = "stuntgres: catalog replacement with live temporary table inheritance of persistent parents is unsupported"

// CheckTemporaryRelationSourcesLocked rejects a replacement while a retained
// TEMP relation inherits from a persistent parent: the replacement cannot
// substitute that source. The root runs it at preallocation and at final
// replacement admission, together with the typed-table check.
func CheckTemporaryRelationSourcesLocked(current *catalog.State) error {
	for _, descriptor := range current.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.Inheritance == nil {
			continue
		}
		namespace := current.MustNamespace(table.Namespace())
		if namespace.Temporary == (ids.SessionToken{}) {
			continue
		}
		for _, edge := range table.Inheritance.Parents {
			parent := edge.Relation
			source, ok := current.Relation(parent)
			if !ok {
				return fmt.Errorf("stuntgres: missing inheritance parent")
			}
			if current.MustNamespace(source.Namespace()).Temporary == (ids.SessionToken{}) {
				return fmt.Errorf("%s", TemporaryInheritanceReplacementError)
			}
		}
	}
	return nil
}

// CheckTemporaryRuleSourcesLocked rejects a replacement while a rule of a
// retained TEMP relation depends on an object outside its own session's
// temporary namespace, other than a pinned builtin.
func CheckTemporaryRuleSourcesLocked(current catalog.Read) error {
	for _, descriptor := range current.State().Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(stored.RulesOf(d)) == 0 && !d.HasTriggers() {
			continue
		}
		ns, ok := current.State().Namespace(d.Namespace())
		if !ok || ns.Temporary == (ids.SessionToken{}) {
			continue
		}
		for _, rule := range stored.RulesOf(d) {
			for _, address := range rule.References() {
				address.SubID = 0
				if stored.DependPinned(current, nil, address) {
					continue
				}
				provider := current.State().Get(address)
				if address.ClassID == 2606 {
					parent := stored.ConstraintParent(current, address)
					if parent == nil {
						return ids.ErrCatalogReplaced
					}
					provider = parent
				}
				if provider == nil {
					return ids.ErrCatalogReplaced
				}
				providerNS, ok := current.State().Namespace(provider.Namespace())
				if !ok || providerNS.Temporary != ns.Temporary {
					return fmt.Errorf("stuntgres: catalog replacement cannot preserve temporary rule %q dependencies", rule.Name)
				}
			}
		}
		for _, trigger := range stored.TriggersOf(d) {
			for _, address := range trigger.Parts().References {
				address.SubID = 0
				if stored.DependPinned(current, nil, address) {
					continue
				}
				provider := current.State().Get(address)
				if provider == nil {
					return ids.ErrCatalogReplaced
				}
				providerNS, ok := current.State().Namespace(provider.Namespace())
				if !ok || providerNS.Temporary != ns.Temporary {
					return fmt.Errorf("stuntgres: catalog replacement cannot preserve temporary trigger %q dependencies", trigger.Name())
				}
			}
		}
	}
	return nil
}

// RetainTemporaryViewProvidersLocked keeps in next the persistent sources of
// retained TEMP views. Only immutable catalog providers have a persistent
// survivor route, independently of dependency pinning. Retain the predecessor
// itself, after checking the candidate's full provider shape and namespace/ACL authority; a
// compatible-looking user table is not it.
func RetainTemporaryViewProvidersLocked(current catalog.Read, next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor) error {
	for _, descriptor := range temporary {
		consumer, ok := descriptor.(*catalog.Relation)
		if !ok || consumer.View == nil {
			continue
		}
		for ref, captured := range stored.ViewOf(consumer).References() {
			address := ref.Address
			address.SubID = 0
			old, ok := current.State().Relation(address)
			if !ok {
				continue
			}
			if temporary[old.Namespace()] != nil {
				if !stored.ViewReferenceValid(current, consumer, ref) {
					return ids.ErrCatalogReplaced
				}
				continue
			}
			fail := func() error {
				return fmt.Errorf("stuntgres: catalog replacement with live temporary view persistent dependencies is unsupported")
			}
			if old != captured || old.RelationKind != catalog.CatalogRelation {
				return fail()
			}
			candidate, ok := next.Relation(address)
			if !ok || candidate.RelationKind != old.RelationKind || candidate.Namespace() != old.Namespace() || candidate.Name() != old.Name() || candidate.Owner != old.Owner || candidate.VirtualView != old.VirtualView || candidate.History.HasIndexes != old.History.HasIndexes || candidate.History.HasSubclasses != old.History.HasSubclasses || candidate.ColumnMeta.HighWater != old.ColumnMeta.HighWater || !slices.Equal(candidate.Attributes, old.Attributes) || len(candidate.Columns) != len(old.Columns) || candidate.Security.ACL.Explicit != old.Security.ACL.Explicit || !slices.Equal(candidate.Security.ACL.Entries, old.Security.ACL.Entries) || len(candidate.Security.ColumnACL) != len(old.Security.ColumnACL) {
				return fail()
			}
			for attribute, acl := range old.Security.ColumnACL {
				other, ok := candidate.Security.ColumnACL[attribute]
				if !ok || other.Explicit != acl.Explicit || !slices.Equal(other.Entries, acl.Entries) {
					return fail()
				}
			}
			for i, column := range old.Columns {
				other := candidate.Columns[i]
				if column.Name != other.Name || column.Usage.Type.ID() != other.Usage.Type.ID() || column.Usage.Typmod != other.Usage.Typmod || column.Usage.Collation.ID != other.Usage.Collation.ID || column.Nullable != other.Nullable || column.NullabilityKnown != other.NullabilityKnown {
					return fail()
				}
			}
			namespace, ok := current.State().Namespace(old.Namespace())
			other, candidateOK := next.Namespace(old.Namespace())
			if !ok || !candidateOK || !stored.DependPinned(current, nil, old.Namespace()) || namespace.Name() != other.Name() || namespace.Owner != other.Owner || namespace.Temporary != other.Temporary || namespace.TemporaryToast != other.TemporaryToast || namespace.ACL.Explicit != other.ACL.Explicit || !slices.Equal(namespace.ACL.Entries, other.ACL.Entries) {
				return fail()
			}
			next.SetDescriptor(address, old)
			next.SetDescriptor(old.Namespace(), namespace)
		}
	}
	return nil
}
