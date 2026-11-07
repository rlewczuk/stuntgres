package nativetrig

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/tsearch"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Host is the statement a native trigger body runs in. Core implements it on
// its statement host; every call reads the live statement, never a copy.
//
// Bodies are entered with no engine lock held, inside the routine environment
// core entered for the invocation. ReacquireEngine takes Engine.Mu and
// ReleaseEngineLocked releases it; the methods named …Locked need it. Text
// search, sequence, large-object, identity and nested SQL calls run with
// nothing held.
type Host[A tsearch.Authority[A]] interface {
	// Host is the statement the tsvector trigger's text-search capture
	// walks and accepts over; it also gives the operation, the I/O
	// environments, work admission, the Engine.Mu pair and the private
	// catalog.
	tsearch.Host[A]
	// Context is the statement's execution context.
	Context() context.Context
	// NamespaceAuthorizationLocked is the statement's namespace
	// authorization over its private catalog.
	NamespaceAuthorizationLocked() (A, error)
	// TextSearchInput resolves regconfig text in auth.
	TextSearchInput(auth A) regobj.TextSearchInput
	// NextvalText is the legacy nextval(text) of autoinc.
	NextvalText(text string) (int64, error)
	// LargeObject runs lo_manage's direct unlink.
	LargeObject(request functions.LargeObjectRequest) (functions.LargeObjectResult, error)
	// CurrentUserValue is current_user as result.
	CurrentUserValue(result *sqltype.Descriptor) (sqltype.Value, error)
	// QueueNotification validates and appends one tcn event to the
	// statement's single notification queue, returning its 22023 rejection.
	QueueNotification(channel, payload string) error
	// SetRoutineAtomic sets the atomic invocation barrier and returns the
	// previous one, which the caller restores.
	SetRoutineAtomic(atomic bool) (previous bool)
	// PrepareTriggerSQL and ExecuteTriggerSQL admit and analyze, then run,
	// one generated refint statement with typed closed parameters over read,
	// as a nested routine command in the outer journal. The count is the
	// command's row count, or whether it returned a row.
	PrepareTriggerSQL(statement parser.Statement, inputs []sqltype.Usage, read catalog.Read) error
	ExecuteTriggerSQL(statement parser.Statement, inputs []sqltype.Usage, read catalog.Read, parameters []sqltype.Value) (int64, error)
}

func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
