package parser

// commentRoutineKind maps the GRANT routine-kind lexeme that follows
// COMMENT ON to the statement's kind. The grammar shares grant_routine_kind
// with GRANT and COMMENT; only those three words reach here.
func commentRoutineKind(kind lexeme) CommentKind {
	switch kind.text {
	case "procedure":
		return CommentProcedure
	case "routine":
		return CommentRoutine
	}
	return CommentFunction
}