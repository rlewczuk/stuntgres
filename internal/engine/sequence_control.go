package engine

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// Called under source synchronization; generator scalars are detached from the
// source, unlike immutable relation descriptors.
func copySequenceState(source *Engine, view catalog.Read) (map[ids.ObjectAddress]*storage.SequenceGenerator, error) {
	result := make(map[ids.ObjectAddress]*storage.SequenceGenerator)
	for a, d := range view.Descriptors() {
		r, ok := d.(*catalog.Relation)
		if !ok || r.RelationKind != catalog.SequenceRelation {
			continue
		}
		g := source.storage.Sequences[a]
		if g == nil {
			return nil, fmt.Errorf("stuntgres: sequence state missing")
		}
		copy := *g
		result[a] = &copy
	}
	return result, nil
}

func (e *Engine) clearPersistentSequenceMemoryLocked() {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	for _, s := range e.sessions {
		s.sqlRoutines = nil
		s.sequences.retainLocked(func(a ids.ObjectAddress) bool {
			r, ok := e.catalog.state.Relation(a)
			if !ok {
				return false
			}
			ns, ok := e.catalog.state.Namespace(r.Namespace())
			return ok && ns.Temporary == s.token
		})
	}
}

type dumpSequence struct {
	owner, schema, name     string
	options                 catalog.SequenceOptions
	state                   storage.SequenceGenerator
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
		name := catalog.QuoteIdentifier(s.schema) + "." + catalog.QuoteIdentifier(s.name)
		persistence := ""
		if s.options.Persistence == "unlogged" {
			persistence = "UNLOGGED "
		}
		_, _, typ := sequenceTypeLimits(s.options.SQLType)
		cycle := "NO CYCLE"
		if s.options.Cycle {
			cycle = "CYCLE"
		}
		if !s.options.Identity {
			prefix = append(prefix, fmt.Sprintf("CREATE %sSEQUENCE %s AS %s INCREMENT BY %d MINVALUE %d MAXVALUE %d START WITH %d CACHE %d %s;\n", persistence, name, strings.ToUpper(typ), s.options.Increment, s.options.Min, s.options.Max, s.options.Start, s.options.Cache, cycle))
			if s.ownedTable != "" {
				suffix = append(suffix, "ALTER SEQUENCE "+name+" OWNED BY "+s.ownedTable+"."+catalog.QuoteIdentifier(s.ownedColumn)+";\n")
			}
		} else {
			if s.divergent {
				suffix = append(suffix, fmt.Sprintf("ALTER SEQUENCE %s AS %s %s RESTART WITH %d;\n", name, strings.ToUpper(typ), sequenceNumericSQL(s.options), s.state.Last))
			}
			if s.options.Persistence == "unlogged" {
				suffix = append(suffix, "ALTER SEQUENCE "+name+" SET UNLOGGED;\n")
			}
		}
		if s.comment != "" {
			if s.options.Identity {
				suffix = append(suffix, "COMMENT ON SEQUENCE "+name+" IS "+commentLiteral(s.comment)+";\n")
			} else {
				prefix = append(prefix, "COMMENT ON SEQUENCE "+name+" IS "+commentLiteral(s.comment)+";\n")
			}
		}
		suffix = append(suffix, fmt.Sprintf("SELECT setval('%s',%d,%t);\n", strings.ReplaceAll(name, "'", "''"), s.state.Last, s.state.Called))
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
