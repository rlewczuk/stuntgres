package engine

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Schema removal protects the captured namespace before constructing its
// dependency closure. Rename/owner changes deliberately do not take this AX.
func acquireSchemaDrop(s *core.StatementScope, stmt *parser.DropSchema) (ddl.SchemaDropManifest, error) {
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	var manifest ddl.SchemaDropManifest
	if err == nil {
		manifest, err = ddl.CaptureSchemaDrop(core.DDLHost{E: e, Op: s.Op}, auth, stmt)
	}
	e.Mu.Unlock()
	if err != nil {
		return ddl.SchemaDropManifest{}, err
	}
	var refScratch [8]ids.ObjectRef
	refs := refScratch[:0]
	for _, target := range manifest.Targets {
		if target.Ref != (ids.ObjectRef{}) {
			refs = append(refs, target.Ref)
		}
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	refs = slices.Compact(refs)
	for _, ref := range refs {
		if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
			return ddl.SchemaDropManifest{}, err
		}
	}
	// Validation and identity-bound construction belong to one subsequent
	// Engine.mu section. Do not validate here and discard the captured names.
	return manifest, nil
}

func skipConcurrentSchema(s *core.StatementScope, stage *core.CatalogStage, stmt *parser.CreateSchema, failure error) (bool, error) {
	var diagnostic *sqlstate.Error
	if !errors.As(failure, &diagnostic) || diagnostic.Code != "23505" {
		return false, failure
	}
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	var name string
	if err == nil {
		_, name, err = ddl.ResolveSchemaCreation(core.DDLHost{E: e, Op: s.Op}, auth, stmt)
	}
	exists := err == nil && auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name}) != (ids.ObjectAddress{})
	if exists && auth.Catalog.Epoch() != stage.Base.Epoch() {
		err = ids.ErrCatalogReplaced
	}
	e.Mu.Unlock()
	if err != nil {
		return false, err
	}
	if !exists {
		return false, failure
	}
	s.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P06", Message: fmt.Sprintf(`schema "%s" already exists, skipping`, name)})
	return true, nil
}
