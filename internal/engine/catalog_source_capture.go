package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
)

// Consumes Engine.mu. These cases select capture policy, never producers: all
// eligible OIDs use the same immutable registry and common row wrapper.
func (e *Engine) openCatalogSourceLocked(scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation) (*exec.Source, error) {
	oid := source.Address().ObjectID
	// These two sources own multi-stage capture/completion, not row producers.
	if oid == pgcatalog.LocksOID {
		return e.capturePGLocksLocked(scope.op.Context(), ref, source)
	}
	if oid == 12048 {
		result, err := e.capturePGSequencesLocked(scope, ref, source)
		e.mu.Unlock()
		return result, err
	}
	produce, ok := pgcatalog.ProducerFor(oid)
	if !ok {
		e.mu.Unlock()
		if source.VirtualView {
			return nil, fmt.Errorf("stuntgres: unknown virtual catalog view")
		}
		return nil, fmt.Errorf("stuntgres: unknown catalog source")
	}
	in := pgcatalog.Input{Context: scope.op.Context(), Owner: e.catalog.owner(), Ref: ref, Source: source}
	runtime := engineCatalogRuntime{engine: e}
	unlockBefore := false
	switch oid {
	case pgcatalog.SettingsOID:
		runtime.settings = scope.op.Session.Settings
		runtime.registry = e.settings
		unlockBefore = true
	case pgcatalog.CursorsOID:
		runtime.cursors = payloadOf(scope.op.Session).cursors
		unlockBefore = true
	case 12081, 12085:
		in.Catalog = scope.inquiry.commentCatalog
		unlockBefore = true
	case 12004, 12005:
		runtime.input = scope.input
		unlockBefore = true
	case 12014, 12007, 12008:
		in.Roles = scope.inquiry.commentRoles
		unlockBefore = true
	case pgcatalog.TablePrivilegesOID, pgcatalog.DataTypePrivilegesOID, pgcatalog.InfoUserMappingsOID:
		in.Catalog, in.Roles = scope.inquiry.commentCatalog, scope.inquiry.deparseAuth.Roles
		runtime.auth, runtime.database = scope.inquiry.deparseAuth, scope.op.Session.DatabaseName
		runtime.temporary = catalogSourceTemporaryOwner(scope.inquiry.deparseAuth)
	case ids.ForeignWrapperClassID, ids.ForeignServerClassID, ids.ForeignMappingClassID, ids.ForeignTableClassID, pgcatalog.UserMappingsOID:
		auth, err := e.authorizationLocked(scope.op)
		if err != nil {
			e.mu.Unlock()
			return nil, err
		}
		runtime.auth = auth
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
	case 2396:
		in.Roles = scope.inquiry.commentRoles
	case 2224:
		runtime.sequenceBase = e.catalog.state
		if scope.owner.SequenceCatalog != nil {
			runtime.sequenceBase = scope.owner.SequenceCatalog
		}
		runtime.sequenceOwn = scope.owner.Catalog
	default:
		switch oid {
		case 2609, 3079, ids.ParameterClassID:
			in.Catalog = scope.inquiry.commentCatalog
		case 2613:
			// Page reads share the active LO query frontier. A fresh VOLATILE
			// body includes its argument effects; the outer inquiry stays frozen.
			in.Catalog = scope.largeObjectRead
			if in.Catalog.State() == nil {
				in.Catalog = scope.inquiry.commentCatalog
			}
		default:
			in.Catalog = e.privateCatalogLocked(scope.owner.Catalog)
		}
		if oid == ids.ParameterClassID {
			in.Roles = scope.inquiry.commentRoles
		} else {
			in.Roles = e.roles.read(e.catalog.owner(), in.Catalog, scope.inquiry.deparseAuth.RoleRecord)
		}
		if oid == 12002 {
			auth, err := e.authorizationLocked(scope.op)
			if err != nil {
				e.mu.Unlock()
				return nil, err
			}
			auth.Catalog = in.Catalog
			auth.Recapture(auth.Catalog)
			runtime.auth, in.Roles = auth, auth.Roles
		}
		if oid == 12006 {
			runtime.presentation = stored.Presentation{Deparse: true, Path: scope.inquiry.deparsePath, Output: scope.output, NonstandardStrings: scope.op.NonstandardStrings}
		}
	}
	in.Runtime = runtime
	if unlockBefore {
		e.mu.Unlock()
		return captureCatalogSourceRows(in, produce)
	}
	result, err := captureCatalogSourceRows(in, produce)
	e.mu.Unlock()
	return result, err
}

func explicitCatalogSource(oid ids.OID) bool { return oid == pgcatalog.LocksOID || oid == 12048 }

// Engine is the sole owner of exec.Source construction, including error paths.
func captureCatalogSourceRows(in pgcatalog.Input, produce pgcatalog.Producer) (*exec.Source, error) {
	rows, err := produce(in)
	if err != nil {
		return nil, err
	}
	return &exec.Source{Ref: in.Ref, Anonymous: rows}, nil
}
