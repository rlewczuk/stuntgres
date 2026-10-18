package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement transfers only portable objects. SessionToken authority belongs
// to the source physical session and cannot be imported, even by self replacement:
// the destination overlays its exact current live TEMP objects after capture.
// Keep the supplied published view immutable and use this private candidate for
// provider validation and generator/storage capture as well as installation.
func persistentReplacementCatalog(view CatalogRead) (CatalogRead, error) {
	for _, descriptor := range view.Descriptors() {
		if extension, ok := descriptor.(*extensionDescriptor); ok {
			namespace, ok := view.Namespace(extension.targetNamespace)
			if !ok {
				return CatalogRead{}, fmt.Errorf("stuntgres: extension namespace is missing")
			}
			if namespace.temporary != (ids.SessionToken{}) {
				return CatalogRead{}, fmt.Errorf("stuntgres: temporary extension installation is not portable")
			}
		}
	}
	next := cloneCatalog(view.state)
	operators := operatorIndexEdit{index: view.state.operatorNames}
	for a, d := range view.Descriptors() {
		ns, isNamespace := d.(*namespaceDescriptor)
		parent, hasParent := view.Namespace(d.Namespace())
		if isNamespace && ns.temporary != (ids.SessionToken{}) || hasParent && parent.temporary != (ids.SessionToken{}) {
			operators.replace(d, nil)
			next.removeDescriptor(a)
		}
	}
	for key, a := range view.Names() {
		parent, hasParent := view.Namespace(key.Namespace)
		if view.Get(a) != nil && next.Get(a) == nil || hasParent && parent.temporary != (ids.SessionToken{}) {
			next.unbindName(key)
		}
	}
	next.operatorNames = operators.index
	pruneNamespaceDefaults(view.state, next, nil)
	return CatalogRead{owner: view.owner, state: next}, nil
}

// Called under source synchronization; generator scalars are detached from the
// source, unlike immutable relation descriptors.
func copySequenceState(source *Engine, view CatalogRead) (map[ids.ObjectAddress]*sequenceGenerator, error) {
	result := make(map[ids.ObjectAddress]*sequenceGenerator)
	for a, d := range view.Descriptors() {
		r, ok := d.(*relationDescriptor)
		if !ok || r.relationKind != sequenceRelation {
			continue
		}
		g := source.sequences[a]
		if g == nil {
			return nil, fmt.Errorf("stuntgres: sequence state missing")
		}
		copy := *g
		result[a] = &copy
	}
	return result, nil
}

// Preserve session-owned temporary namespaces and generations during persistent
// replacement. Imported identity collisions are remapped, never temp identities.
func (e *Engine) preserveTemporaryLocked(ctx context.Context, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, sequences map[ids.ObjectAddress]*sequenceGenerator, restoreOp *Operation, indexes *indexSnapshot) error {
	e.retainDatabaseLocked(next)
	if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
		return err
	}
	if err := e.checkTemporaryRuleSourcesReplacementLocked(); err != nil {
		return err
	}
	temporary := make(map[ids.ObjectAddress]Descriptor)
	for a, d := range e.catalog.state.Descriptors() {
		if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary != (ids.SessionToken{}) {
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
		if extension, ok := descriptor.(*extensionDescriptor); ok {
			if imported, exists := next.LookupName(descriptorNameKey(extension)); exists && next.Get(imported) != extension {
				return fmt.Errorf("stuntgres: catalog replacement conflicts with live temporary extension %q", extension.name)
			}
		}
		temporary[address] = descriptor
	}
	// Persistent replacement cannot substitute imported domains for a live
	// TEMP array's exact dependency identities. Fail before allocator/state work.
	for _, descriptor := range temporary {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, policy := range relation.policies {
			for _, condition := range []*policyExpression{policyExpressionOf(policy.using), policyExpressionOf(policy.check)} {
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
					address := dependency.ref.Address
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
		for _, column := range relation.columns {
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
		if !ok || source.typ != typ {
			return
		}
		if imported, ok := next.Type(address); ok && imported.typ != typ {
			copy := *imported
			copy.typ = typ
			next.setDescriptor(address, &copy)
			typesChanged = true
		}
		if typ.IsArray() {
			retainBuiltin(typ.Element())
		}
		if source.array != 0 {
			if array, ok := e.catalog.state.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.array)}); ok {
				retainBuiltin(array.typ)
			}
		}
	}
	for _, descriptor := range temporary {
		if relation, ok := descriptor.(*relationDescriptor); ok {
			for _, column := range relation.columns {
				retainBuiltin(column.Usage.Type)
			}
			retainExpression := func(node expr.Expr) {
				walkBoundExpression(node, func(node expr.Expr) { retainBuiltin(node.ResultUsage().Type) })
			}
			for _, definition := range relation.defaults {
				retainExpression(definition.expression)
			}
			for _, definition := range relation.checkConstraints {
				retainExpression(definition.expression)
			}
			for _, policy := range relation.policies {
				for _, condition := range []*policyExpression{policyExpressionOf(policy.using), policyExpressionOf(policy.check)} {
					if condition != nil {
						walkQueryArgumentExpression(condition.expression, func(node expr.Expr) { retainBuiltin(node.ResultUsage().Type) })
					}
				}
			}
			for _, definition := range relation.generations {
				retainExpression(definition.expression)
				retainExpression(definition.planned)
			}
			if index := relation.sqlIndex; index != nil {
				for _, key := range index.keys {
					if key.derived != nil {
						retainExpression(key.derived.definition)
						retainExpression(key.derived.execution)
					}
				}
				if predicate := index.predicate; predicate != nil {
					retainExpression(predicate.definition)
					retainExpression(predicate.execution)
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
								retainQuery(child.queryPlan.Root)
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
					case *typeDescriptor:
						retainBuiltin(provider.typ)
					case *relationDescriptor:
						if ref.Address.SubID != 0 || provider != captured || provider.relationKind != catalogRelation || !pgDependPinned(e.catalog.read(e.catalog.state), address) {
							continue
						}
						// The exact immutable catalog provider includes unused columns.
						// Retain their generations too, so imported scalar retagging
						// cannot clone the certified provider on a later replacement.
						for _, column := range provider.columns {
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
		if proc, ok := descriptor.(*procedureDescriptor); ok && proc.aggregate != nil {
			// These are mapping-only identities, not callable descriptors.
			// Publish their mapping before scalar/default/expression remapping.
			for _, helper := range proc.aggregate.helpers {
				if err := remapCollision(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.id}); err != nil {
					return err
				}
			}
		}
	}
	if err := e.importScalarTypesLocked(next, mapping); err != nil {
		return err
	}
	defer func() { next.scalarImports = nil }()
	domainsChanged, err := reownDomainTypes(next, mapping, true)
	if err != nil {
		return err
	}
	refreshScalarImportDestinations(next)
	indexes.captureMapping(mapping, next.scalarImports)
	if typesChanged || domainsChanged || len(mapping) != 0 || len(next.scalarImports) != 0 || next.aclItemImport != nil {
		if err := reownCatalogScalarTypes(next, storage, mapping); err != nil {
			return err
		}
	}
	if len(mapping) != 0 {
		remapDefaultSchemas(next, mapping)
		originals := next.resetMaps(next.DescriptorCount() + len(temporary))
		for a, d := range originals {
			target := a
			if dest, ok := mapping[a]; ok {
				target = dest
				if next.dumpPriorities == nil {
					next.dumpPriorities = make(map[ids.ObjectAddress]ids.OID)
				}
				priority := a.ObjectID
				if captured := next.dumpPriorities[a]; captured != 0 {
					priority = captured
					delete(next.dumpPriorities, a)
				}
				next.dumpPriorities[target] = priority
			}
			switch r := d.(type) {
			case *namespaceDescriptor:
				if target == a && d == e.catalog.state.Get(a) && pgDependPinned(e.catalog.read(e.catalog.state), a) {
					// Preflight retained this exact immutable namespace authority.
					break
				}
				n := *r
				n.address = target
				d = &n
			case *collationDescriptor:
				n := *r
				n.oid = target.ObjectID
				if dest, ok := mapping[n.Namespace()]; ok {
					n.namespace = dest
				}
				d = &n
			case *conversionDescriptor:
				namespace, handler := r.Namespace(), r.handler
				if dest, ok := mapping[namespace]; ok {
					namespace = dest
				}
				if dest, ok := mapping[handler]; ok {
					handler = dest
				}
				if target == a && namespace == r.Namespace() && handler == r.handler {
					break
				}
				if r.seeded() && target != a {
					return fmt.Errorf("stuntgres: pinned conversion identity collision")
				}
				n := *r
				n.oid, n.namespace, n.handler = target.ObjectID, namespace, handler
				d = &n
			case *operatorDescriptor:
				n := *r
				n.address = target
				move := func(class, id ids.OID) ids.OID {
					if mapped, ok := mapping[ids.ObjectAddress{ClassID: class, ObjectID: id}]; ok {
						return mapped.ObjectID
					}
					return id
				}
				if mapped, ok := mapping[n.namespace]; ok {
					n.namespace = mapped
				}
				n.left, n.right, n.result = move(ids.TypeClassID, n.left), move(ids.TypeClassID, n.right), move(ids.TypeClassID, n.result)
				n.function, n.restriction, n.join = move(ids.ProcedureClassID, n.function), move(ids.ProcedureClassID, n.restriction), move(ids.ProcedureClassID, n.join)
				n.commutator, n.negator = move(ids.OperatorClassID, n.commutator), move(ids.OperatorClassID, n.negator)
				if n != *r {
					d = &n
				}
			case *relationDescriptor:
				if target == a && r.relationKind == catalogRelation && d == e.catalog.state.Get(a) && pgDependPinned(e.catalog.read(e.catalog.state), a) {
					if _, moved := mapping[r.namespace]; !moved {
						// Remapping unrelated imported identities must not copy
						// the exact provider certified for retained TEMP consumers.
						break
					}
				}
				n := *r
				n.address = target
				if r.inheritance != nil {
					n.inheritance = remapInheritance(r.inheritance, func(address ids.ObjectAddress) ids.ObjectAddress {
						if destination, ok := mapping[address]; ok {
							return destination
						}
						return address
					}, func(origin inheritanceOrigin) ids.OID { return origin.constraint })
				}
				if target != a {
					n.columns = append([]ColumnDescriptor(nil), r.columns...)
					for i := range n.columns {
						if n.columns[i].OriginTable == uint32(a.ObjectID) {
							n.columns[i].OriginTable = uint32(target.ObjectID)
						}
					}
				}
				if dest, ok := mapping[n.namespace]; ok {
					n.namespace = dest
				}
				if len(r.uniqueConstraints) != 0 {
					n.uniqueConstraints = append([]uniqueConstraint(nil), r.uniqueConstraints...)
					for i := range n.uniqueConstraints {
						if dest, ok := mapping[n.uniqueConstraints[i].Index]; ok {
							n.uniqueConstraints[i].Index = dest
						}
					}
				}
				if err := remapPhysicalNotNullReferences(r, &n, mapping); err != nil {
					return err
				}
				if len(r.exclusionConstraints) != 0 {
					n.exclusionConstraints = append([]exclusionConstraint(nil), r.exclusionConstraints...)
					for i := range n.exclusionConstraints {
						if dest, ok := mapping[n.exclusionConstraints[i].Index]; ok {
							n.exclusionConstraints[i].Index = dest
						}
					}
				}
				if len(r.foreignKeys) != 0 {
					n.foreignKeys = append([]foreignKeyConstraint(nil), r.foreignKeys...)
					for i := range n.foreignKeys {
						if dest, ok := mapping[n.foreignKeys[i].Target]; ok {
							n.foreignKeys[i].Target = dest
						}
						if dest, ok := mapping[n.foreignKeys[i].Index]; ok {
							n.foreignKeys[i].Index = dest
						}
					}
				}
				if r.sqlIndex != nil {
					definition := *r.sqlIndex
					if parent, remapped := mapping[definition.partitionParent]; remapped {
						definition.partitionParent = parent
					}
					if parent, remapped := mapping[definition.parent]; remapped {
						definition.parent = parent
					}
					if definition.constraint != nil {
						constraint := *definition.constraint
						if dest, remapped := mapping[constraint.Index]; remapped {
							constraint.Index = dest
						}
						definition.constraint = &constraint
					}
					n.sqlIndex = &definition
				}
				if r.sequence != nil {
					o := *r.sequence
					whole := o.owned
					whole.SubID = 0
					if dest, ok := mapping[whole]; ok {
						dest.SubID = o.owned.SubID
						o.owned = dest
					}
					n.sequence = &o
				}
				if len(r.identities) != 0 {
					n.identities = make(map[int32]columnIdentity, len(r.identities))
					for attribute, identity := range r.identities {
						if destination, ok := mapping[identity.sequence]; ok {
							identity.sequence = destination
						}
						n.identities[attribute] = identity
					}
				}
				d = &n
			}
			d = remapExtensionDescriptor(d, mapping)
			next.setDescriptor(target, d)
			next.bindName(descriptorNameKey(d), target)
		}
		next.operatorNames = rebuildOperatorNames(next.Descriptors())
		// Copy into fresh maps before deleting old keys to avoid remap collisions.
		oldStorage := make(map[ids.ObjectAddress]*relationStorage, len(storage))
		for a, s := range storage {
			oldStorage[a] = s
			delete(storage, a)
		}
		for a, s := range oldStorage {
			if dest, ok := mapping[a]; ok {
				a = dest
			}
			storage[a] = s
		}
		oldSequences := make(map[ids.ObjectAddress]*sequenceGenerator, len(sequences))
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
	next.epoch = e.catalog.state.epoch + 1
	if err := e.remapCatalogExpressions(next, mapping); err != nil {
		return err
	}
	if err := rebindImportedExclusions(next); err != nil {
		return err
	}
	if err := e.repairRemappedGenerationsLocked(ctx, next, storage, sequences, mapping, restoreOp, indexes); err != nil {
		return err
	}
	operators := operatorIndexEdit{index: next.operatorNames}
	for a, d := range temporary {
		operators.replace(next.Get(a), d)
		next.setDescriptor(a, d)
		next.bindName(descriptorNameKey(d), a)
		if rows := e.storage[a]; rows != nil {
			storage[a] = rows
		}
		if g := e.sequences[a]; g != nil {
			sequences[a] = g
		}
	}
	next.operatorNames = operators.index
	for address, descriptor := range temporary {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok || len(relation.policies) == 0 && len(rulesOf(relation)) == 0 {
			continue
		}
		view := e.catalog.read(next)
		remap := func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch = next.epoch; return ref }
		typ := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
		// Replacement preflight certified these exact TEMP providers. They
		// enter the candidate after imported expressions have been remapped,
		// so advance their frozen definition refs without applying import IDs.
		if len(relation.policies) != 0 {
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
		next.setDescriptor(address, relation)
	}
	previous := make(map[ids.ObjectAddress]Descriptor, next.DescriptorCount())
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
		if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.policies) != 0 {
			if err := validatePolicies(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*relationDescriptor); ok {
			if err := validateTypedAttachment(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*relationDescriptor); ok && relation.inheritance != nil {
			if err := validateInheritance(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*relationDescriptor); ok && relation.view != nil {
			if err := validateViewDefinition(e.catalog.read(next), relation); err != nil {
				return err
			}
		}
	}
	return nil
}
func (e *Engine) clearPersistentSequenceMemoryLocked() {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	for _, s := range e.sessions {
		for a := range s.sequences {
			r, ok := e.catalog.state.Relation(a)
			keep := false
			if ok {
				ns, ok := e.catalog.state.Namespace(r.namespace)
				keep = ok && ns.temporary == s.token
			}
			if !keep {
				delete(s.sequences, a)
				if s.lastSequence == a {
					s.lastSequence = ids.ObjectAddress{}
				}
			}
		}
	}
}

type dumpSequence struct {
	owner, schema, name     string
	options                 sequenceOptions
	state                   sequenceGenerator
	comment                 string
	ownedTable, ownedColumn string
	divergent               bool
}

func sequenceDumpStatements(sequences []dumpSequence) (creation, completion []string) {
	sort.Slice(sequences, func(i, j int) bool {
		a, b := sequences[i], sequences[j]
		if a.owner != b.owner {
			return a.owner < b.owner
		}
		if a.schema != b.schema {
			return a.schema < b.schema
		}
		return a.name < b.name
	})
	var prefix, suffix []string
	owner := ""
	for _, s := range sequences {
		if owner != s.owner {
			directive := "SET SESSION AUTHORIZATION '" + strings.ReplaceAll(s.owner, "'", "''") + "';\n"
			prefix = append(prefix, directive)
			suffix = append(suffix, directive)
			owner = s.owner
		}
		name := quoteIdentifier(s.schema) + "." + quoteIdentifier(s.name)
		persistence := ""
		if s.options.persistence == "unlogged" {
			persistence = "UNLOGGED "
		}
		_, _, typ := sequenceTypeLimits(s.options.typ)
		cycle := "NO CYCLE"
		if s.options.cycle {
			cycle = "CYCLE"
		}
		if !s.options.identity {
			prefix = append(prefix, fmt.Sprintf("CREATE %sSEQUENCE %s AS %s INCREMENT BY %d MINVALUE %d MAXVALUE %d START WITH %d CACHE %d %s;\n", persistence, name, strings.ToUpper(typ), s.options.increment, s.options.min, s.options.max, s.options.start, s.options.cache, cycle))
			if s.ownedTable != "" {
				suffix = append(suffix, "ALTER SEQUENCE "+name+" OWNED BY "+s.ownedTable+"."+quoteIdentifier(s.ownedColumn)+";\n")
			}
		} else {
			if s.divergent {
				suffix = append(suffix, fmt.Sprintf("ALTER SEQUENCE %s AS %s %s RESTART WITH %d;\n", name, strings.ToUpper(typ), sequenceNumericSQL(s.options), s.state.last))
			}
			if s.options.persistence == "unlogged" {
				suffix = append(suffix, "ALTER SEQUENCE "+name+" SET UNLOGGED;\n")
			}
		}
		if s.comment != "" {
			if s.options.identity {
				suffix = append(suffix, "COMMENT ON SEQUENCE "+name+" IS "+commentLiteral(s.comment)+";\n")
			} else {
				prefix = append(prefix, "COMMENT ON SEQUENCE "+name+" IS "+commentLiteral(s.comment)+";\n")
			}
		}
		suffix = append(suffix, fmt.Sprintf("SELECT setval('%s',%d,%t);\n", strings.ReplaceAll(name, "'", "''"), s.state.last, s.state.called))
	}
	return prefix, suffix
}
func restoreSequenceState(s *parser.Select) bool {
	if len(s.With) != 0 || s.From != nil || s.Where != nil || s.GroupBy != nil || s.Having != nil || s.Order != nil || len(s.Targets) != 1 {
		return false
	}
	for _, label := range s.Labels {
		if label.Text != "" {
			return false
		}
	}
	call, ok := s.Targets[0].(parser.CallExpr)
	if !ok || len(call.Name.Parts) != 1 || call.Name.Parts[0].Text != "setval" || len(call.Args) != 3 {
		return false
	}
	for _, a := range call.Args {
		if _, ok := a.(parser.LiteralExpr); !ok {
			return false
		}
	}
	first := call.Args[0].(parser.LiteralExpr)
	second := call.Args[1].(parser.LiteralExpr)
	third := call.Args[2].(parser.LiteralExpr)
	return first.Value.Kind == parser.StringValue && second.Value.Kind == parser.IntegerValue && third.Value.Kind == parser.BooleanValue
}

func arrayDomainDependency(typ *sqltype.Descriptor) bool {
	if typ.IsDomain() {
		return true
	}
	return typ.IsArray() && arrayDomainDependency(typ.Element())
}
func retainedArrayDependencies(source, target *catalogState, typ *sqltype.Descriptor) bool {
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
	old, ok := source.Type(address)
	if !ok {
		return false
	}
	next, ok := target.Type(address)
	if !ok || old.typ != typ || next.typ != typ || old.array != next.array || old.Namespace() != next.Namespace() || old.owner != next.owner || old.Revision() != next.Revision() {
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
func (e *Engine) retainTemporaryViewProvidersLocked(next *catalogState, temporary map[ids.ObjectAddress]Descriptor) error {
	catalog := e.catalog.read(e.catalog.state)
	for _, descriptor := range temporary {
		consumer, ok := descriptor.(*relationDescriptor)
		if !ok || consumer.view == nil {
			continue
		}
		for ref, captured := range viewOf(consumer).references {
			address := ref.Address
			address.SubID = 0
			old, ok := e.catalog.state.Relation(address)
			if !ok {
				continue
			}
			if temporary[old.namespace] != nil {
				if !storedViewReferenceValid(catalog, consumer, ref) {
					return ids.ErrCatalogReplaced
				}
				continue
			}
			fail := func() error {
				return fmt.Errorf("stuntgres: catalog replacement with live temporary view persistent dependencies is unsupported")
			}
			if old != captured || old.relationKind != catalogRelation || !pgDependPinned(catalog, address) {
				return fail()
			}
			candidate, ok := next.Relation(address)
			if !ok || candidate.relationKind != old.relationKind || candidate.namespace != old.namespace || candidate.name != old.name || candidate.owner != old.owner || candidate.virtualView != old.virtualView || candidate.hasIndexes != old.hasIndexes || candidate.hasSubclasses != old.hasSubclasses || candidate.attributeHighWater != old.attributeHighWater || !slices.Equal(candidate.attributes, old.attributes) || len(candidate.columns) != len(old.columns) || candidate.acl.Explicit != old.acl.Explicit || !slices.Equal(candidate.acl.Entries, old.acl.Entries) || len(candidate.columnACL) != len(old.columnACL) {
				return fail()
			}
			for attribute, acl := range old.columnACL {
				other, ok := candidate.columnACL[attribute]
				if !ok || other.Explicit != acl.Explicit || !slices.Equal(other.Entries, acl.Entries) {
					return fail()
				}
			}
			for i, column := range old.columns {
				other := candidate.columns[i]
				if column.Name != other.Name || column.Usage.Type.ID() != other.Usage.Type.ID() || column.Usage.Typmod != other.Usage.Typmod || column.Usage.Collation.ID != other.Usage.Collation.ID || column.Nullable != other.Nullable || column.NullabilityKnown != other.NullabilityKnown {
					return fail()
				}
			}
			namespace, ok := e.catalog.state.Namespace(old.namespace)
			other, candidateOK := next.Namespace(old.namespace)
			if !ok || !candidateOK || !pgDependPinned(catalog, old.namespace) || namespace.name != other.name || namespace.owner != other.owner || namespace.temporary != other.temporary || namespace.temporaryToast != other.temporaryToast || namespace.acl.Explicit != other.acl.Explicit || !slices.Equal(namespace.acl.Entries, other.acl.Entries) {
				return fail()
			}
			next.setDescriptor(address, old)
			next.setDescriptor(old.namespace, namespace)
		}
	}
	return nil
}
