package expr

// Composite is a node whose expression children are all lexical: they belong
// to the node's own query frame and are rebuilt the same way. Central engine
// switches handle such nodes with one Composite branch instead of a case per
// node type.
type Composite interface {
	Expr
	// Children returns the lexical children in evaluation order. Entries may
	// be nil (an omitted optional argument). The slice may alias the node, so
	// callers must not modify it.
	Children() []Expr
	// Rebuild returns a copy of the node with info and children, which has the
	// length of Children. It revalidates the node's provider against the new
	// children, as Reconstruct does, and leaves the receiver unchanged.
	Rebuild(info Info, children []Expr) (Expr, error)
}

// Walk visits node and then, depth first, every child that children
// enumerates. Nil nodes are skipped. The enumerator decides where lexical
// children end; Walk itself knows no node types.
func Walk(node Expr, children func(Expr, func(Expr)), visit func(Expr)) {
	if node == nil {
		return
	}
	visit(node)
	children(node, func(child Expr) { Walk(child, children, visit) })
}

// Map applies child to every non-nil child of node. Only when some child
// reports a change does it rebuild node, with InfoOf(node) as the metadata, on
// a fresh children slice. Composite nodes never carry UnknownNull or
// RegclassRef, which InfoOf does not copy. Unchanged nodes are returned as
// they are.
func Map(node Composite, child func(Expr) (Expr, bool, error)) (Expr, bool, error) {
	return mapChildren(node, nil, child)
}

// MapEvaluated is Map visiting the children in evaluation order: a node that
// lists them in written order names that order through EvaluationOrdered. The
// rebuilt children keep their positions, so only error precedence and side
// effects of child follow the evaluation order.
func MapEvaluated(node Composite, child func(Expr) (Expr, bool, error)) (Expr, bool, error) {
	var order []int
	if hook, ok := node.(EvaluationOrdered); ok {
		order = hook.EvaluationOrder()
	}
	return mapChildren(node, order, child)
}

func mapChildren(node Composite, order []int, child func(Expr) (Expr, bool, error)) (Expr, bool, error) {
	children := node.Children()
	if order == nil {
		order = make([]int, len(children))
		for i := range order {
			order[i] = i
		}
	}
	var mapped []Expr
	for _, i := range order {
		original := children[i]
		if original == nil {
			continue
		}
		next, changed, err := child(original)
		if err != nil {
			return nil, false, err
		}
		if !changed {
			continue
		}
		if mapped == nil {
			mapped = append([]Expr(nil), children...)
		}
		mapped[i] = next
	}
	if mapped == nil {
		return node, false, nil
	}
	rebuilt, err := node.Rebuild(InfoOf(node), mapped)
	if err != nil {
		return nil, false, err
	}
	return rebuilt, true, nil
}
