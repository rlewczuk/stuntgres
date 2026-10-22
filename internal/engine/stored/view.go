package stored

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A definition owns an immutable analyzed query and its creation-time source layout.
// Runtime source opening uses identities, never the retained deparser syntax.
type View struct {
	query             *plan.Query
	syntax            *parser.Select
	expressionSyntax  parser.Expr // Lexical outer frame for a frozen Boolean definition.
	mutationFrame     bool        // Synthetic frame 1 includes a mutation target row.
	sources           []ViewLeaf
	dependencies      []catalog.ExpressionDependency
	sequenceRefs      map[int]ids.ObjectRef
	literals          map[analyze.ViewLiteralPath]plan.LiteralExpr
	castUsages        map[analyze.ViewLiteralPath]sqltype.Usage
	routineInputs     []sqltype.Usage // Non-nil only for a routine command presentation.
	routineRefs       map[analyze.ViewLiteralPath]ids.ObjectRef
	operatorRefs      map[analyze.ViewLiteralPath]ids.ObjectRef
	composites        map[analyze.ViewLiteralPath]analyze.TypeOccurrence
	similarSubstrings map[analyze.ViewLiteralPath]bool
	posixSubstrings   map[analyze.ViewLiteralPath]bool
	columns           map[analyze.ViewLiteralPath]analyze.ViewColumn
	stars             map[analyze.ViewLiteralPath][]analyze.ViewColumn
	frames            map[uint32]*plan.ProjectNode
	queries           map[*parser.Select]*plan.ProjectNode
	namespaces        []analyze.ViewNamespace
	barrier, invoker  bool
	checkOption       CheckMode
	options           []string // Immutable ordered installed name=value facts, including explicit false.
	rule              ids.OID
	consumer          ids.ObjectAddress
	temporary         ids.SessionToken
	references        map[ids.ObjectRef]catalog.Descriptor
	collations        map[analyze.ViewLiteralPath]uint32
	quoteRevision     ids.DefinitionRevision // frozen defining revision, not later relation renames
}

// Stored leaves contain identities and frozen layout, never live storage or
// descriptors. Ordinals are SQL occurrences, including repeated self-joins.
type ViewLeaf struct {
	Ref          ids.ObjectRef
	Only         bool
	Attributes   []int32
	Reads        []int32
	AnalysisOnly bool
	Mark         plan.RowMark
}

// fromRoot pairs retained FROM syntax with its checked source graph. Mutation
// frame 1 also owns target slots, which are lexical input, not FROM syntax.
func (v *View) fromRoot(project *plan.ProjectNode) (plan.Node, error) {
	if project == nil || project.Syntax == nil {
		return nil, fmt.Errorf("stuntgres: missing stored source frame")
	}
	root := analyze.ViewFromRoot(project)
	if !v.mutationFrame || project.Frame != 1 || project.Syntax.From == nil {
		return root, nil
	}
	join, ok := root.(*plan.JoinNode)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing mutation source frame")
	}
	if _, ok := join.Left.(*plan.SingletonNode); !ok {
		return nil, fmt.Errorf("stuntgres: invalid mutation target frame")
	}
	return join.Right, nil
}

func ViewOptions(options []parser.ViewOption) (barrier, invoker bool, mode CheckMode, err error) {
	seen := make(map[string]bool, len(options))
	for _, option := range options {
		name := option.Name.Text
		if name != "check_option" && name != "security_barrier" && name != "security_invoker" {
			return false, false, CheckNone, sqlError("22023", fmt.Sprintf(`unrecognized parameter "%s"`, name))
		}
		if seen[name] {
			return false, false, CheckNone, sqlError("22023", fmt.Sprintf(`parameter "%s" specified more than once`, name))
		}
		seen[name] = true
		if name == "check_option" {
			switch {
			case strings.EqualFold(option.Value, "local"):
				mode = CheckLocal
			case strings.EqualFold(option.Value, "cascaded"):
				mode = CheckCascaded
			default:
				return false, false, CheckNone, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for enum option "check_option": %s`, option.Value), Detail: `Valid values are "local" and "cascaded".`}
			}
			continue
		}
		value, valid := false, false
		text := strings.ToLower(strings.TrimSpace(option.Value))
		if text == "1" {
			value, valid = true, true
		} else if text == "0" {
			valid = true
		} else if text != "" {
			for _, spelling := range []string{"true", "yes", "on"} {
				if strings.HasPrefix(spelling, text) {
					value, valid = true, true
				}
			}
			for _, spelling := range []string{"false", "no", "off"} {
				if strings.HasPrefix(spelling, text) {
					if valid {
						valid = false
						break
					}
					valid = true
				}
			}
		}
		if !valid {
			return false, false, CheckNone, sqlError("22023", fmt.Sprintf(`invalid value for boolean option "%s": %s`, name, option.Value))
		}
		if name == "security_barrier" {
			barrier = value
		} else {
			invoker = value
		}
	}
	return
}

func (v *View) validateNamespaces() error {
	expected := 0
	var countFrom func(parser.FromItem)
	countFrom = func(item parser.FromItem) {
		if item == nil {
			return
		}
		expected++
		switch n := item.(type) {
		case *parser.AliasReference:
			countFrom(n.Input)
		case *parser.JoinReference:
			countFrom(n.Left)
			countFrom(n.Right)
		}
	}
	for _, project := range v.frames {
		if project.Syntax != nil {
			countFrom(project.Syntax.From)
		}
	}
	if expected != len(v.namespaces) {
		return fmt.Errorf("stuntgres: invalid view namespace count")
	}
	for _, ns := range v.namespaces {
		project := v.frames[ns.Frame]
		if project == nil {
			return fmt.Errorf("stuntgres: missing view namespace frame")
		}
		if ns.Node < analyze.ViewSourceNamespace || ns.Node > analyze.ViewRegexpNamespace {
			return fmt.Errorf("stuntgres: invalid view namespace kind")
		}
		root := v.namespaceRoot(ns)
		if root == nil {
			return fmt.Errorf("stuntgres: missing view namespace node")
		}
		check := func(columns []analyze.ViewNamespaceColumn) error {
			for _, column := range columns {
				if column.Slot < 0 || column.Slot >= len(root.Schema()) {
					return fmt.Errorf("stuntgres: invalid view namespace slot")
				}
				if column.Leaf >= 0 {
					if column.Leaf >= len(v.sources) {
						return fmt.Errorf("stuntgres: invalid view namespace occurrence")
					}
					found := false
					for _, attribute := range v.sources[column.Leaf].Attributes {
						found = found || attribute == column.Attribute
					}
					if !found {
						return fmt.Errorf("stuntgres: invalid view namespace attribute")
					}
				} else if column.Attribute != 0 {
					return fmt.Errorf("stuntgres: invalid computed view namespace identity")
				}
			}
			return nil
		}
		if err := check(ns.Columns); err != nil {
			return err
		}
		for _, q := range ns.Qualifiers {
			if err := check(q.Columns); err != nil {
				return err
			}
		}
		if len(ns.Keys) > len(ns.Columns) {
			return fmt.Errorf("stuntgres: invalid view USING namespace")
		}
		for i, key := range ns.Keys {
			if ns.Columns[i].Name != key {
				return fmt.Errorf("stuntgres: invalid view USING key label")
			}
		}
	}
	return nil
}

func (v *View) namespaceRoot(ns analyze.ViewNamespace) plan.Node {
	project := v.frames[ns.Frame]
	if project == nil {
		return nil
	}
	from := analyze.ViewFromRoot(project)
	switch ns.Node {
	case analyze.ViewSourceNamespace:
		return viewSourceAt(from, ns.Ordinal)
	case analyze.ViewJoinNamespace:
		return viewJoinAt(from, ns.Ordinal)
	case analyze.ViewValuesNamespace:
		return viewValuesAt(from, ns.Ordinal)
	case analyze.ViewFunctionNamespace:
		return viewFunctionFromAt(from, ns.Ordinal, false)
	case analyze.ViewRegexpNamespace:
		return viewFunctionFromAt(from, ns.Ordinal, true)
	case analyze.ViewDerivedNamespace:
		if ns.Ordinal < 0 {
			return ViewCTEOrdinal(from, ns.Ordinal)
		}
		if child := v.frames[uint32(ns.Ordinal)]; child != nil {
			return viewDerivedAt(from, child.Syntax)
		}
	}
	return nil
}
