package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement transfers only portable objects. SessionToken authority belongs
// to the source physical session and cannot be imported, even by self replacement:
// the destination overlays its exact current live TEMP objects after capture.
// Keep the supplied published view immutable and use this private candidate for
// provider validation and generator/storage capture as well as installation.
func persistentReplacementCatalog(view CatalogRead) (CatalogRead, error) {
	for _, descriptor := range view.state.descriptors {
		if extension, ok := descriptor.(*extensionDescriptor); ok {
			namespace, ok := view.state.descriptors[extension.targetNamespace].(*namespaceDescriptor)
			if !ok {
				return CatalogRead{}, fmt.Errorf("stuntgres: extension namespace is missing")
			}
			if namespace.temporary != (SessionToken{}) {
				return CatalogRead{}, fmt.Errorf("stuntgres: temporary extension installation is not portable")
			}
		}
	}
	next := cloneCatalog(view.state)
	for a, d := range view.state.descriptors {
		ns, isNamespace := d.(*namespaceDescriptor)
		parent, hasParent := view.state.descriptors[d.Namespace()].(*namespaceDescriptor)
		if isNamespace && ns.temporary != (SessionToken{}) || hasParent && parent.temporary != (SessionToken{}) {
			delete(next.descriptors, a)
		}
	}
	for key, a := range view.state.names {
		parent, hasParent := view.state.descriptors[key.Namespace].(*namespaceDescriptor)
		if view.state.descriptors[a] != nil && next.descriptors[a] == nil || hasParent && parent.temporary != (SessionToken{}) {
			delete(next.names, key)
		}
	}
	pruneRemovedNamespaceDefaults(view.state, next)
	return CatalogRead{owner: view.owner, state: next}, nil
}

// Called under source synchronization; generator scalars are detached from the
// source, unlike immutable relation descriptors.
func copySequenceState(source *Engine, view CatalogRead) (map[ObjectAddress]*sequenceGenerator, error) {
	result := make(map[ObjectAddress]*sequenceGenerator)
	for a, d := range view.state.descriptors {
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
func (e *Engine) preserveTemporaryLocked(ctx context.Context, next *catalogState, storage map[ObjectAddress]*relationStorage, sequences map[ObjectAddress]*sequenceGenerator, restoreOp *Operation) error {
	e.retainDatabaseLocked(next)
	if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
		return err
	}
	temporary := make(map[ObjectAddress]Descriptor)
	for a, d := range e.state.descriptors {
		if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary != (SessionToken{}) {
			temporary[a] = d
		}
	}
	namespaces := make(map[ObjectAddress]bool, len(temporary))
	for address := range temporary {
		namespaces[address] = true
	}
	children, err := namespaceChildren(CatalogRead{owner: e, state: e.state}, namespaces)
	if err != nil {
		return err
	}
	for _, address := range children {
		descriptor := e.state.descriptors[address]
		if extension, ok := descriptor.(*extensionDescriptor); ok {
			if imported, exists := next.names[descriptorNameKey(extension)]; exists && next.descriptors[imported] != extension {
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
			for _, condition := range []*policyExpression{policy.using, policy.check} {
				if condition == nil {
					continue
				}
				for _, leaf := range condition.presentation.sources {
					if _, retained := temporary[leaf.ref.Address]; retained {
						continue
					}
					if next.descriptors[leaf.ref.Address] != e.state.descriptors[leaf.ref.Address] {
						return fmt.Errorf("stuntgres: catalog replacement with incompatible live temporary policy dependencies is unsupported")
					}
				}
				for _, dependency := range condition.presentation.dependencies {
					address := dependency.ref.Address
					address.SubID = 0
					if _, retained := temporary[address]; retained {
						continue
					}
					if next.descriptors[address] != e.state.descriptors[address] {
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
			if !retainedArrayDependencies(e.state, next, typ) {
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
		address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.ID())}
		source, ok := e.state.descriptors[address].(*typeDescriptor)
		if !ok || source.typ != typ {
			return
		}
		if imported, ok := next.descriptors[address].(*typeDescriptor); ok && imported.typ != typ {
			copy := *imported
			copy.typ = typ
			next.descriptors[address] = &copy
			typesChanged = true
		}
		if typ.IsArray() {
			retainBuiltin(typ.Element())
		}
		if source.array != 0 {
			if array, ok := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(source.array)}].(*typeDescriptor); ok {
				retainBuiltin(array.typ)
			}
		}
	}
	for _, descriptor := range temporary {
		if relation, ok := descriptor.(*relationDescriptor); ok {
			for _, column := range relation.columns {
				retainBuiltin(column.Usage.Type)
			}
			retainExpression := func(node BoundExpr) {
				walkBoundExpression(node, func(node BoundExpr) { retainBuiltin(node.ResultUsage().Type) })
			}
			for _, definition := range relation.defaults {
				retainExpression(definition.expression)
			}
			for _, definition := range relation.checkConstraints {
				retainExpression(definition.expression)
			}
			for _, policy := range relation.policies {
				for _, condition := range []*policyExpression{policy.using, policy.check} {
					if condition != nil {
						walkQueryArgumentExpression(condition.expression, func(node BoundExpr) { retainBuiltin(node.ResultUsage().Type) })
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
			if definition := relation.view; definition != nil {
				var retainQuery func(LogicalNode)
				retainQuery = func(node LogicalNode) {
					if node == nil {
						return
					}
					for _, column := range node.Schema() {
						retainBuiltin(column.Usage.Type)
					}
					queryExpressions(node, func(expression BoundExpr) {
						walkBoundExpression(expression, func(expression BoundExpr) {
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
					switch provider := e.state.descriptors[address].(type) {
					case *typeDescriptor:
						retainBuiltin(provider.typ)
					case *relationDescriptor:
						if ref.Address.SubID != 0 || provider != captured || provider.relationKind != catalogRelation || !pgDependPinned(CatalogRead{e, e.state}, address) {
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
	retainedIDs := make(map[OID]bool)
	for address, descriptor := range temporary {
		retainedIDs[address.ObjectID] = true
		if relation, ok := descriptor.(*relationDescriptor); ok {
			reserveDefinitionOIDs(retainedIDs, relation)
		}
	}
	reservedIDs := make(map[OID]bool, len(next.descriptors)+len(retainedIDs))
	for id := range retainedIDs {
		reservedIDs[id] = true
	}
	for address, descriptor := range next.descriptors {
		reservedIDs[address.ObjectID] = true
		if relation, ok := descriptor.(*relationDescriptor); ok {
			reserveDefinitionOIDs(reservedIDs, relation)
		}
	}
	mapping := make(map[ObjectAddress]ObjectAddress)
	for address := range next.descriptors {
		if !retainedIDs[address.ObjectID] {
			continue
		}
		for {
			oid, err := e.allocateOIDLocked()
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
			break
		}
	}
	if err := e.importEnumMembersLocked(next, mapping); err != nil {
		return err
	}
	defer func() { next.enumImports = nil }()
	domainsChanged, err := reownDomainTypes(next, mapping)
	if err != nil {
		return err
	}
	refreshEnumImportDestinations(next)
	if typesChanged || domainsChanged || len(mapping) != 0 || len(next.enumImports) != 0 || next.aclItemImport != nil {
		if err := reownCatalogScalarTypes(next, storage, mapping); err != nil {
			return err
		}
	}
	if len(mapping) != 0 {
		remapDefaultSchemas(next, mapping)
		originals := next.descriptors
		next.descriptors = make(map[ObjectAddress]Descriptor, len(originals)+len(temporary))
		next.names = make(map[NameKey]ObjectAddress, len(originals)+len(temporary))
		for a, d := range originals {
			target := a
			if dest, ok := mapping[a]; ok {
				target = dest
				if next.dumpPriorities == nil {
					next.dumpPriorities = make(map[ObjectAddress]OID)
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
				if target == a && d == e.state.descriptors[a] && pgDependPinned(CatalogRead{e, e.state}, a) {
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
			case *relationDescriptor:
				if target == a && r.relationKind == catalogRelation && d == e.state.descriptors[a] && pgDependPinned(CatalogRead{e, e.state}, a) {
					if _, moved := mapping[r.namespace]; !moved {
						// Remapping unrelated imported identities must not copy
						// the exact provider certified for retained TEMP consumers.
						break
					}
				}
				n := *r
				n.address = target
				if r.inheritance != nil {
					n.inheritance = remapInheritance(r.inheritance, func(address ObjectAddress) ObjectAddress {
						if destination, ok := mapping[address]; ok {
							return destination
						}
						return address
					}, func(origin inheritanceOrigin) OID { return origin.constraint })
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
			next.descriptors[target] = d
			next.names[descriptorNameKey(d)] = target
		}
		// Copy into fresh maps before deleting old keys to avoid remap collisions.
		oldStorage := make(map[ObjectAddress]*relationStorage, len(storage))
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
		oldSequences := make(map[ObjectAddress]*sequenceGenerator, len(sequences))
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
	next.epoch = e.state.epoch + 1
	if err := e.remapCatalogExpressions(next, mapping); err != nil {
		return err
	}
	if err := rebindImportedExclusions(next); err != nil {
		return err
	}
	if err := e.repairRemappedGenerationsLocked(ctx, next, storage, sequences, mapping, restoreOp); err != nil {
		return err
	}
	for a, d := range temporary {
		next.descriptors[a] = d
		next.names[descriptorNameKey(d)] = a
		if rows := e.storage[a]; rows != nil {
			storage[a] = rows
		}
		if g := e.sequences[a]; g != nil {
			sequences[a] = g
		}
	}
	for address, descriptor := range temporary {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok || len(relation.policies) == 0 {
			continue
		}
		view := CatalogRead{owner: e, state: next}
		remap := func(ref ObjectRef) ObjectRef { ref.Epoch = next.epoch; return ref }
		typ := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
		replacement, err := remapPolicies(view, relation, nil, remap, typ, nil)
		if err != nil {
			return err
		}
		next.descriptors[address] = replacement
	}
	previous := make(map[ObjectAddress]Descriptor, len(next.descriptors))
	for address, descriptor := range next.descriptors {
		previous[address] = descriptor
	}
	if err := e.remapViewRuleOIDs(next); err != nil {
		return err
	}
	if err := remapInheritanceConstraintOrigins(next, previous); err != nil {
		return err
	}
	for _, descriptor := range next.descriptors {
		if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.policies) != 0 {
			if err := validatePolicies(CatalogRead{owner: e, state: next}, relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*relationDescriptor); ok {
			if err := validateTypedAttachment(CatalogRead{owner: e, state: next}, relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*relationDescriptor); ok && relation.inheritance != nil {
			if err := validateInheritance(CatalogRead{owner: e, state: next}, relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*relationDescriptor); ok && relation.view != nil {
			if err := validateViewDefinition(CatalogRead{owner: e, state: next}, relation); err != nil {
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
			r, ok := e.state.descriptors[a].(*relationDescriptor)
			keep := false
			if ok {
				ns, ok := e.state.descriptors[r.namespace].(*namespaceDescriptor)
				keep = ok && ns.temporary == s.token
			}
			if !keep {
				delete(s.sequences, a)
				if s.lastSequence == a {
					s.lastSequence = ObjectAddress{}
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
	if s.From != nil || s.Where != nil || s.GroupBy != nil || s.Having != nil || s.Order != nil || len(s.Targets) != 1 {
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
	address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.ID())}
	old, ok := source.descriptors[address].(*typeDescriptor)
	if !ok {
		return false
	}
	next, ok := target.descriptors[address].(*typeDescriptor)
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
func (e *Engine) retainTemporaryViewProvidersLocked(next *catalogState, temporary map[ObjectAddress]Descriptor) error {
	catalog := CatalogRead{e, e.state}
	for _, descriptor := range temporary {
		consumer, ok := descriptor.(*relationDescriptor)
		if !ok || consumer.view == nil {
			continue
		}
		for ref, captured := range consumer.view.references {
			address := ref.Address
			address.SubID = 0
			old, ok := e.state.descriptors[address].(*relationDescriptor)
			if !ok {
				continue
			}
			if temporary[old.namespace] != nil {
				if !storedViewReferenceValid(catalog, consumer, ref) {
					return ErrCatalogReplaced
				}
				continue
			}
			fail := func() error {
				return fmt.Errorf("stuntgres: catalog replacement with live temporary view persistent dependencies is unsupported")
			}
			if old != captured || old.relationKind != catalogRelation || !pgDependPinned(catalog, address) {
				return fail()
			}
			candidate, ok := next.descriptors[address].(*relationDescriptor)
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
			namespace, ok := e.state.descriptors[old.namespace].(*namespaceDescriptor)
			other, candidateOK := next.descriptors[old.namespace].(*namespaceDescriptor)
			if !ok || !candidateOK || !pgDependPinned(catalog, old.namespace) || namespace.name != other.name || namespace.owner != other.owner || namespace.temporary != other.temporary || namespace.acl.Explicit != other.acl.Explicit || !slices.Equal(namespace.acl.Entries, other.acl.Entries) {
				return fail()
			}
			next.descriptors[address] = old
			next.descriptors[old.namespace] = namespace
		}
	}
	return nil
}
