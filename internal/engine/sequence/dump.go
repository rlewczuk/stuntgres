package sequence

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Dump is one sequence as native Dump writes it: owner, name, options,
// generator state, comment and OWNED BY or identity linkage.
type Dump struct {
	Owner, Schema, Name     string
	Options                 catalog.SequenceOptions
	State                   storage.SequenceGenerator
	Comment                 string
	OwnedTable, OwnedColumn string
	Divergent               bool
}

// DumpStatements renders sequences, sorted by owner, schema and name, as the
// creation statements and the completion statements (ownership, identity
// divergence, comments and setval) that follow the tables.
func DumpStatements(sequences []Dump) (creation, completion []string) {
	sort.Slice(sequences, func(i, j int) bool {
		a, b := sequences[i], sequences[j]
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		if a.Schema != b.Schema {
			return a.Schema < b.Schema
		}
		return a.Name < b.Name
	})
	var prefix, suffix []string
	owner := ""
	for _, s := range sequences {
		if owner != s.Owner {
			directive := "SET SESSION AUTHORIZATION '" + strings.ReplaceAll(s.Owner, "'", "''") + "';\n"
			prefix = append(prefix, directive)
			suffix = append(suffix, directive)
			owner = s.Owner
		}
		name := catalog.QuoteIdentifier(s.Schema) + "." + catalog.QuoteIdentifier(s.Name)
		persistence := ""
		if s.Options.Persistence == "unlogged" {
			persistence = "UNLOGGED "
		}
		_, _, typ := TypeLimits(s.Options.SQLType)
		cycle := "NO CYCLE"
		if s.Options.Cycle {
			cycle = "CYCLE"
		}
		if !s.Options.Identity {
			prefix = append(prefix, fmt.Sprintf("CREATE %sSEQUENCE %s AS %s INCREMENT BY %d MINVALUE %d MAXVALUE %d START WITH %d CACHE %d %s;\n", persistence, name, strings.ToUpper(typ), s.Options.Increment, s.Options.Min, s.Options.Max, s.Options.Start, s.Options.Cache, cycle))
			if s.OwnedTable != "" {
				suffix = append(suffix, "ALTER SEQUENCE "+name+" OWNED BY "+s.OwnedTable+"."+catalog.QuoteIdentifier(s.OwnedColumn)+";\n")
			}
		} else {
			if s.Divergent {
				suffix = append(suffix, fmt.Sprintf("ALTER SEQUENCE %s AS %s %s RESTART WITH %d;\n", name, strings.ToUpper(typ), NumericSQL(s.Options), s.State.Last))
			}
			mode := "LOGGED"
			if s.Options.Persistence == "unlogged" {
				mode = "UNLOGGED"
			}
			suffix = append(suffix, "ALTER SEQUENCE "+name+" SET "+mode+";\n")
		}
		if s.Comment != "" {
			if s.Options.Identity {
				suffix = append(suffix, "COMMENT ON SEQUENCE "+name+" IS "+commentLiteral(s.Comment)+";\n")
			} else {
				prefix = append(prefix, "COMMENT ON SEQUENCE "+name+" IS "+commentLiteral(s.Comment)+";\n")
			}
		}
		suffix = append(suffix, fmt.Sprintf("SELECT setval('%s',%d,%t);\n", strings.ReplaceAll(name, "'", "''"), s.State.Last, s.State.Called))
	}
	return prefix, suffix
}

// RestoresState reports whether s is exactly the unqualified
// SELECT setval('name',value,called) statement that DumpStatements writes.
func RestoresState(s *parser.Select) bool {
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

// NumericSQL renders the numeric options of a sequence for CREATE or ALTER.
func NumericSQL(options catalog.SequenceOptions) string {
	cycle := "NO CYCLE"
	if options.Cycle {
		cycle = "CYCLE"
	}
	return fmt.Sprintf("INCREMENT BY %d MINVALUE %d MAXVALUE %d START WITH %d CACHE %d %s", options.Increment, options.Min, options.Max, options.Start, options.Cache, cycle)
}

// IdentityDumpDivergent reports whether an identity sequence differs from the
// defaults its column type implies, so the dump must ALTER it.
func IdentityDumpDivergent(options catalog.SequenceOptions, column sqltype.ID) bool {
	lo, hi, _ := TypeLimits(column)
	return options.SQLType != column || options.Min < lo || options.Max > hi || options.Start < lo || options.Start > hi
}
