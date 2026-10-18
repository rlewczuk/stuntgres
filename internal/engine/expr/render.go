package expr

// Renderable nodes describe their own presentation. One Rendering value serves
// every engine renderer that lists nodes: the node-tree output and the
// PostgreSQL deparser. The renderers own quoting, literal transport and
// parenthesization; the node only states what it looks like.
type Renderable interface {
	// Render validates the node and returns its presentation. Its errors are
	// integrity errors of a corrupted node; a node bound by the engine renders.
	Render() (Rendering, error)
}

// Rendering is a node's presentation, read by each renderer for the parts it
// needs.
type Rendering struct {
	// Tree is the node-tree header after "node ", for example
	// "Math pg_size_pretty". An empty Tree means the node has no node-tree form.
	Tree string
	// Inputs are the node-tree children in output order; nil entries are skipped.
	Inputs []Expr
}
