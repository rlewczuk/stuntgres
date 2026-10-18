package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func viewSourceAlias(occurrence int) string {
	return "__view_source_" + strconv.Itoa(occurrence)
}

func viewFromRoot(node LogicalNode) LogicalNode {
	for {
		switch n := node.(type) {
		case *setProjectNode:
			node = n.child
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
		case *distinctOnNode:
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

func viewJoinOffset(root LogicalNode, target *joinNode) (int, bool) {
	if root == target {
		return 0, true
	}
	join, ok := root.(*joinNode)
	if !ok {
		return 0, false
	}
	if offset, found := viewJoinOffset(join.left, target); found {
		return offset, true
	}
	if offset, found := viewJoinOffset(join.right, target); found {
		return join.leftWidth + offset, true
	}
	return 0, false
}

// Rendering state is private to one definition request. Frozen slots index the
// bound tree, while columns below follow the current emitted positional order.
// No SQL is rebound and no persistent execution layout is changed.
type viewRenderer struct {
	*viewDefinition
	query           *BoundQuery
	frame           uint32
	slots           map[LogicalNode]map[int]string
	reserved        map[string]bool
	namespace       int
	depth           int
	renderFrames    map[uint32]viewRenderFrame
	aliases         map[viewRenderAlias]string
	presentation    viewPresentation
	simple          bool
	functionSources bool // Public presentation of a frame admitting FunctionFrom.
	// Summary names follow current attributes independently of literal presentation.
	currentNames bool
	rowTargets   map[string]string
	wholeRows    map[wholeRowSource]viewRenderWholeRow
}

type viewRenderFrame struct {
	namespace int
	base      [viewRegexpNamespace + 1]int
	seen      [viewRegexpNamespace + 1]bool
}
type viewRenderAlias struct {
	depth, ordinal  int
	node            uint8
	attribute       int32
	label, physical bool
}
type viewRenderColumn struct {
	name string
	slot int // -1 is a newly added, unreferenced attribute.
}
type viewRenderWholeRow struct {
	sql   string
	names []string
}
type viewRenderFrom struct {
	text, relation string
	root           LogicalNode
	columns        []viewRenderColumn
	slots          map[int]string
	rowTarget      string
	rowTargetSQL   string
	rowSource      bool
	namespace      int
	wholeSQL       string
}

func newViewRenderer(v *viewDefinition) *viewRenderer {
	r := &viewRenderer{viewDefinition: v, query: v.query, slots: make(map[LogicalNode]map[int]string), reserved: make(map[string]bool)}
	if v.syntax != nil && len(v.syntax.Locking) != 0 {
		r.rowTargets = make(map[string]string)
	}
	for _, captured := range v.composites {
		if captured.source.namespace == 0 {
			continue
		}
		if r.wholeRows == nil {
			r.wholeRows = make(map[wholeRowSource]viewRenderWholeRow)
		}
		r.wholeRows[captured.source] = viewRenderWholeRow{}
	}
	if len(v.namespaces) != 0 {
		r.renderFrames = make(map[uint32]viewRenderFrame)
	}
	for i, ns := range v.namespaces {
		frame, exists := r.renderFrames[ns.frame]
		if !exists {
			frame.namespace = i
		}
		if !frame.seen[ns.node] || ns.ordinal < frame.base[ns.node] {
			frame.base[ns.node], frame.seen[ns.node] = ns.ordinal, true
		}
		r.renderFrames[ns.frame] = frame
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

func (v *viewRenderer) freshLabel(ns viewNamespace, attribute int32, physical bool) string {
	key := v.renderAliasKey(ns)
	key.attribute, key.label, key.physical = attribute, true, physical
	base := "__view_added_" + strconv.Itoa(int(key.node)) + "_" + strconv.Itoa(key.ordinal) + "_" + strconv.FormatInt(int64(attribute), 10)
	if physical {
		base += "_physical"
	}
	if key.depth > 1 {
		base += "_query_" + strconv.Itoa(key.depth-1)
	}
	return v.allocateAlias(key, base)
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

// Equivalent sibling queries need identical emitted RTE names for SQL99
// replay. Normalize generated node IDs within the lexical frame, cache names
// across siblings, and separate depths so correlations cannot be shadowed.
func (v *viewRenderer) renderAliasKey(ns viewNamespace) viewRenderAlias {
	return viewRenderAlias{depth: v.depth, node: ns.node, ordinal: ns.ordinal - v.renderFrames[ns.frame].base[ns.node]}
}
func (v *viewRenderer) namespaceAlias(ns viewNamespace) string {
	key := v.renderAliasKey(ns)
	base := viewSourceAlias(key.ordinal)
	switch ns.node {
	case viewJoinNamespace:
		base = "__view_using_" + strconv.Itoa(key.ordinal)
	case viewValuesNamespace:
		base = "__view_values_" + strconv.Itoa(key.ordinal)
	case viewDerivedNamespace:
		base = "__view_derived_" + strconv.Itoa(key.ordinal)
	case viewFunctionNamespace, viewRegexpNamespace:
		base = "__view_function_" + strconv.Itoa(key.ordinal)
	}
	if key.depth > 1 {
		base += "_query_" + strconv.Itoa(key.depth-1)
	}
	return v.allocateAlias(key, base)
}
func (v *viewRenderer) allocateAlias(key viewRenderAlias, base string) string {
	if name, ok := v.aliases[key]; ok {
		return name
	}
	name := base
	for suffix := 1; v.reserved[name]; suffix++ {
		name = base + "_" + strconv.Itoa(suffix)
	}
	v.reserved[name] = true
	if v.aliases == nil {
		v.aliases = make(map[viewRenderAlias]string)
	}
	v.aliases[key] = name
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
	aliasWidth := len(from.columns)
	switch scan := from.root.(type) {
	case *worktableScanNode:
		if scan.cte.search != nil || scan.cte.cycle != nil {
			aliasWidth = scan.cte.userWidth
		}
	case *derivedNode:
		if scan.cte != nil && (scan.cte.search != nil || scan.cte.cycle != nil) {
			aliasWidth = scan.cte.userWidth
		}
	}
	// SEARCH/CYCLE references accept aliases only for original columns.
	// Generated columns retain their symbolic names but still need slots.
	used := make(map[string]bool, len(from.columns))
	for _, c := range from.columns[aliasWidth:] {
		used[c.name] = true
	}
	aliases := make([]string, aliasWidth)
	from.slots = make(map[int]string, len(from.columns))
	for i := range from.columns {
		c := &from.columns[i]
		if i < aliasWidth {
			if label, ok := labels[c.slot]; ok {
				c.name = label
			}
			if used[c.name] {
				c.name = v.freshLabel(ns, int32(i+1), false)
			}
			used[c.name] = true
			aliases[i] = quoteIdentifier(c.name)
		}
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
	from.wholeSQL = quoteIdentifier(name)
	v.slots[from.root] = from.slots
	return from
}

func viewHasFunctionSource(item parser.FromItem) bool {
	switch n := item.(type) {
	case *parser.FunctionTable:
		for _, call := range n.Calls {
			if selectedFunctionSourceName(call.Name.Parts[len(call.Name.Parts)-1].Text) {
				return true
			}
		}
		return false
	case *parser.FunctionFrom:
		return true
	case *parser.AliasReference:
		return viewHasFunctionSource(n.Input)
	case *parser.JoinReference:
		return viewHasFunctionSource(n.Left) || viewHasFunctionSource(n.Right)
	}
	return false
}

// Public inquiry aliases preserve actual current source names and only explicit
// column aliases. Dump's positional bridge aliases remain owned by aliasFrom.
func (v *viewRenderer) functionSourceAlias(from viewRenderFrom, name string, columns []parser.Identifier) viewRenderFrom {
	identifier := v.presentation.identifier
	qualifier := identifier(name)
	from.slots = make(map[int]string, len(from.columns))
	for i := range from.columns {
		if i < len(columns) {
			from.columns[i].name = columns[i].Text
		}
		column := from.columns[i]
		if column.slot >= 0 {
			from.slots[column.slot] = qualifier + "." + identifier(column.name)
		}
	}
	text := from.relation
	if text == "" {
		text = "(" + from.text + ")"
	}
	from.text = text + " " + qualifier
	if from.rowSource {
		from.rowTargetSQL = qualifier
	}
	if len(columns) != 0 {
		labels := make([]string, len(columns))
		for i, column := range columns {
			labels[i] = identifier(column.Text)
		}
		from.text += "(" + strings.Join(labels, ", ") + ")"
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
	if err == nil && v.wholeRows != nil && from.wholeSQL != "" {
		namespace := v.namespaces[from.namespace-1]
		for position, qualifier := range namespace.qualifiers {
			key := wholeRowSource{namespace: from.namespace, qualifier: position, frame: namespace.frame}
			if _, demanded := v.wholeRows[key]; !demanded {
				continue
			}
			names := make([]string, len(qualifier.columns))
			for i, column := range qualifier.columns {
				found := false
				for _, rendered := range from.columns {
					if rendered.slot == column.slot {
						names[i] = rendered.name
						found = true
						break
					}
				}
				if !found {
					return viewRenderFrom{}, fmt.Errorf("stuntgres: missing rendered whole-row field")
				}
			}
			v.wholeRows[wholeRowSource{namespace: from.namespace, qualifier: position, frame: namespace.frame}] = viewRenderWholeRow{sql: from.wholeSQL + ".*", names: names}
		}
	}
	return from, err
}

func (v *viewRenderer) renderFromSQL(ctx context.Context, catalog CatalogRead, item parser.FromItem) (result viewRenderFrom, failure error) {
	if err := ctx.Err(); err != nil {
		return viewRenderFrom{}, err
	}
	// Nested expressions can render in a different order from analysis.
	// Namespace entries retain their owning query frame and bound node ordinal.
	for v.namespace < len(v.namespaces) && v.namespaces[v.namespace].frame != v.frame {
		v.namespace++
	}
	if v.namespace >= len(v.namespaces) {
		return viewRenderFrom{}, fmt.Errorf("stuntgres: missing view FROM namespace")
	}
	ns := v.namespaces[v.namespace]
	namespaceIndex := v.namespace + 1
	defer func() {
		if failure == nil {
			result.namespace = namespaceIndex
		}
	}()
	v.namespace++
	switch n := item.(type) {
	case *parser.FunctionTable:
		return v.renderFunctionTableSQL(ctx, catalog, n, ns)
	case *parser.FunctionFrom:
		return v.renderFunctionTableSQL(ctx, catalog, &parser.FunctionTable{Calls: n.Calls, Ordinality: n.Ordinality, Lateral: n.Lateral, RowsFrom: len(n.Calls) > 1, Pos: n.Pos}, ns)
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
		aliased := v.aliasFrom(from, ns, v.namespaceAlias(ns))
		aliased.relation = "(" + text + ")"
		return aliased, nil
	case *parser.ValuesQuery:
		ordinal := ns.ordinal
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
		aliased := v.aliasFrom(from, ns, v.namespaceAlias(ns))
		// An explicit VALUES alias replaces the renderer's generated alias.
		aliased.relation = "(" + text + ")"
		return aliased, nil
	case *parser.TableReference:
		if root := viewCTETable(viewFromRoot(v.query.Root), n); root != nil {
			name := quoteIdentifier(n.Name.Parts[0].Text)
			from := viewRenderFrom{text: name, relation: name, root: root}
			for i, column := range root.Schema() {
				from.columns = append(from.columns, viewRenderColumn{name: column.Name, slot: i})
			}
			aliased := v.aliasFrom(from, ns, v.namespaceAlias(ns))
			aliased.relation = name
			return aliased, nil
		}
		occurrence := ns.ordinal
		if occurrence < 0 || occurrence >= len(v.sources) {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: invalid view source occurrence")
		}
		leaf := v.sources[occurrence]
		d, ok := catalog.Relation(leaf.ref.Address)
		if !ok {
			return viewRenderFrom{}, ids.ErrCatalogReplaced
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
			if v.presentation.output.QuoteAllIdentifiers {
				name = v.presentation.identifier(d.name)
				if !relationVisibleAt(catalog, d, v.presentation.path) {
					name = v.presentation.identifier(catalog.Get(d.namespace).Name()) + "." + name
				}
			}
			if leaf.only {
				name = "ONLY " + name
			}
			from := viewRenderFrom{text: name, root: root, slots: make(map[int]string)}
			from.wholeSQL = pgIdentifier(d.name)
			if v.rowTargets != nil {
				key := n.Name.Parts[len(n.Name.Parts)-1].Text
				from.rowTarget, from.rowSource = key, true
				from.rowTargetSQL = v.presentation.identifier(d.name)
			}
			for i, attribute := range d.Attributes {
				if c, ok := frozen[attribute]; ok {
					from.slots[c.slot] = v.presentation.identifier(d.Columns[i].Name)
					from.columns = append(from.columns, viewRenderColumn{name: d.Columns[i].Name, slot: c.slot})
				}
			}
			v.slots[root] = from.slots
			return from, nil
		}
		from := viewRenderFrom{root: root}
		from.rowTarget, from.rowSource = n.Name.Parts[len(n.Name.Parts)-1].Text, true
		if v.functionSources {
			if v.presentation.output.QuoteAllIdentifiers {
				from.relation = v.presentation.identifier(d.name)
				if !relationVisibleAt(catalog, d, v.presentation.path) {
					from.relation = v.presentation.identifier(catalog.Get(d.namespace).Name()) + "." + from.relation
				}
			} else {
				from.relation = relationDiagnosticNameAt(catalog, d, v.presentation.path)
			}
			if leaf.only {
				from.relation = "ONLY " + from.relation
			}
			from.text = from.relation
			from.slots = make(map[int]string, len(frozen))
			qualifier := v.presentation.identifier(d.name)
			from.rowTargetSQL = qualifier
			for i, attribute := range d.Attributes {
				column := viewRenderColumn{name: d.Columns[i].Name, slot: -1}
				if captured, ok := frozen[attribute]; ok {
					column.slot = captured.slot
					from.slots[column.slot] = qualifier + "." + v.presentation.identifier(column.name)
				}
				from.columns = append(from.columns, column)
			}
			v.slots[root] = from.slots
			return from, nil
		}
		from.relation = quoteIdentifier(catalog.Get(d.namespace).Name()) + "." + quoteIdentifier(d.name)
		if v.presentation.deparse && v.presentation.pretty && !v.presentation.output.QuoteAllIdentifiers {
			from.relation = relationDiagnosticNameAt(catalog, d, v.presentation.path)
		}
		if leaf.only {
			from.relation = "ONLY " + from.relation
		}
		for _, attribute := range d.Attributes {
			column := viewRenderColumn{slot: -1}
			if c, ok := frozen[attribute]; ok {
				column.name, column.slot = c.name, c.slot
			} else {
				column.name = v.freshLabel(ns, attribute, true)
			}
			from.columns = append(from.columns, column)
		}
		from.text = from.relation
		alias := v.namespaceAlias(ns)
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
		if n.Name.Text == "" {
			alias := v.namespaceAlias(ns)
			if v.functionSources {
				return v.functionSourceAlias(from, alias, nil), nil
			}
			return v.aliasFrom(from, ns, alias), nil
		}
		if v.rowTargets != nil && from.rowSource {
			from.rowTarget = n.Name.Text
			from.rowTargetSQL = quoteIdentifier(n.Name.Text)
		}
		aliasName := n.Name.Text
		if aliasName == "" {
			aliasName = v.namespaceAlias(ns)
		}
		if v.functionSources {
			return v.functionSourceAlias(from, aliasName, n.Columns), nil
		}
		return v.aliasFrom(from, ns, aliasName), nil
	case *parser.JoinReference:
		ordinal := ns.ordinal
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
					usingAlias = v.namespaceAlias(ns)
					break
				}
			}
		}
		left, err := v.fromSQL(ctx, catalog, n.Left)
		if err != nil {
			return viewRenderFrom{}, err
		}
		// Function arguments reference the owner's full FROM frame before the
		// right source exists. Publish only finalized legal predecessor slots.
		owner := viewFromRoot(v.query.Root)
		offset, found := viewJoinOffset(owner, bound)
		if !found {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: missing view join predecessor frame")
		}
		if v.slots[owner] == nil {
			v.slots[owner] = make(map[int]string, len(owner.Schema()))
		}
		for slot, text := range left.slots {
			v.slots[owner][offset+slot] = text
		}
		// Frozen column occurrences retain their original full FROM-frame slots.
		// Execution-only P/L normalization must not replace that public mapping.
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
		prettyFunctionSources := v.functionSources && v.presentation.pretty
		rightText := right.text
		if v.functionSources {
			joinText = "\n     " + strings.TrimSpace(joinText) + " "
			if _, nested := n.Right.(*parser.JoinReference); nested && prettyFunctionSources {
				rightText = "(" + rightText + ")"
			}
		}
		if n.On != nil {
			on, err := v.expressionSQL(ctx, catalog, n.On, viewLiteralPath{clause: viewJoinLiteral, target: ordinal})
			if err != nil {
				return viewRenderFrom{}, err
			}
			if v.functionSources && !v.presentation.pretty {
				on = "(" + on + ")"
			}
			from.text = left.text + joinText + rightText + " ON " + on
			if !prettyFunctionSources {
				from.text = "(" + from.text + ")"
			}
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
				separator := " CROSS JOIN "
				if v.functionSources {
					separator = "\n     CROSS JOIN "
				}
				from.text = left.text + separator + rightText
			} else {
				condition := "TRUE"
				if v.functionSources {
					condition = "true"
					if !v.presentation.pretty {
						condition = "(" + condition + ")"
					}
				}
				from.text = left.text + joinText + rightText + " ON " + condition
			}
			if !prettyFunctionSources {
				from.text = "(" + from.text + ")"
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
