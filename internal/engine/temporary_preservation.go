package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/extension"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// Replacement transfers only portable objects. SessionToken authority belongs
// to the source physical session and cannot be imported, even by self replacement:
// the destination overlays its exact current live TEMP objects after capture.
// Keep the supplied published view immutable and use this private candidate for
// provider validation and generator/storage capture as well as installation.
func persistentReplacementCatalog(view catalog.Read) (catalog.Read, error) {
	for _, descriptor := range view.Descriptors() {
		if extension, ok := descriptor.(*catalog.Extension); ok {
			namespace, ok := view.Namespace(extension.TargetNamespace)
			if !ok {
				return catalog.Read{}, fmt.Errorf("stuntgres: extension namespace is missing")
			}
			if namespace.Temporary != (ids.SessionToken{}) {
				return catalog.Read{}, fmt.Errorf("stuntgres: temporary extension installation is not portable")
			}
		}
	}
	next := catalog.CloneState(view.State())
	operators := catalog.OperatorIndexEdit{Index: view.State().OperatorNames()}
	procedures := catalog.ProcedureIndexEdit{Index: view.State().ProcedureNames()}
	for a, d := range view.Descriptors() {
		ns, isNamespace := d.(*catalog.Namespace)
		parent, hasParent := view.Namespace(d.Namespace())
		if isNamespace && ns.Temporary != (ids.SessionToken{}) || hasParent && parent.Temporary != (ids.SessionToken{}) {
			operators.Replace(d, nil)
			procedures.Replace(d, nil)
			next.RemoveDescriptor(a)
		}
	}
	for key, a := range view.Names() {
		parent, hasParent := view.Namespace(key.Namespace)
		if view.Get(a) != nil && next.Get(a) == nil || hasParent && parent.Temporary != (ids.SessionToken{}) {
			next.UnbindName(key)
		}
	}
	next.SetOperatorNames(operators.Index)
	next.SetProcedureNames(procedures.Index)
	catalog.PruneNamespaceDefaults(view.State(), next, nil)
	return view.WithState(next), nil
}

// Preserve session-owned temporary namespaces and generations during persistent
// replacement. Imported identity collisions are remapped, never temp identities.
func (e *Engine) preserveTemporaryLocked(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, restoreOp *Operation, indexes *indexSnapshot) error {
	e.retainDatabaseLocked(next)
	if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
		return err
	}
	if err := e.checkTemporaryRuleSourcesReplacementLocked(); err != nil {
		return err
	}
	temporary := make(map[ids.ObjectAddress]catalog.Descriptor)
	for a, d := range e.catalog.state.Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary != (ids.SessionToken{}) {
			temporary[a] = d
		}
	}
	namespaces := make(map[ids.ObjectAddress]bool, len(temporary))
	for address := range temporary {
		namespaces[address] = true
	}
	children, err := namespaceChildren(e.catalog.read(e.catalog.state), namespaces)
	if err != nil {
		return err
	}
	for _, address := range children {
		descriptor := e.catalog.state.Get(address)
		if extension, ok := descriptor.(*catalog.Extension); ok {
			if imported, exists := next.LookupName(catalog.DescriptorNameKey(extension)); exists && next.Get(imported) != extension {
				return fmt.Errorf("stuntgres: catalog replacement conflicts with live temporary extension %q", extension.Name())
			}
		}
		temporary[address] = descriptor
	}
	// Persistent replacement cannot substitute imported domains for a live
	// TEMP array's exact dependency identities. Fail before allocator/state work.
	for _, descriptor := range temporary {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, policy := range relation.Security.Policies {
			for _, condition := range []*policyExpression{policyExpressionOf(policy.Using), policyExpressionOf(policy.Check)} {
				if condition == nil {
					continue
				}
				for _, leaf := range condition.presentation.sources {
					if _, retained := temporary[leaf.ref.Address]; retained {
						continue
					}
					if next.Get(leaf.ref.Address) != e.catalog.state.Get(leaf.ref.Address) {
						return fmt.Errorf("stuntgres: catalog replacement with incompatible live temporary policy dependencies is unsupported")
					}
				}
				for _, dependency := range condition.presentation.dependencies {
					address := dependency.Ref.Address
					address.SubID = 0
					if _, retained := temporary[address]; retained {
						continue
					}
					if next.Get(address) != e.catalog.state.Get(address) {
						return fmt.Errorf("stuntgres: catalog replacement with incompatible live temporary policy dependencies is unsupported")
					}
				}
			}
		}
		for _, column := range relation.Columns {
			typ := column.Usage.Type
			if !typ.BaseType().IsArray() || !arrayDomainDependency(typ) {
				continue
			}
			if !retainedArrayDependencies(e.catalog.state, next, typ) {
				return fmt.Errorf("stuntgres: catalog replacement with incompatible live temporary user-domain array dependencies is unsupported")
			}
		}
	}
	// Retained TEMP columns retain exact builtin generations and their array
	// associations. Imported columns/values are reowned below, never TEMP rows.
	typesChanged := false
	retained := make(map[sqltype.ID]bool)
	var retainBuiltin func(*sqltype.Descriptor)
	retainBuiltin = func(typ *sqltype.Descriptor) {
		if typ == nil || typ.IsDomain() || typ.IsArray() && typ.Element().IsDomain() || retained[typ.ID()] {
			return
		}
		retained[typ.ID()] = true
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
		source, ok := e.catalog.state.Type(address)
		if !ok || source.SQLType != typ {
			return
		}
		if imported, ok := next.Type(address); ok && imported.SQLType != typ {
			copy := *imported
			copy.SQLType = typ
			next.SetDescriptor(address, &copy)
			typesChanged = true
		}
		if typ.IsArray() {
			retainBuiltin(typ.Element())
		}
		if source.Array != 0 {
			if array, ok := e.catalog.state.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.Array)}); ok {
				retainBuiltin(array.SQLType)
			}
		}
	}
	for _, descriptor := range temporary {
		if relation, ok := descriptor.(*catalog.Relation); ok {
			for _, column := range relation.Columns {
				retainBuiltin(column.Usage.Type)
			}
			retainExpression := func(node expr.Expr) {
				walkBoundExpression(node, func(node expr.Expr) { retainBuiltin(node.ResultUsage().Type) })
			}
			for _, definition := range relation.ColumnMeta.Defaults {
				retainExpression(definition.Expression)
			}
			for _, definition := range relation.Constraints.Check {
				retainExpression(definition.Expression)
			}
			for _, policy := range relation.Security.Policies {
				for _, condition := range []*policyExpression{policyExpressionOf(policy.Using), policyExpressionOf(policy.Check)} {
					if condition != nil {
						walkQueryArgumentExpression(condition.expression, func(node expr.Expr) { retainBuiltin(node.ResultUsage().Type) })
					}
				}
			}
			for _, definition := range relation.ColumnMeta.Generations {
				retainExpression(definition.Expression)
				retainExpression(definition.Planned)
			}
			if index := relation.SQLIndex; index != nil {
				for _, key := range index.Keys {
					if key.Derived != nil {
						retainExpression(key.Derived.Definition)
						retainExpression(key.Derived.Execution)
					}
				}
				if predicate := index.Predicate; predicate != nil {
					retainExpression(predicate.Definition)
					retainExpression(predicate.Execution)
				}
			}
			if definition := viewOf(relation); definition != nil {
				var retainQuery func(LogicalNode)
				retainQuery = func(node LogicalNode) {
					if node == nil {
						return
					}
					for _, column := range node.Schema() {
						retainBuiltin(column.Usage.Type)
					}
					queryExpressions(node, func(expression expr.Expr) {
						walkBoundExpression(expression, func(expression expr.Expr) {
							retainBuiltin(expression.ResultUsage().Type)
							if child, ok := expression.(subqueryExpression); ok {
								retainQuery(child.QueryPlan.Root)
							}
						})
					})
					left, right := queryInputs(node)
					retainQuery(left)
					retainQuery(right)
				}
				retainQuery(definition.query.Root)
				for _, literal := range definition.literals {
					retainBuiltin(literal.ResultUsage().Type)
				}
				for _, usage := range definition.castUsages {
					retainBuiltin(usage.Type)
				}
				for ref, captured := range definition.references {
					address := ref.Address
					address.SubID = 0
					switch provider := e.catalog.state.Get(address).(type) {
					case *catalog.Type:
						retainBuiltin(provider.SQLType)
					case *catalog.Relation:
						if ref.Address.SubID != 0 || provider != captured || provider.RelationKind != catalog.CatalogRelation || !pgDependPinned(e.catalog.read(e.catalog.state), nil, address) {
							continue
						}
						// The exact immutable catalog provider includes unused columns.
						// Retain their generations too, so imported scalar retagging
						// cannot clone the certified provider on a later replacement.
						for _, column := range provider.Columns {
							retainBuiltin(column.Usage.Type)
						}
					}
				}
			}
		}
	}
	if err := e.retainTemporaryViewProvidersLocked(next, temporary); err != nil {
		return err
	}
	// OIDs share one identity space even when their catalog classes differ.
	// Retained TEMP definitions cannot be reminted to make room for imports.
	retainedIDs := make(map[ids.OID]bool)
	for address, descriptor := range temporary {
		retainedIDs[address.ObjectID] = true
		reserveDefinitionOIDs(retainedIDs, descriptor)
	}
	reservedIDs := make(map[ids.OID]bool, next.DescriptorCount()+len(retainedIDs))
	for id := range retainedIDs {
		reservedIDs[id] = true
	}
	for address, descriptor := range next.Descriptors() {
		reservedIDs[address.ObjectID] = true
		reserveDefinitionOIDs(reservedIDs, descriptor)
	}
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	remapCollision := func(address ids.ObjectAddress) error {
		if !retainedIDs[address.ObjectID] {
			return nil
		}
		for {
			oid, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			if reservedIDs[oid] {
				continue
			}
			destination := address
			destination.ObjectID = oid
			mapping[address] = destination
			reservedIDs[oid] = true
			return nil
		}
	}
	for address, descriptor := range next.Descriptors() {
		if err := remapCollision(address); err != nil {
			return err
		}
		if proc, ok := descriptor.(*catalog.Procedure); ok && proc.Aggregate != nil {
			// These are mapping-only identities, not callable descriptors.
			// Publish their mapping before scalar/default/expression remapping.
			for _, helper := range proc.Aggregate.Helpers {
				if err := remapCollision(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}); err != nil {
					return err
				}
			}
		}
	}
	if err := e.importScalarTypesLocked(next, mapping); err != nil {
		return err
	}
	defer func() { next.SetScalarImports(nil) }()
	domainsChanged, err := typedef.ReownDomainTypes(next, mapping, true)
	if err != nil {
		return err
	}
	refreshScalarImportDestinations(next)
	indexes.captureMapping(mapping, next.ScalarImports())
	if typesChanged || domainsChanged || len(mapping) != 0 || len(next.ScalarImports()) != 0 || next.ACLItemImport() != nil {
		if err := reownCatalogScalarTypes(next, relations, mapping); err != nil {
			return err
		}
	}
	if len(mapping) != 0 {
		catalog.RemapDefaultSchemas(next, mapping)
		originals := next.ResetMaps(next.DescriptorCount() + len(temporary))
		for a, d := range originals {
			target := a
			if dest, ok := mapping[a]; ok {
				target = dest
				priority := a.ObjectID
				if captured := next.DumpPriority(a); captured != 0 {
					priority = captured
					next.RemoveDumpPriority(a)
				}
				next.SetDumpPriority(target, priority)
			}
			switch r := d.(type) {
			case *catalog.Namespace:
				if target == a && d == e.catalog.state.Get(a) && pgDependPinned(e.catalog.read(e.catalog.state), nil, a) {
					// Preflight retained this exact immutable namespace authority.
					break
				}
				n := *r
				n.ObjectAddress = target
				d = &n
			case *catalog.Collation:
				n := *r
				n.OID = target.ObjectID
				if dest, ok := mapping[n.Namespace()]; ok {
					n.ObjectNamespace = dest
				}
				d = &n
			case *catalog.Conversion:
				namespace, handler := r.Namespace(), r.Handler
				if dest, ok := mapping[namespace]; ok {
					namespace = dest
				}
				if dest, ok := mapping[handler]; ok {
					handler = dest
				}
				if target == a && namespace == r.Namespace() && handler == r.Handler {
					break
				}
				if r.Seeded() && target != a {
					return fmt.Errorf("stuntgres: pinned conversion identity collision")
				}
				n := *r
				n.OID, n.ObjectNamespace, n.Handler = target.ObjectID, namespace, handler
				d = &n
			case *catalog.Operator:
				n := *r
				n.ObjectAddress = target
				move := func(class, id ids.OID) ids.OID {
					if mapped, ok := mapping[ids.ObjectAddress{ClassID: class, ObjectID: id}]; ok {
						return mapped.ObjectID
					}
					return id
				}
				if mapped, ok := mapping[n.Namespace()]; ok {
					n.ObjectNamespace = mapped
				}
				n.Left, n.Right, n.Result = move(ids.TypeClassID, n.Left), move(ids.TypeClassID, n.Right), move(ids.TypeClassID, n.Result)
				n.Function, n.Restriction, n.Join = move(ids.ProcedureClassID, n.Function), move(ids.ProcedureClassID, n.Restriction), move(ids.ProcedureClassID, n.Join)
				n.Commutator, n.Negator = move(ids.OperatorClassID, n.Commutator), move(ids.OperatorClassID, n.Negator)
				if n != *r {
					d = &n
				}
			case *catalog.Relation:
				if target == a && r.RelationKind == catalog.CatalogRelation && d == e.catalog.state.Get(a) && pgDependPinned(e.catalog.read(e.catalog.state), nil, a) {
					if _, moved := mapping[r.Namespace()]; !moved {
						// Remapping unrelated imported identities must not copy
						// the exact provider certified for retained TEMP consumers.
						break
					}
				}
				n := *r
				n.ObjectAddress = target
				if r.Inheritance != nil {
					n.Inheritance = remapInheritance(r.Inheritance, func(address ids.ObjectAddress) ids.ObjectAddress {
						if destination, ok := mapping[address]; ok {
							return destination
						}
						return address
					}, func(origin catalog.InheritanceOrigin) ids.OID { return origin.Constraint })
				}
				if target != a {
					n.Columns = append([]catalog.Column(nil), r.Columns...)
					for i := range n.Columns {
						if n.Columns[i].OriginTable == uint32(a.ObjectID) {
							n.Columns[i].OriginTable = uint32(target.ObjectID)
						}
					}
				}
				if dest, ok := mapping[n.Namespace()]; ok {
					n.ObjectNamespace = dest
				}
				if len(r.Constraints.Unique) != 0 {
					n.Constraints.Unique = append([]catalog.UniqueConstraint(nil), r.Constraints.Unique...)
					for i := range n.Constraints.Unique {
						if dest, ok := mapping[n.Constraints.Unique[i].Index]; ok {
							n.Constraints.Unique[i].Index = dest
						}
					}
				}
				if err := remapPhysicalNotNullReferences(r, &n, mapping); err != nil {
					return err
				}
				if len(r.Constraints.Exclusion) != 0 {
					n.Constraints.Exclusion = append([]catalog.ExclusionConstraint(nil), r.Constraints.Exclusion...)
					for i := range n.Constraints.Exclusion {
						if dest, ok := mapping[n.Constraints.Exclusion[i].Index]; ok {
							n.Constraints.Exclusion[i].Index = dest
						}
					}
				}
				if len(r.Constraints.ForeignKeys) != 0 {
					n.Constraints.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), r.Constraints.ForeignKeys...)
					for i := range n.Constraints.ForeignKeys {
						if dest, ok := mapping[n.Constraints.ForeignKeys[i].Target]; ok {
							n.Constraints.ForeignKeys[i].Target = dest
						}
						if dest, ok := mapping[n.Constraints.ForeignKeys[i].Index]; ok {
							n.Constraints.ForeignKeys[i].Index = dest
						}
					}
				}
				if r.SQLIndex != nil {
					definition := *r.SQLIndex
					if parent, remapped := mapping[definition.PartitionParent]; remapped {
						definition.PartitionParent = parent
					}
					if parent, remapped := mapping[definition.Parent]; remapped {
						definition.Parent = parent
					}
					if definition.Constraint != nil {
						constraint := *definition.Constraint
						if dest, remapped := mapping[constraint.Index]; remapped {
							constraint.Index = dest
						}
						definition.Constraint = &constraint
					}
					n.SQLIndex = &definition
				}
				if r.Sequence != nil {
					o := *r.Sequence
					whole := o.Owned
					whole.SubID = 0
					if dest, ok := mapping[whole]; ok {
						dest.SubID = o.Owned.SubID
						o.Owned = dest
					}
					n.Sequence = &o
				}
				if len(r.ColumnMeta.Identities) != 0 {
					n.ColumnMeta.Identities = make(map[int32]catalog.ColumnIdentity, len(r.ColumnMeta.Identities))
					for attribute, identity := range r.ColumnMeta.Identities {
						if destination, ok := mapping[identity.Sequence]; ok {
							identity.Sequence = destination
						}
						n.ColumnMeta.Identities[attribute] = identity
					}
				}
				d = &n
			}
			d = extension.RemapDescriptor(d, mapping)
			next.SetDescriptor(target, d)
			next.BindName(catalog.DescriptorNameKey(d), target)
		}
		next.SetOperatorNames(catalog.RebuildOperatorNames(next.Descriptors()))
		next.SetProcedureNames(catalog.RebuildProcedureNames(next.Descriptors()))
		// Copy into fresh maps before deleting old keys to avoid remap collisions.
		oldStorage := make(map[ids.ObjectAddress]*storage.Relation, len(relations))
		for a, s := range relations {
			oldStorage[a] = s
			delete(relations, a)
		}
		for a, s := range oldStorage {
			if dest, ok := mapping[a]; ok {
				a = dest
			}
			relations[a] = s
		}
		oldSequences := make(map[ids.ObjectAddress]*storage.SequenceGenerator, len(sequences))
		for a, g := range sequences {
			oldSequences[a] = g
			delete(sequences, a)
		}
		for a, g := range oldSequences {
			if dest, ok := mapping[a]; ok {
				a = dest
			}
			sequences[a] = g
		}
	}
	next.SetEpoch(e.catalog.state.Epoch() + 1)
	if err := rebindSQLRoutineDefinitions(e.catalog.read(next), next, mapping); err != nil {
		return err
	}
	if err := e.remapCatalogExpressions(next, mapping); err != nil {
		return err
	}
	if err := rebindImportedExclusions(next); err != nil {
		return err
	}
	installedStage, err := e.repairRemappedGenerationsLocked(ctx, next, relations, sequences, mapping, restoreOp, indexes)
	if err != nil {
		return err
	}
	operators := catalog.OperatorIndexEdit{Index: next.OperatorNames()}
	procedures := catalog.ProcedureIndexEdit{Index: next.ProcedureNames()}
	for a, d := range temporary {
		operators.Replace(next.Get(a), d)
		procedures.Replace(next.Get(a), d)
		next.SetDescriptor(a, d)
		next.BindName(catalog.DescriptorNameKey(d), a)
		// Repair may have published these candidate containers into its engine.
		// The target mutex is already held. A distinct stage is isolated and
		// has finished provider evaluation; no callback runs under both locks.
		if installedStage != nil && installedStage != e {
			installedStage.mu.Lock()
		}
		if rows := e.storage.Relations[a]; rows != nil {
			if installedStage == nil {
				relations[a] = rows
			} else {
				installedStage.storage.InstallRelationLocked(a, rows)
			}
		}
		if g := e.storage.Sequences[a]; g != nil {
			if installedStage == nil {
				sequences[a] = g
			} else {
				installedStage.storage.InstallSequenceLocked(a, g)
			}
		}
		if installedStage != nil && installedStage != e {
			installedStage.mu.Unlock()
		}
	}
	next.SetOperatorNames(operators.Index)
	next.SetProcedureNames(procedures.Index)
	for address, descriptor := range temporary {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || len(relation.Security.Policies) == 0 && len(rulesOf(relation)) == 0 {
			continue
		}
		view := e.catalog.read(next)
		remap := func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch = next.Epoch(); return ref }
		typ := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
		// Replacement preflight certified these exact TEMP providers. They
		// enter the candidate after imported expressions have been remapped,
		// so advance their frozen definition refs without applying import IDs.
		if len(relation.Security.Policies) != 0 {
			replacement, err := remapPolicies(view, relation, nil, remap, typ, nil)
			if err != nil {
				return err
			}
			relation = replacement
		}
		if len(rulesOf(relation)) != 0 {
			replacement, err := remapRules(view, relation, nil, remap, typ, nil)
			if err != nil {
				return err
			}
			relation = replacement
		}
		next.SetDescriptor(address, relation)
	}
	previous := make(map[ids.ObjectAddress]catalog.Descriptor, next.DescriptorCount())
	for address, descriptor := range next.Descriptors() {
		previous[address] = descriptor
	}
	if err := e.remapViewRuleOIDs(next); err != nil {
		return err
	}
	if err := remapInheritanceConstraintOrigins(next, previous); err != nil {
		return err
	}
	if err := e.remapRetiredConstraints(next, previous); err != nil {
		return err
	}
	if err := e.remapPhysicalNotNull(next, previous); err != nil {
		return err
	}
	for _, descriptor := range next.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && len(relation.Security.Policies) != 0 {
			if err := validatePolicies(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			if err := typedef.ValidateTypedAttachment(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Inheritance != nil {
			if err := validateInheritance(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.View != nil {
			if err := validateViewDefinition(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
	}
	return nil
}

func arrayDomainDependency(typ *sqltype.Descriptor) bool {
	if typ.IsDomain() {
		return true
	}
	return typ.IsArray() && arrayDomainDependency(typ.Element())
}
func retainedArrayDependencies(source, target *catalog.State, typ *sqltype.Descriptor) bool {
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
	old, ok := source.Type(address)
	if !ok {
		return false
	}
	next, ok := target.Type(address)
	if !ok || old.SQLType != typ || next.SQLType != typ || old.Array != next.Array || old.Namespace() != next.Namespace() || old.Owner != next.Owner || old.Revision() != next.Revision() {
		return false
	}
	if typ.IsDomain() {
		return retainedArrayDependencies(source, target, typ.DomainBase().Type)
	}
	if typ.IsArray() {
		return retainedArrayDependencies(source, target, typ.Element())
	}
	return true
}

// Only immutable bootstrap catalog sources have a persistent survivor route.
// Retain the predecessor itself, after checking the candidate's full provider
// shape and namespace/ACL authority; a compatible-looking user table is not it.
func (e *Engine) retainTemporaryViewProvidersLocked(next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor) error {
	view := e.catalog.read(e.catalog.state)
	for _, descriptor := range temporary {
		consumer, ok := descriptor.(*catalog.Relation)
		if !ok || consumer.View == nil {
			continue
		}
		for ref, captured := range viewOf(consumer).references {
			address := ref.Address
			address.SubID = 0
			old, ok := e.catalog.state.Relation(address)
			if !ok {
				continue
			}
			if temporary[old.Namespace()] != nil {
				if !storedViewReferenceValid(view, consumer, ref) {
					return ids.ErrCatalogReplaced
				}
				continue
			}
			fail := func() error {
				return fmt.Errorf("stuntgres: catalog replacement with live temporary view persistent dependencies is unsupported")
			}
			if old != captured || old.RelationKind != catalog.CatalogRelation || !pgDependPinned(view, nil, address) {
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
			namespace, ok := e.catalog.state.Namespace(old.Namespace())
			other, candidateOK := next.Namespace(old.Namespace())
			if !ok || !candidateOK || !pgDependPinned(view, nil, old.Namespace()) || namespace.Name() != other.Name() || namespace.Owner != other.Owner || namespace.Temporary != other.Temporary || namespace.TemporaryToast != other.TemporaryToast || namespace.ACL.Explicit != other.ACL.Explicit || !slices.Equal(namespace.ACL.Entries, other.ACL.Entries) {
				return fail()
			}
			next.SetDescriptor(address, old)
			next.SetDescriptor(old.Namespace(), namespace)
		}
	}
	return nil
}
