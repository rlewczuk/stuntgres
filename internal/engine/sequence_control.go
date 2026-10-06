package engine

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/parser"
)

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
				if dest, ok := mapping[n.namespace]; ok {
					n.namespace = dest
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
}

func sequenceDumpStatements(sequences []dumpSequence) []string {
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
	var lines []string
	owner := ""
	for _, s := range sequences {
		if owner != s.owner {
			lines = append(lines, "SET SESSION AUTHORIZATION '"+strings.ReplaceAll(s.owner, "'", "''")+"';\n")
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
		lines = append(lines, fmt.Sprintf("CREATE %sSEQUENCE %s AS %s INCREMENT BY %d MINVALUE %d MAXVALUE %d START WITH %d CACHE %d %s;\n", persistence, name, strings.ToUpper(typ), s.options.increment, s.options.min, s.options.max, s.options.start, s.options.cache, cycle))
		if s.ownedTable != "" {
			lines = append(lines, "ALTER SEQUENCE "+name+" OWNED BY "+s.ownedTable+"."+quoteIdentifier(s.ownedColumn)+";\n")
		}
		if s.comment != "" {
			lines = append(lines, "COMMENT ON SEQUENCE "+name+" IS "+commentLiteral(s.comment)+";\n")
		}
		lines = append(lines, fmt.Sprintf("SELECT setval('%s',%d,%t);\n", strings.ReplaceAll(name, "'", "''"), s.state.last, s.state.called))
	}
	return lines
}
func restoreSequenceState(s *parser.Select) bool {
	if len(s.Name.Parts) != 0 || s.Star || s.Where != nil || s.Order != nil || len(s.Targets) != 1 {
		return false
	}
	call, ok := s.Targets[0].(parser.CallExpr)
	if !ok || call.Name.Text != "setval" || len(call.Args) != 3 {
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
