package stored

import (
	"context"
	"fmt"
	"strconv"
	"unicode/utf8"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

type ruleRenderAlias struct {
	frame   uint32
	node    uint8
	ordinal int
}

type ruleRangeName struct {
	name     string
	columns  []parser.Identifier
	explicit bool
}

type ruleRangeTable struct {
	names            map[ruleRenderAlias]ruleRangeName
	used             map[string]int
	pseudos          [2]string
	target, excluded string
	modifier         *ruleRangeTable
}

// Names are allocated in RTE order, not rendering order. An explicit alias is
// a preferred name for its occurrence, not a reservation against earlier RTEs.
func (t *ruleRangeTable) allocate(key ruleRenderAlias, preferred string, alias *parser.AliasReference) ruleRangeName {
	if name, ok := t.names[key]; ok {
		return name
	}
	name := preferred
	if counter, exists := t.used[preferred]; exists {
		for {
			counter++
			suffix := "_" + strconv.Itoa(counter)
			length := len(preferred)
			if limit := 63 - len(suffix); length > limit {
				length = limit
				for length > 0 && !utf8.RuneStart(preferred[length]) {
					length--
				}
			}
			name = preferred[:length] + suffix
			if _, exists := t.used[name]; !exists {
				break
			}
		}
		t.used[preferred] = counter
	}
	if t.used == nil {
		t.used = make(map[string]int)
	}
	t.used[name] = 0
	if t.names == nil {
		t.names = make(map[ruleRenderAlias]ruleRangeName)
	}
	allocated := ruleRangeName{name: name}
	if alias != nil {
		allocated.columns, allocated.explicit = alias.Columns, alias.Name.Text != ""
	}
	t.names[key] = allocated
	return allocated
}

func (v *QueryRenderer) prepareRuleRangeTable(ctx context.Context, read catalog.Read, project *plan.ProjectNode) error {
	if !v.Presentation.Rule {
		return nil
	}
	if project == nil || project.Syntax == nil {
		return fmt.Errorf("stuntgres: missing rule range-table frame")
	}
	if _, exists := v.ruleTables[project.Frame]; exists {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root := v.View.query.Root.(*plan.ProjectNode)
	if project.Frame != root.Frame {
		if err := v.prepareRuleRangeTable(ctx, read, root); err != nil {
			return err
		}
	}
	var table ruleRangeTable
	var err error
	if project.Frame == root.Frame {
		table, err = v.initialRuleRangeTable(ctx, read, project.Frame)
	} else if v.ruleEntryParent != nil && project.Frame == v.ruleEntryFrame {
		err = table.inherit(ctx, v.ruleEntryParent)
	} else {
		parent := v.ruleTables[root.Frame]
		for i := len(v.ruleScopes) - 1; i >= 0; i-- {
			frame := v.ruleScopes[i]
			if frame != project.Frame {
				parent = v.ruleTables[frame]
				break
			}
		}
		err = table.inherit(ctx, &parent)
	}
	if err != nil {
		return err
	}
	from, err := v.fromRoot(project)
	if err != nil {
		return err
	}
	cursor := 0
	var walk func(parser.FromItem, *parser.AliasReference) error
	walk = func(item parser.FromItem, alias *parser.AliasReference) error {
		if item == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		for cursor < len(v.namespaces) && v.namespaces[cursor].Frame != project.Frame {
			cursor++
		}
		if cursor == len(v.namespaces) {
			return fmt.Errorf("stuntgres: missing rule range-table occurrence")
		}
		namespace := v.namespaces[cursor]
		cursor++
		key := ruleRenderAlias{namespace.Frame, namespace.Node, namespace.Ordinal}
		preferred := ""
		if alias != nil {
			preferred = alias.Name.Text
		}
		switch raw := item.(type) {
		case *parser.AliasReference:
			if alias == nil {
				alias = raw
			}
			return walk(raw.Input, alias)
		case *parser.JoinReference:
			if err := walk(raw.Left, nil); err != nil {
				return err
			}
			if err := walk(raw.Right, nil); err != nil {
				return err
			}
			// PostgreSQL adds the join RTE after its inputs. An unnamed join
			// has no range-table name, including when it has a USING alias.
			if preferred != "" {
				table.allocate(key, preferred, alias)
			}
			return nil
		case *parser.TableReference:
			if preferred == "" {
				if ViewCTETable(from, raw) != nil {
					preferred = raw.Name.Parts[0].Text
				} else {
					if namespace.Ordinal < 0 || namespace.Ordinal >= len(v.sources) {
						return fmt.Errorf("stuntgres: invalid rule range-table source")
					}
					relation, ok := read.Relation(v.sources[namespace.Ordinal].Ref.Address)
					if !ok {
						return ids.ErrCatalogReplaced
					}
					preferred = relation.Name()
				}
			}
		case *parser.DerivedQuery, *parser.ValuesQuery:
			if preferred == "" {
				preferred = "unnamed_subquery"
			}
		case *parser.FunctionTable:
			if preferred == "" {
				if len(namespace.Qualifiers) != 0 {
					preferred = namespace.Qualifiers[0].Name
				} else {
					calls := parser.NormalizedFunctionCalls(raw)
					preferred = calls[0].Name.Parts[len(calls[0].Name.Parts)-1].Text
				}
			}
		case *parser.FunctionFrom:
			if preferred == "" {
				if len(namespace.Qualifiers) != 0 {
					preferred = namespace.Qualifiers[0].Name
				} else {
					preferred = raw.Calls[0].Name.Parts[len(raw.Calls[0].Name.Parts)-1].Text
				}
			}
		case *parser.JSONTable:
			if preferred == "" {
				preferred = "json_table"
				if len(namespace.Qualifiers) != 0 {
					preferred = namespace.Qualifiers[0].Name
				}
			}
		default:
			return fmt.Errorf("stuntgres: unsupported rule range-table source %T", item)
		}
		table.allocate(key, preferred, alias)
		return nil
	}
	if err := walk(project.Syntax.From, nil); err != nil {
		return err
	}
	if project.Syntax.Values != nil {
		table.allocate(ruleRenderAlias{project.Frame, 0, 2}, "*VALUES*", nil)
	}
	if v.ruleTables == nil {
		v.ruleTables = make(map[uint32]ruleRangeTable)
	}
	v.ruleTables[project.Frame] = table
	if project.Frame == root.Frame {
		v.ruleRootNames = &table
	}
	return nil
}

func (v *QueryRenderer) ruleRangeName(namespace analyze.ViewNamespace) (ruleRangeName, error) {
	key := ruleRenderAlias{namespace.Frame, namespace.Node, namespace.Ordinal}
	name, exists := v.ruleTables[namespace.Frame].names[key]
	if !exists && namespace.Node != analyze.ViewJoinNamespace {
		return ruleRangeName{}, fmt.Errorf("stuntgres: missing allocated rule range-table name")
	}
	return name, nil
}
