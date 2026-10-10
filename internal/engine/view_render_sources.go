package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
)

func viewSourceAlias(occurrence int) string {
	return "__view_source_" + strconv.Itoa(occurrence)
}

func viewFromRoot(node LogicalNode) LogicalNode {
	for {
		switch n := node.(type) {
		case *limitNode:
			node = n.child
		case *evaluationNode:
			node = n.child
		case *rowLockNode:
			node = n.child
		case *projectNode:
			node = n.child
		case *filterNode:
			node = n.child
		case *sortNode:
			node = n.child
		case *aggregateNode:
			node = n.child
		case *windowNode:
			node = n.child
		case *distinctNode:
			node = n.child
		default:
			return node
		}
	}
}

func viewJoinAt(root LogicalNode, ordinal int) *joinNode {
	var visit func(LogicalNode) *joinNode
	visit = func(node LogicalNode) *joinNode {
		n, ok := node.(*joinNode)
		if !ok {
			return nil
		}
		if ordinal == n.viewOrdinal {
			return n
		}
		if found := visit(n.left); found != nil {
			return found
		}
		return visit(n.right)
	}
	return visit(root)
}

// Rendering state is private to one definition request. Frozen slots index the
// bound tree, while columns below follow the current emitted positional order.
// No SQL is rebound and no persistent execution layout is changed.
type viewRenderer struct {
	*viewDefinition
	query                       *BoundQuery
	frame                       uint32
	slots                       map[LogicalNode]map[int]string
	reserved                    map[string]bool
	namespace, occurrence, join int
	values                      int
	presentation                viewPresentation
	simple                      bool
	rowTargets                  map[string]string
}
type viewRenderColumn struct {
	name string
	slot int // -1 is a newly added, unreferenced attribute.
}
type viewRenderFrom struct {
	text, relation string
	root           LogicalNode
	columns        []viewRenderColumn
	slots          map[int]string
	rowTarget      string
	rowTargetSQL   string
	rowSource      bool
}

func newViewRenderer(v *viewDefinition) *viewRenderer {
	r := &viewRenderer{viewDefinition: v, query: v.query, slots: make(map[LogicalNode]map[int]string), reserved: make(map[string]bool)}
	if v.syntax != nil && len(v.syntax.Locking) != 0 {
		r.rowTargets = make(map[string]string)
	}
	for _, ns := range v.namespaces {
		for _, c := range ns.columns {
			r.reserved[c.name] = true
		}
		for _, q := range ns.qualifiers {
			r.reserved[q.name] = true
			for _, c := range q.columns {
				r.reserved[c.name] = true
			}
		}
	}
	return r
}

func (v *viewRenderer) freshLabel(occurrence int, attribute int32) string {
	base := "__view_added_" + strconv.Itoa(occurrence) + "_" + strconv.FormatInt(int64(attribute), 10)
	name := base
	for suffix := 1; v.reserved[name]; suffix++ {
		name = base + "_" + strconv.Itoa(suffix)
	}
	v.reserved[name] = true
	return name
}

func (v *viewRenderer) slotSQL(ctx context.Context, catalog CatalogRead, root LogicalNode, slot int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if text, ok := v.slots[root][slot]; ok {
		return text, nil
	}
	return "", fmt.Errorf("stuntgres: missing view column namespace slot")
}

func (v *viewRenderer) sourceAlias(occurrence int) string {
	base := viewSourceAlias(occurrence)
	name := base
	for suffix := 1; v.reserved[name]; suffix++ {
		name = base + "_" + strconv.Itoa(suffix)
	}
	v.reserved[name] = true
	return name
}

func (v *viewRenderer) usingAlias(ordinal int) string {
	base := "__view_using_" + strconv.Itoa(ordinal)
	name := base
	for suffix := 1; v.reserved[name]; suffix++ {
		name = base + "_" + strconv.Itoa(suffix)
	}
	v.reserved[name] = true
	return name
}

// Apply every alias in actual emitted order, mapping frozen labels by slot.
// This handles prefixes after compaction and whole-join aliases hiding operands.
func (v *viewRenderer) aliasFrom(from viewRenderFrom, ns viewNamespace, name string) viewRenderFrom {
	labels := make(map[int]string, len(ns.columns))
	for _, c := range ns.columns {
		labels[c.slot] = c.name
	}
	// Private bridge labels identify positions, not user-visible output names.
	// Reserve every original label before allocating replacements so a later
	// user column cannot collide with an earlier duplicate's generated label.
	for _, c := range from.columns {
		v.reserved[c.name] = true
	}
	for _, label := range labels {
		v.reserved[label] = true
	}
	used := make(map[string]bool, len(from.columns))
	aliases := make([]string, len(from.columns))
	from.slots = make(map[int]string, len(from.columns))
	for i := range from.columns {
		c := &from.columns[i]
		if label, ok := labels[c.slot]; ok {
			c.name = label
		}
		if used[c.name] {
			c.name = v.freshLabel(v.namespace, int32(i+1))
		}
		used[c.name] = true
		aliases[i] = quoteIdentifier(c.name)
		if c.slot >= 0 {
			from.slots[c.slot] = quoteIdentifier(name) + "." + quoteIdentifier(c.name)
		}
	}
	text := "(" + from.text + ")"
	if from.relation != "" {
		text = from.relation
	}
	from.text = text + " AS " + quoteIdentifier(name)
	if len(aliases) > 0 {
		from.text += "(" + strings.Join(aliases, ", ") + ")"
	}
	from.relation = ""
	v.slots[from.root] = from.slots
	return from
}

func (v *viewRenderer) fromSQL(ctx context.Context, catalog CatalogRead, item parser.FromItem) (viewRenderFrom, error) {
	from, err := v.renderFromSQL(ctx, catalog, item)
	if err == nil && v.rowTargets != nil && from.rowSource {
		// Publish only the finalized visible target of this occurrence. An
		// enclosing alias must not overwrite or delete a sibling's base name.
		v.rowTargets[from.rowTarget] = from.rowTargetSQL
	}
	return from, err
}

func (v *viewRenderer) renderFromSQL(ctx context.Context, catalog CatalogRead, item parser.FromItem) (viewRenderFrom, error) {
	if err := ctx.Err(); err != nil {
		return viewRenderFrom{}, err
	}
	if v.namespace >= len(v.namespaces) {
		return viewRenderFrom{}, fmt.Errorf("stuntgres: missing view FROM namespace")
	}
	ns := v.namespaces[v.namespace]
	v.namespace++
	switch n := item.(type) {
	case *parser.DerivedQuery:
		root := viewDerivedAt(viewFromRoot(v.query.Root), n.Query)
		if root == nil {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: missing frozen derived query")
		}
		text, err := v.selectQuerySQL(ctx, catalog, n.Query, v.queries[n.Query])
		if err != nil {
			return viewRenderFrom{}, err
		}
		from := viewRenderFrom{text: text, root: root}
		for i, column := range root.Schema() {
			from.columns = append(from.columns, viewRenderColumn{name: column.Name, slot: i})
		}
		aliased := v.aliasFrom(from, ns, v.sourceAlias(len(v.sources)+v.namespace-1))
		aliased.relation = "(" + text + ")"
		return aliased, nil
	case *parser.ValuesQuery:
		ordinal := v.values
		v.values++
		text, err := v.valuesSQL(ctx, catalog, n, ordinal)
		if err != nil {
			return viewRenderFrom{}, err
		}
		root := viewValuesAt(v.query.Root, ordinal)
		from := viewRenderFrom{text: text, root: root}
		from.rowSource = true
		for i, column := range root.Schema() {
			from.columns = append(from.columns, viewRenderColumn{name: column.Name, slot: i})
		}
		aliased := v.aliasFrom(from, ns, v.sourceAlias(len(v.sources)+ordinal))
		// An explicit VALUES alias replaces the renderer's generated alias.
		aliased.relation = "(" + text + ")"
		return aliased, nil
	case *parser.TableReference:
		occurrence := v.occurrence
		v.occurrence++
		if occurrence >= len(v.sources) {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: invalid view source occurrence")
		}
		leaf := v.sources[occurrence]
		d, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
		if !ok {
			return viewRenderFrom{}, ErrCatalogReplaced
		}
		root := viewSourceAt(viewFromRoot(v.query.Root), occurrence)
		if root == nil {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: missing view source node")
		}
		frozen := make(map[int32]viewNamespaceColumn, len(ns.columns))
		for _, c := range ns.columns {
			frozen[c.attribute] = c
		}
		if v.simple {
			name := relationDiagnosticNameAt(catalog, d, v.presentation.path)
			from := viewRenderFrom{text: name, root: root, slots: make(map[int]string)}
			if v.rowTargets != nil {
				key := n.Name.Parts[len(n.Name.Parts)-1].Text
				from.rowTarget, from.rowSource = key, true
				from.rowTargetSQL = pgIdentifier(d.name)
			}
			for i, attribute := range d.attributes {
				if c, ok := frozen[attribute]; ok {
					from.slots[c.slot] = pgIdentifier(d.columns[i].Name)
					from.columns = append(from.columns, viewRenderColumn{name: d.columns[i].Name, slot: c.slot})
				}
			}
			v.slots[root] = from.slots
			return from, nil
		}
		from := viewRenderFrom{relation: quoteIdentifier(catalog.state.descriptors[d.namespace].Name()) + "." + quoteIdentifier(d.name), root: root}
		from.rowTarget, from.rowSource = n.Name.Parts[len(n.Name.Parts)-1].Text, true
		if v.presentation.deparse && v.presentation.pretty {
			from.relation = relationDiagnosticNameAt(catalog, d, v.presentation.path)
		}
		for _, attribute := range d.attributes {
			column := viewRenderColumn{slot: -1}
			if c, ok := frozen[attribute]; ok {
				column.name, column.slot = c.name, c.slot
			} else {
				column.name = v.freshLabel(occurrence, attribute)
			}
			from.columns = append(from.columns, column)
		}
		from.text = from.relation
		alias := v.sourceAlias(occurrence)
		aliased := v.aliasFrom(from, ns, alias)
		if v.rowTargets != nil {
			aliased.rowTargetSQL = quoteIdentifier(alias)
		}
		// A surrounding table alias replaces this default, not nests it.
		aliased.relation = from.relation
		return aliased, nil
	case *parser.AliasReference:
		from, err := v.renderFromSQL(ctx, catalog, n.Input)
		if err != nil {
			return viewRenderFrom{}, err
		}
		if v.rowTargets != nil && from.rowSource {
			from.rowTarget = n.Name.Text
			from.rowTargetSQL = quoteIdentifier(n.Name.Text)
		}
		return v.aliasFrom(from, ns, n.Name.Text), nil
	case *parser.JoinReference:
		ordinal := v.join
		v.join++
		bound := viewJoinAt(viewFromRoot(v.query.Root), ordinal)
		if bound == nil {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: missing view join occurrence")
		}
		usingAlias := ""
		if n.UsingAlias != nil {
			usingAlias = n.UsingAlias.Text
		} else {
			for i := range ns.keys {
				if ns.columns[i].slot >= bound.leftWidth+bound.rightWidth {
					// Reserve before rendering descendants. This qualifies the
					// exact merged usage without hiding either physical input.
					usingAlias = v.usingAlias(ordinal)
					break
				}
			}
		}
		left, err := v.fromSQL(ctx, catalog, n.Left)
		if err != nil {
			return viewRenderFrom{}, err
		}
		right, err := v.fromSQL(ctx, catalog, n.Right)
		if err != nil {
			return viewRenderFrom{}, err
		}
		from := viewRenderFrom{root: bound, slots: make(map[int]string, len(left.slots)+len(right.slots)+len(bound.merges))}
		for slot, text := range left.slots {
			from.slots[slot] = text
		}
		for slot, text := range right.slots {
			from.slots[slot+bound.leftWidth] = text
		}
		for i := range right.columns {
			if right.columns[i].slot >= 0 {
				right.columns[i].slot += bound.leftWidth
			}
		}
		v.slots[bound] = from.slots
		joinText := " JOIN "
		switch bound.kind {
		case parser.JoinLeft:
			joinText = " LEFT JOIN "
		case parser.JoinRight:
			joinText = " RIGHT JOIN "
		case parser.JoinFull:
			joinText = " FULL JOIN "
		}
		if n.On != nil {
			on, err := v.expressionSQL(ctx, catalog, n.On, viewLiteralPath{clause: viewJoinLiteral, target: ordinal})
			if err != nil {
				return viewRenderFrom{}, err
			}
			from.text = "(" + left.text + joinText + right.text + " ON " + on + ")"
		} else if len(ns.keys) > 0 {
			keys := make([]string, len(ns.keys))
			for i, key := range ns.keys {
				keys[i] = quoteIdentifier(key)
				c := ns.columns[i]
				from.columns = append(from.columns, viewRenderColumn{name: key, slot: c.slot})
				// A synthetic merged value is recreated by USING's implicit
				// common-type binder, never an unsupported explicit cast.
				if c.slot >= bound.leftWidth+bound.rightWidth {
					from.slots[c.slot] = quoteIdentifier(usingAlias) + "." + quoteIdentifier(key)
				}
			}
			from.text = "(" + left.text + joinText + right.text + " USING (" + strings.Join(keys, ", ") + ")"
			if usingAlias != "" {
				from.text += " AS " + quoteIdentifier(usingAlias)
			}
			from.text += ")"
		} else {
			if bound.kind == parser.JoinInner {
				from.text = "(" + left.text + " CROSS JOIN " + right.text + ")"
			} else {
				from.text = "(" + left.text + joinText + right.text + " ON TRUE)"
			}
		}
		for _, columns := range [][]viewRenderColumn{left.columns, right.columns} {
			for _, c := range columns {
				key := false
				for _, name := range ns.keys {
					if c.name == name {
						key = true
						break
					}
				}
				if !key {
					from.columns = append(from.columns, c)
				}
			}
		}
		return from, nil
	}
	return viewRenderFrom{}, fmt.Errorf("stuntgres: unsupported view FROM syntax %T", item)
}

func viewSourceAt(node LogicalNode, occurrence int) *sourceNode {
	switch n := node.(type) {
	case *sourceNode:
		if n.occurrence == occurrence {
			return n
		}
	case *joinNode:
		if found := viewSourceAt(n.left, occurrence); found != nil {
			return found
		}
		return viewSourceAt(n.right, occurrence)
	}
	return nil
}

func viewDerivedAt(node LogicalNode, syntax *parser.Select) *derivedNode {
	if node == nil {
		return nil
	}
	if derived, ok := node.(*derivedNode); ok {
		if project, ok := derived.query.Root.(*projectNode); ok && project.syntax == syntax {
			return derived
		}
	}
	left, right := queryInputs(node)
	if found := viewDerivedAt(left, syntax); found != nil {
		return found
	}
	return viewDerivedAt(right, syntax)
}
