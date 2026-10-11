package parser

import "sync"

// The pool retains fixed-size, fully cleared receivers, not expanded stacks.
// Retention is GC-evictable, with no hard object-count bound or active-parse cap.
var parseScratchPool = sync.Pool{New: func() any { return new(sqlParserImpl) }}

// parseLexer lends one generated receiver to one invocation, including recursive
// INSERT replay and caller callback reentry. Lexers, options and results are never
// pooled. Grammar actions copy semantic values and allocate AST nodes/backing
// slices independently; no returned AST refers to a receiver's semantic cell.
func parseLexer(l *lexer) int {
	p := parseScratchPool.Get().(*sqlParserImpl)
	defer parseScratchPool.Put(p)
	return parseWithScratch(p, l)
}

// parseWithScratch resets after generated Parse cleanup and before pool return,
// on success, error, cancellation and panic. Clear all cells, including stale
// cells above the stack top; lexer state and independently owned ASTs survive.
func parseWithScratch(p *sqlParserImpl, l *lexer) int {
	defer func() { *p = sqlParserImpl{} }()
	return p.Parse(l)
}
