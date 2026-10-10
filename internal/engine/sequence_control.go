package engine

import (
	"context"
	"fmt"
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
func persistentReplacementCatalog(view CatalogRead) CatalogRead {
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
	return CatalogRead{owner: view.owner, state: next}
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
	temporary := make(map[ObjectAddress]Descriptor)
	for a, d := range e.state.descriptors {
		if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary != (SessionToken{}) {
			temporary[a] = d
		}
	}
	for a, d := range e.state.descriptors {
		if temporary[d.Namespace()] != nil {
			temporary[a] = d
		}
	}
	// Persistent replacement cannot substitute imported domains for a live
	// TEMP array's exact dependency identities. Fail before allocator/state work.
	for _, descriptor := range temporary {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
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
		}
	}
	mapping := make(map[ObjectAddress]ObjectAddress)
	for a := range temporary {
		if next.descriptors[a] != nil {
			oid, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			dest := a
			dest.ObjectID = oid
			for next.descriptors[dest] != nil || temporary[dest] != nil {
				oid, err = e.allocateOIDLocked()
				if err != nil {
					return err
				}
				dest.ObjectID = oid
			}
			mapping[a] = dest
		}
	}
	domainsChanged, err := reownDomainTypes(next, mapping)
	if err != nil {
		return err
	}
	if typesChanged || domainsChanged || len(mapping) != 0 {
		if err := reownCatalogScalarTypes(next, storage, mapping); err != nil {
			return err
		}
	}
	if len(mapping) != 0 {
		originals := next.descriptors
		next.descriptors = make(map[ObjectAddress]Descriptor, len(originals)+len(temporary))
		next.names = make(map[NameKey]ObjectAddress, len(originals)+len(temporary))
		for a, d := range originals {
			target := a
			if dest, ok := mapping[a]; ok {
				target = dest
			}
			switch r := d.(type) {
			case *namespaceDescriptor:
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
			case *relationDescriptor:
				n := *r
				n.address = target
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
				if r.sqlIndex != nil {
					definition := *r.sqlIndex
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
			next.descriptors[target] = d
			next.names[relationNameKey(d)] = target
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
	if err := e.repairRemappedGenerationsLocked(ctx, next, storage, sequences, mapping, restoreOp); err != nil {
		return err
	}
	for a, d := range temporary {
		next.descriptors[a] = d
		next.names[relationNameKey(d)] = a
		if rows := e.storage[a]; rows != nil {
			storage[a] = rows
		}
		if g := e.sequences[a]; g != nil {
			sequences[a] = g
		}
	}
	if err := e.remapViewRuleOIDs(next); err != nil {
		return err
	}
	for _, descriptor := range next.descriptors {
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
