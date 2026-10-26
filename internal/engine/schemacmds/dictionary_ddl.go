package schemacmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

var _ extension.DictionaryHost = SchemaHost{}

// StageDictionaryDetached captures semantic inputs under Engine.Mu, compiles
// shipped resources with it released, then stages against current authority.
// The caller holds no engine mutex; the returned candidate is not published.
func StageDictionaryDetached(e *core.Engine, op *session.Operation, stmt parser.Statement) (*core.CatalogStage, error) {
	e.Mu.Lock()
	auth, err := schemaAuthorizationLocked(e, op)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	preparation, err := extension.PrepareDictionary(SchemaHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, stmt)
	e.Mu.Unlock()
	if err == nil {
		err = preparation.Compile(op.Context())
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return CompleteDictionaryLocked(e, op, stmt, stage, &preparation)
}

// CompleteDictionaryLocked fences the compiled semantic inputs and applies the
// prepared dictionary operation with fresh authority. Engine.Mu must be held.
// It discards a failed or superseded candidate; success transfers its ownership
// to the caller for ordinary admission and publication.
func CompleteDictionaryLocked(e *core.Engine, op *session.Operation, stmt parser.Statement, stage *core.CatalogStage, preparation *extension.DictionaryPreparation) (*core.CatalogStage, error) {
	current, err := schemaAuthorizationLocked(e, op)
	if err == nil && (current.Catalog.Domain() != stage.Base.Domain() || current.Catalog.Epoch() != stage.Base.Epoch()) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil && current.Catalog.State() != stage.Base.State() {
		// Preparation may own the session's first TEMP namespace pair.
		// Retain those exact candidate identities across an unrelated catalog
		// publication before checking the captured semantic inputs.
		prepared := stage
		stage = e.BeginCatalogStage(current.Catalog)
		for _, descriptor := range prepared.Descriptors() {
			if err = stage.Create(descriptor); err != nil {
				break
			}
		}
		e.DiscardCatalogStage(prepared)
	}
	if err == nil && !preparation.Current(e.StageRead(stage)) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		stage.AuthorizeLocked(current, op.Session)
		// Reuse candidate-only namespace authority, not the session's as-yet
		// unpublished TEMP pointer. Otherwise CREATE would allocate a second
		// pair while applying the already prepared operation.
		current.Recapture(e.StageRead(stage))
		if namespace, ok := preparation.Namespace.(*catalog.Namespace); ok &&
			!namespace.TemporaryToast && namespace.Temporary == op.Session.SessionToken {
			current.StagedTemporary = namespace.Address()
		}
		host := preparation.Host(SchemaHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}})
		switch s := stmt.(type) {
		case *parser.CreateDictionary:
			err = extension.StageCreateDictionary(host, current, s)
		case *parser.AlterDictionary:
			err = extension.StageAlterDictionary(host, current, s)
		}
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return nil, err
	}
	return stage, nil
}
