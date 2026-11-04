package tsearch

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Host is the statement a text-search call runs in. Core implements it on its
// statement host; every call reads the live statement, never a copy.
//
// Text search is entered with no engine lock held. ReacquireEngine takes
// Engine.Mu and ReleaseEngineLocked releases it; the methods named …Locked
// need it. Parser, dictionary and notice callbacks run with nothing held.
type Host[A Authority[A]] interface {
	// NestedSQL runs ts_rewrite's and ts_stat's SQL arguments.
	exec.NestedSQL
	Operation() *session.Operation
	Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment)
	// ActivateRuntime arms the statement's work admission for detached
	// lexical work.
	ActivateRuntime(ctx context.Context) context.Context
	ReacquireEngine()
	ReleaseEngineLocked()
	// PendingCatalog is the statement's private catalog journal.
	PendingCatalog() *session.Journal
	// PrivateCatalogLocked is the current catalog with changes applied.
	PrivateCatalogLocked(changes *session.Journal) catalog.Read
	// AuthorizationLocked is the operation's current authorization.
	AuthorizationLocked() (A, error)
	// TextSearchInputLocked is the statement's namespace authorization: its
	// catalog and the regconfig/regdictionary resolver over it.
	TextSearchInputLocked() (catalog.Read, regobj.TextSearchInput, error)
	// DictionaryHost compiles thesaurus inputs.
	DictionaryHost() extension.DictionaryHost
	EnterDictionaryCallback(catalog.DictionaryCallback) (sqltype.RoutineBodyToken, error)
	MarkDictionaryInitialization() access.SepgsqlObservationMark
	AbortDictionaryInitialization(access.SepgsqlObservationMark)
	// DictionaryMemos is the session's thesaurus initializations (Engine.Mu).
	DictionaryMemos() *DictionaryMemos
}

// Authority is the operation's authorization, held by value: capturing or
// recapturing it never allocates. CaptureThesaurusInput reads it as a DDL
// authority.
type Authority[A any] interface {
	ddl.Authority
	Access() access.View
	// Recaptured returns the authorization over view.
	Recaptured(view catalog.Read) A
}

// DictionaryMemos holds one physical session's successful thesaurus
// initializations, by dictionary.
type DictionaryMemos map[ids.ObjectAddress]dictionaryMemo

func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
