package stored

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func viewSourceAlias(occurrence int) string {
	return "__view_source_" + strconv.Itoa(occurrence)
}

func viewJoinAt(root plan.Node, ordinal int) *plan.JoinNode {
	var visit func(plan.Node) *plan.JoinNode
	visit = func(node plan.Node) *plan.JoinNode {
		n, ok := node.(*plan.JoinNode)
		if !ok {
			return nil
		}
		if ordinal == n.ViewOrdinal {
			return n
		}
		if found := visit(n.Left); found != nil {
			return found
		}
		return visit(n.Right)
	}
	return visit(root)
}

func viewJoinOffset(root plan.Node, target *plan.JoinNode) (int, bool) {
	if root == target {
		return 0, true
	}
	join, ok := root.(*plan.JoinNode)
	if !ok {
		return 0, false
	}
	if offset, found := viewJoinOffset(join.Left, target); found {
		return offset, true
	}
	if offset, found := viewJoinOffset(join.Right, target); found {
		return join.LeftWidth + offset, true
	}
	return 0, false
}

// Rendering state is private to one definition request. Frozen slots index the
// bound tree, while columns below follow the current emitted positional order.
// No SQL is rebound and no persistent execution layout is changed.
type QueryRenderer struct {
	*View
	query           *plan.Query
	Frame           uint32
	Slots           map[plan.Node]map[int]string
	reserved        map[string]bool
	namespace       int
	depth           int
	renderFrames    map[uint32]viewRenderFrame
	aliases         map[viewRenderAlias]string
	Presentation    Presentation
	simple          bool
	functionSources bool // Public presentation of a frame admitting FunctionFrom.
	// Summary names follow current attributes independently of literal presentation.
	CurrentNames bool
	rowTargets   map[string]string
	wholeRows    map[plan.WholeRowSource]viewRenderWholeRow
}

type viewRenderFrame struct {
	namespace int
	base      [analyze.ViewRegexpNamespace + 1]int
	seen      [analyze.ViewRegexpNamespace + 1]bool
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
	root           plan.Node
	columns        []viewRenderColumn
	slots          map[int]string
	rowTarget      string
	rowTargetSQL   string
	rowSource      bool
	namespace      int
	wholeSQL       string
}

func NewQueryRenderer(v *View) *QueryRenderer {
	r := &QueryRenderer{View: v, query: v.query, Slots: make(map[plan.Node]map[int]string), reserved: make(map[string]bool)}
	if v.syntax != nil && len(v.syntax.Locking) != 0 {
		r.rowTargets = make(map[string]string)
	}
	for _, captured := range v.composites {
		if captured.Source.Namespace == 0 {
			continue
		}
		if r.wholeRows == nil {
			r.wholeRows = make(map[plan.WholeRowSource]viewRenderWholeRow)
		}
		r.wholeRows[captured.Source] = viewRenderWholeRow{}
	}
	if len(v.namespaces) != 0 {
		r.renderFrames = make(map[uint32]viewRenderFrame)
	}
	for i, ns := range v.namespaces {
		frame, exists := r.renderFrames[ns.Frame]
		if !exists {
			frame.namespace = i
		}
		if !frame.seen[ns.Node] || ns.Ordinal < frame.base[ns.Node] {
			frame.base[ns.Node], frame.seen[ns.Node] = ns.Ordinal, true
		}
		r.renderFrames[ns.Frame] = frame
		for _, c := range ns.Columns {
			r.reserved[c.Name] = true
		}
		for _, q := range ns.Qualifiers {
			r.reserved[q.Name] = true
			for _, c := range q.Columns {
				r.reserved[c.Name] = true
			}
		}
	}
	return r
}

func (v *QueryRenderer) freshLabel(ns analyze.ViewNamespace, attribute int32, physical bool) string {
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

func (v *QueryRenderer) SlotSQL(ctx context.Context, catalog catalog.Read, root plan.Node, slot int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if text, ok := v.Slots[root][slot]; ok {
		return text, nil
	}
	return "", fmt.Errorf("stuntgres: missing view column namespace slot")
}

// Equivalent sibling queries need identical emitted RTE names for SQL99
// replay. Normalize generated node IDs within the lexical frame, cache names
// across siblings, and separate depths so correlations cannot be shadowed.
func (v *QueryRenderer) renderAliasKey(ns analyze.ViewNamespace) viewRenderAlias {
	return viewRenderAlias{depth: v.depth, node: ns.Node, ordinal: ns.Ordinal - v.renderFrames[ns.Frame].base[ns.Node]}
}
func (v *QueryRenderer) namespaceAlias(ns analyze.ViewNamespace) string {
	key := v.renderAliasKey(ns)
	base := viewSourceAlias(key.ordinal)
	switch ns.Node {
	case analyze.ViewJoinNamespace:
		base = "__view_using_" + strconv.Itoa(key.ordinal)
	case analyze.ViewValuesNamespace:
		base = "__view_values_" + strconv.Itoa(key.ordinal)
	case analyze.ViewDerivedNamespace:
		base = "__view_derived_" + strconv.Itoa(key.ordinal)
	case analyze.ViewFunctionNamespace, analyze.ViewRegexpNamespace:
		base = "__view_function_" + strconv.Itoa(key.ordinal)
	}
	if key.depth > 1 {
		base += "_query_" + strconv.Itoa(key.depth-1)
	}
	return v.allocateAlias(key, base)
}
func (v *QueryRenderer) allocateAlias(key viewRenderAlias, base string) string {
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
func (v *QueryRenderer) aliasFrom(from viewRenderFrom, ns analyze.ViewNamespace, name string) viewRenderFrom {
	labels := make(map[int]string, len(ns.Columns))
	for _, c := range ns.Columns {
		labels[c.Slot] = c.Name
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
	case *plan.WorktableScanNode:
		if scan.CTE.Search != nil || scan.CTE.Cycle != nil {
			aliasWidth = scan.CTE.UserWidth
		}
	case *plan.DerivedNode:
		if scan.CTE != nil && (scan.CTE.Search != nil || scan.CTE.Cycle != nil) {
			aliasWidth = scan.CTE.UserWidth
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
			aliases[i] = catalog.QuoteIdentifier(c.name)
		}
		if c.slot >= 0 {
			from.slots[c.slot] = catalog.QuoteIdentifier(name) + "." + catalog.QuoteIdentifier(c.name)
		}
	}
	text := "(" + from.text + ")"
	if from.relation != "" {
		text = from.relation
	}
	from.text = text + " AS " + catalog.QuoteIdentifier(name)
	if len(aliases) > 0 {
		from.text += "(" + strings.Join(aliases, ", ") + ")"
	}
	from.relation = ""
	from.wholeSQL = catalog.QuoteIdentifier(name)
	v.Slots[from.root] = from.slots
	return from
}

func viewHasFunctionSource(item parser.FromItem) bool {
	switch n := item.(type) {
	case *parser.JSONTable:
		return true
	case *parser.FunctionTable:
		for _, call := range n.Calls {
			if analyze.MixedFunctionSourceName(call.Name.Parts[len(call.Name.Parts)-1].Text) {
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
func (v *QueryRenderer) functionSourceAlias(from viewRenderFrom, name string, columns []parser.Identifier) viewRenderFrom {
	identifier := v.Presentation.identifier
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
	v.Slots[from.root] = from.slots
	return from
}

func (v *QueryRenderer) fromSQL(ctx context.Context, catalog catalog.Read, item parser.FromItem) (viewRenderFrom, error) {
	from, err := v.renderFromSQL(ctx, catalog, item)
	if err == nil && v.rowTargets != nil && from.rowSource {
		// Publish only the finalized visible target of this occurrence. An
		// enclosing alias must not overwrite or delete a sibling's base name.
		v.rowTargets[from.rowTarget] = from.rowTargetSQL
	}
	if err == nil && v.wholeRows != nil && from.wholeSQL != "" {
		namespace := v.namespaces[from.namespace-1]
		for position, qualifier := range namespace.Qualifiers {
			key := plan.WholeRowSource{Namespace: from.namespace, Qualifier: position, Frame: namespace.Frame}
			if _, demanded := v.wholeRows[key]; !demanded {
				continue
			}
			names := make([]string, len(qualifier.Columns))
			for i, column := range qualifier.Columns {
				found := false
				for _, rendered := range from.columns {
					if rendered.slot == column.Slot {
						names[i] = rendered.name
						found = true
						break
					}
				}
				if !found {
					return viewRenderFrom{}, fmt.Errorf("stuntgres: missing rendered whole-row field")
				}
			}
			v.wholeRows[plan.WholeRowSource{Namespace: from.namespace, Qualifier: position, Frame: namespace.Frame}] = viewRenderWholeRow{sql: from.wholeSQL + ".*", names: names}
		}
	}
	return from, err
}

func (v *QueryRenderer) renderFromSQL(ctx context.Context, catalogView catalog.Read, item parser.FromItem) (result viewRenderFrom, failure error) {
	if err := ctx.Err(); err != nil {
		return viewRenderFrom{}, err
	}
	// Nested expressions can render in a different order from analysis.
	// Namespace entries retain their owning query frame and bound node ordinal.
	for v.namespace < len(v.namespaces) && v.namespaces[v.namespace].Frame != v.Frame {
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
	case *parser.JSONTable:
		return v.renderJSONTableSQL(ctx, catalogView, n, ns)
	case *parser.FunctionTable:
		return v.renderFunctionTableSQL(ctx, catalogView, n, ns)
	case *parser.FunctionFrom:
		return v.renderFunctionTableSQL(ctx, catalogView, &parser.FunctionTable{Calls: n.Calls, Ordinality: n.Ordinality, Lateral: n.Lateral, RowsFrom: len(n.Calls) > 1, Pos: n.Pos}, ns)
	case *parser.DerivedQuery:
		root := viewDerivedAt(analyze.ViewFromRoot(v.query.Root), n.Query)
		if root == nil {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: missing frozen derived query")
		}
		text, err := v.SelectQuerySQL(ctx, catalogView, n.Query, v.queries[n.Query])
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
		ordinal := ns.Ordinal
		text, err := v.valuesSQL(ctx, catalogView, n, ordinal)
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
		if root := ViewCTETable(analyze.ViewFromRoot(v.query.Root), n); root != nil {
			name := catalog.QuoteIdentifier(n.Name.Parts[0].Text)
			from := viewRenderFrom{text: name, relation: name, root: root}
			for i, column := range root.Schema() {
				from.columns = append(from.columns, viewRenderColumn{name: column.Name, slot: i})
			}
			aliased := v.aliasFrom(from, ns, v.namespaceAlias(ns))
			aliased.relation = name
			return aliased, nil
		}
		occurrence := ns.Ordinal
		if occurrence < 0 || occurrence >= len(v.sources) {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: invalid view source occurrence")
		}
		leaf := v.sources[occurrence]
		d, ok := catalogView.Relation(leaf.Ref.Address)
		if !ok {
			return viewRenderFrom{}, ids.ErrCatalogReplaced
		}
		root := viewSourceAt(analyze.ViewFromRoot(v.query.Root), occurrence)
		if root == nil {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: missing view source node")
		}
		frozen := make(map[int32]analyze.ViewNamespaceColumn, len(ns.Columns))
		for _, c := range ns.Columns {
			frozen[c.Attribute] = c
		}
		if v.simple {
			name := RelationDiagnosticNameAt(catalogView, d, v.Presentation.Path)
			if v.Presentation.Output.QuoteAllIdentifiers {
				name = v.Presentation.identifier(d.Name())
				if !relationVisibleAt(catalogView, d, v.Presentation.Path) {
					name = v.Presentation.identifier(catalogView.Get(d.Namespace()).Name()) + "." + name
				}
			}
			if leaf.Only {
				name = "ONLY " + name
			}
			from := viewRenderFrom{text: name, root: root, slots: make(map[int]string)}
			from.wholeSQL = catalog.Identifier(d.Name())
			if v.rowTargets != nil {
				key := n.Name.Parts[len(n.Name.Parts)-1].Text
				from.rowTarget, from.rowSource = key, true
				from.rowTargetSQL = v.Presentation.identifier(d.Name())
			}
			for i, attribute := range d.Attributes {
				if c, ok := frozen[attribute]; ok {
					from.slots[c.Slot] = v.Presentation.identifier(d.Columns[i].Name)
					from.columns = append(from.columns, viewRenderColumn{name: d.Columns[i].Name, slot: c.Slot})
				}
			}
			v.Slots[root] = from.slots
			return from, nil
		}
		from := viewRenderFrom{root: root}
		from.rowTarget, from.rowSource = n.Name.Parts[len(n.Name.Parts)-1].Text, true
		if v.functionSources {
			if v.Presentation.Output.QuoteAllIdentifiers {
				from.relation = v.Presentation.identifier(d.Name())
				if !relationVisibleAt(catalogView, d, v.Presentation.Path) {
					from.relation = v.Presentation.identifier(catalogView.Get(d.Namespace()).Name()) + "." + from.relation
				}
			} else {
				from.relation = RelationDiagnosticNameAt(catalogView, d, v.Presentation.Path)
			}
			if leaf.Only {
				from.relation = "ONLY " + from.relation
			}
			from.text = from.relation
			from.slots = make(map[int]string, len(frozen))
			qualifier := v.Presentation.identifier(d.Name())
			from.rowTargetSQL = qualifier
			for i, attribute := range d.Attributes {
				column := viewRenderColumn{name: d.Columns[i].Name, slot: -1}
				if captured, ok := frozen[attribute]; ok {
					column.slot = captured.Slot
					from.slots[column.slot] = qualifier + "." + v.Presentation.identifier(column.name)
				}
				from.columns = append(from.columns, column)
			}
			v.Slots[root] = from.slots
			return from, nil
		}
		from.relation = catalog.QuoteIdentifier(catalogView.Get(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
		if v.Presentation.Deparse && v.Presentation.Pretty && !v.Presentation.Output.QuoteAllIdentifiers {
			from.relation = RelationDiagnosticNameAt(catalogView, d, v.Presentation.Path)
		}
		if leaf.Only {
			from.relation = "ONLY " + from.relation
		}
		for _, attribute := range d.Attributes {
			column := viewRenderColumn{slot: -1}
			if c, ok := frozen[attribute]; ok {
				column.name, column.slot = c.Name, c.Slot
			} else {
				column.name = v.freshLabel(ns, attribute, true)
			}
			from.columns = append(from.columns, column)
		}
		from.text = from.relation
		alias := v.namespaceAlias(ns)
		aliased := v.aliasFrom(from, ns, alias)
		if v.rowTargets != nil {
			aliased.rowTargetSQL = catalog.QuoteIdentifier(alias)
		}
		// A surrounding table alias replaces this default, not nests it.
		aliased.relation = from.relation
		return aliased, nil
	case *parser.AliasReference:
		from, err := v.renderFromSQL(ctx, catalogView, n.Input)
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
			from.rowTargetSQL = catalog.QuoteIdentifier(n.Name.Text)
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
		ordinal := ns.Ordinal
		bound := viewJoinAt(analyze.ViewFromRoot(v.query.Root), ordinal)
		if bound == nil {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: missing view join occurrence")
		}
		usingAlias := ""
		if n.UsingAlias != nil {
			usingAlias = n.UsingAlias.Text
		} else {
			for i := range ns.Keys {
				if ns.Columns[i].Slot >= bound.LeftWidth+bound.RightWidth {
					// Reserve before rendering descendants. This qualifies the
					// exact merged usage without hiding either physical input.
					usingAlias = v.namespaceAlias(ns)
					break
				}
			}
		}
		left, err := v.fromSQL(ctx, catalogView, n.Left)
		if err != nil {
			return viewRenderFrom{}, err
		}
		// Function arguments reference the owner's full FROM frame before the
		// right source exists. Publish only finalized legal predecessor slots.
		owner := analyze.ViewFromRoot(v.query.Root)
		offset, found := viewJoinOffset(owner, bound)
		if !found {
			return viewRenderFrom{}, fmt.Errorf("stuntgres: missing view join predecessor frame")
		}
		if v.Slots[owner] == nil {
			v.Slots[owner] = make(map[int]string, len(owner.Schema()))
		}
		for slot, text := range left.slots {
			v.Slots[owner][offset+slot] = text
		}
		// Frozen column occurrences retain their original full FROM-frame slots.
		// Execution-only P/L normalization must not replace that public mapping.
		right, err := v.fromSQL(ctx, catalogView, n.Right)
		if err != nil {
			return viewRenderFrom{}, err
		}
		from := viewRenderFrom{root: bound, slots: make(map[int]string, len(left.slots)+len(right.slots)+len(bound.Merges))}
		for slot, text := range left.slots {
			from.slots[slot] = text
		}
		for slot, text := range right.slots {
			from.slots[slot+bound.LeftWidth] = text
		}
		for i := range right.columns {
			if right.columns[i].slot >= 0 {
				right.columns[i].slot += bound.LeftWidth
			}
		}
		v.Slots[bound] = from.slots
		joinText := " JOIN "
		switch bound.Kind {
		case parser.JoinLeft:
			joinText = " LEFT JOIN "
		case parser.JoinRight:
			joinText = " RIGHT JOIN "
		case parser.JoinFull:
			joinText = " FULL JOIN "
		}
		prettyFunctionSources := v.functionSources && v.Presentation.Pretty
		rightText := right.text
		if v.functionSources {
			joinText = "\n     " + strings.TrimSpace(joinText) + " "
			if _, nested := n.Right.(*parser.JoinReference); nested && prettyFunctionSources {
				rightText = "(" + rightText + ")"
			}
		}
		if n.On != nil {
			on, err := v.ExpressionSQL(ctx, catalogView, n.On, analyze.ViewLiteralPath{Clause: analyze.ViewJoinLiteral, Target: ordinal})
			if err != nil {
				return viewRenderFrom{}, err
			}
			if v.functionSources && !v.Presentation.Pretty {
				on = "(" + on + ")"
			}
			from.text = left.text + joinText + rightText + " ON " + on
			if !prettyFunctionSources {
				from.text = "(" + from.text + ")"
			}
		} else if len(ns.Keys) > 0 {
			keys := make([]string, len(ns.Keys))
			for i, key := range ns.Keys {
				keys[i] = catalog.QuoteIdentifier(key)
				c := ns.Columns[i]
				from.columns = append(from.columns, viewRenderColumn{name: key, slot: c.Slot})
				// A synthetic merged value is recreated by USING's implicit
				// common-type binder, never an unsupported explicit cast.
				if c.Slot >= bound.LeftWidth+bound.RightWidth {
					from.slots[c.Slot] = catalog.QuoteIdentifier(usingAlias) + "." + catalog.QuoteIdentifier(key)
				}
			}
			from.text = "(" + left.text + joinText + right.text + " USING (" + strings.Join(keys, ", ") + ")"
			if usingAlias != "" {
				from.text += " AS " + catalog.QuoteIdentifier(usingAlias)
			}
			from.text += ")"
		} else {
			if bound.Kind == parser.JoinInner {
				separator := " CROSS JOIN "
				if v.functionSources {
					separator = "\n     CROSS JOIN "
				}
				from.text = left.text + separator + rightText
			} else {
				condition := "TRUE"
				if v.functionSources {
					condition = "true"
					if !v.Presentation.Pretty {
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
				for _, name := range ns.Keys {
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

func viewSourceAt(node plan.Node, occurrence int) *plan.SourceNode {
	switch n := node.(type) {
	case *plan.SourceNode:
		if n.Occurrence == occurrence {
			return n
		}
	case *plan.JoinNode:
		if found := viewSourceAt(n.Left, occurrence); found != nil {
			return found
		}
		return viewSourceAt(n.Right, occurrence)
	}
	return nil
}

func viewDerivedAt(node plan.Node, syntax *parser.Select) *plan.DerivedNode {
	if node == nil {
		return nil
	}
	if derived, ok := node.(*plan.DerivedNode); ok {
		if project, ok := derived.Query.Root.(*plan.ProjectNode); ok && project.Syntax == syntax {
			return derived
		}
	}
	left, right := plan.QueryInputs(node)
	if found := viewDerivedAt(left, syntax); found != nil {
		return found
	}
	return viewDerivedAt(right, syntax)
}
