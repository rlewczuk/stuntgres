package parser

import "runtime"

// The cache retains at most the initial GOMAXPROCS count of fully cleared
// receivers. Checkout and return never block or cap active/nested parses.
var parseScratchCache = make(chan *sqlParserImpl, runtime.GOMAXPROCS(0))

// parseLexer lends one generated receiver to one invocation, including recursive
// INSERT replay and caller callback reentry. Lexers, options and results are never
// pooled. Grammar actions copy semantic values and allocate AST nodes/backing
// slices independently; no returned AST refers to a receiver's semantic cell.
func parseLexer(l *lexer) int {
	var parser *sqlParserImpl
	select {
	case parser = <-parseScratchCache:
	default:
		parser = new(sqlParserImpl)
	}
	defer func() {
		// Clear the entire receiver on success, errors and panic, including stale
		// cells above the last stack top. Never retain query strings or ASTs in the
		// cache. Grown goyacc stacks are invocation-local and are not retained here.
		*parser = sqlParserImpl{}
		select {
		case parseScratchCache <- parser:
		default:
		}
	}()
	return parser.Parse(l)
}
