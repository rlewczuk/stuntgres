package engine

import (
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
func (e *Engine) preserveTemporaryLocked(next *catalogState, storage map[ObjectAddress]*relationStorage, sequences map[ObjectAddress]*sequenceGenerator) error {
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
	// Exact retained sequence descriptors also retain their builtin type
	// generation. Keep those dependencies, then retag only affected imported
	// columns and detached live values; never rewrite retained TEMP or source rows.
	var retainedInt8, retainedBool *sqltype.Descriptor
	for _, d := range temporary {
		if r, ok := d.(*relationDescriptor); ok && r.relationKind == sequenceRelation {
			retainedInt8 = r.columns[0].Usage.Type
			retainedBool = r.columns[2].Usage.Type
		}
	}
	typesChanged := false
	for _, typ := range [...]*sqltype.Descriptor{retainedInt8, retainedBool} {
		if typ == nil {
			continue
		}
		address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.ID())}
		if imported, ok := next.descriptors[address].(*typeDescriptor); ok && imported.typ != typ {
			next.descriptors[address] = e.state.descriptors[address]
			typesChanged = true
		}
	}
	if typesChanged {
		retainedType := func(typ *sqltype.Descriptor) *sqltype.Descriptor {
			if typ.ID() == sqltype.Int8 && retainedInt8 != nil {
				return retainedInt8
			}
			if typ.ID() == sqltype.Bool && retainedBool != nil {
				return retainedBool
			}
			return typ
		}
		for address, d := range next.descriptors {
			r, ok := d.(*relationDescriptor)
			if !ok {
				continue
			}
			var columns []ColumnDescriptor
			for i, column := range r.columns {
				typ := retainedType(column.Usage.Type)
				if typ != column.Usage.Type {
					if columns == nil {
						columns = append([]ColumnDescriptor(nil), r.columns...)
					}
					columns[i].Usage.Type = typ
				}
			}
			var defaults map[int32]columnDefault
			for attribute, definition := range r.defaults {
				expression, changed, err := retagBoundTypes(definition.expression, retainedType)
				if err != nil {
					return err
				}
				if changed {
					if defaults == nil {
						defaults = make(map[int32]columnDefault, len(r.defaults))
						for key, original := range r.defaults {
							defaults[key] = original
						}
					}
					definition.expression = expression
					defaults[attribute] = definition
				}
			}
			if columns != nil || defaults != nil {
				n := *r
				if columns != nil {
					n.columns = columns
				}
				if defaults != nil {
					n.defaults = defaults
				}
				next.descriptors[address] = &n
			}
		}
		for _, relation := range storage {
			for i, row := range relation.versions {
				var values []sqltype.Value
				for j, value := range row.values {
					typ := retainedType(value.Type())
					if typ == value.Type() {
						continue
					}
					var rebound sqltype.Value
					if value.IsNull() {
						rebound = sqltype.Null(typ)
					} else if typ.ID() == sqltype.Int8 {
						number, err := value.Integer()
						if err != nil {
							return err
						}
						rebound, err = sqltype.Integer(typ, number)
						if err != nil {
							return err
						}
					} else {
						flag, err := value.Boolean()
						if err != nil {
							return err
						}
						rebound = sqltype.Boolean(typ, flag)
					}
					if values == nil {
						values = append([]sqltype.Value(nil), row.values...)
					}
					values[j] = rebound
				}
				if values != nil {
					relation.versions[i].values = values
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
	if err := e.remapCatalogExpressions(next, mapping); err != nil {
		return err
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
					if definition.unique != nil {
						constraint := *definition.unique
						if dest, remapped := mapping[constraint.Index]; remapped {
							constraint.Index = dest
						}
						definition.unique = &constraint
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
	for a, d := range temporary {
		next.descriptors[a] = d
		next.names[relationNameKey(d)] = a
		if g := e.sequences[a]; g != nil {
			sequences[a] = g
		}
	}
	next.epoch = e.state.epoch + 1
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
	if s.From != nil || s.Where != nil || len(s.GroupBy) != 0 || s.Having != nil || s.Order != nil || len(s.Targets) != 1 {
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
