package exec

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

// NestedSQL is the statement's SPI analogue: SQL text a function runs inside
// the calling statement, parsed, analyzed and executed by the engine's own
// binder and executor over the caller's private catalog and data, with no
// parameters. The engine implements it, because admission, binder
// construction and dispatch re-entry stay in core; callers own their
// statement loop and SPI policy (which commands they accept, what they
// consume). Every method is called without Engine.mu held.
//
// A caller enters one nested level, parses, then prepares or executes each
// statement:
//
//	if err := spi.EnterNestedSQL(); err != nil {
//		return err
//	}
//	defer spi.LeaveNestedSQL()
//	read, statements, err := spi.ParseNestedSQL(ctx, text)
//	...
//	err = spi.ExecuteNestedSQL(statement, read, request, consumer)
type NestedSQL interface {
	// EnterNestedSQL enters one nested routine level and fails past the
	// stack depth limit. Each nil return pairs with one LeaveNestedSQL.
	EnterNestedSQL() error
	LeaveNestedSQL()
	// ParseNestedSQL captures the private catalog the nested statements
	// read, then parses text with the session's standard_conforming_strings.
	ParseNestedSQL(ctx context.Context, text string) (catalog.Read, []parser.Statement, error)
	// PrepareNestedSQL admits and analyzes statement without executing it.
	PrepareNestedSQL(statement parser.Statement, read catalog.Read) error
	// ExecuteNestedSQL runs statement as one nested cursor command: fresh
	// lexical command scratch, consumer.NestedCommand, relation admission,
	// analysis, consumer.NestedAnalyzed, execution, consumer.NestedRows,
	// then the command's other row sets close and the scratch is restored.
	ExecuteNestedSQL(statement parser.Statement, read catalog.Read, request NestedRequest, consumer NestedConsumer) error
	// NestedCursorStatement reports whether statement returns tuples, so
	// that a cursor can open it.
	NestedCursorStatement(statement parser.Statement) bool
	// NestedReadOnlyViolation is the command tag a non-volatile caller
	// rejects in statement, or "".
	NestedReadOnlyViolation(statement parser.Statement) string
	// NestedCommandTag is statement's command tag in SPI diagnostics.
	NestedCommandTag(statement parser.Statement) string
}

// NestedRequest is the SPI policy of one ExecuteNestedSQL command.
type NestedRequest struct {
	// Text is the nested SQL text. With Diagnose, admission, analysis and
	// execution failures gain it as RewriteSQLDiagnostic does.
	Text     string
	Diagnose bool
	// Caller names the function in the internal error for a non-empty
	// parameter frame ("<Caller> SQL parameter frame is not empty").
	Caller string
	// ReadOnly classifies the executable binding as read-only SPI before
	// execution.
	ReadOnly bool
	// BorrowSource lets nested sources borrow the outer data view's roots
	// instead of keeping the outer view and its row-security references.
	BorrowSource bool
	// FreshCommand uses ordinary fresh command admission rather than reinstalling
	// the outer View/RLS roots. Its caller supplies the lexical routine policy.
	// It is mutually exclusive with BorrowSource.
	FreshCommand bool
	// CloseFailed closes the rows and row sets of a failed execution.
	CloseFailed bool
}

// NestedConsumer is the caller's side of one ExecuteNestedSQL command. Its
// methods run with nothing held, inside the command's lexical scratch.
type NestedConsumer interface {
	// NestedCommand runs before admission. stop ends the command without
	// executing it and without an error.
	NestedCommand(statement parser.Statement) (stop bool, err error)
	// NestedAnalyzed runs after analysis, before execution.
	NestedAnalyzed(statement parser.Statement) error
	// NestedRows receives the executed command's rows, nil when it produced
	// none. The consumer owns rows and closes them.
	NestedRows(statement parser.Statement, rows RowResult) error
}
