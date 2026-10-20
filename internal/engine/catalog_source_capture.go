package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

// Consumes Engine.mu. These cases select capture policy, never producers: all
// eligible OIDs use the same immutable registry and common row wrapper.
func (e *Engine) openCatalogSourceLocked(scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation) (*querySource, error) {
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
	in := catalogSourceInput{Context: scope.op.Context(), Owner: e.catalog.owner(), Ref: ref, Source: source}
	runtime := engineCatalogRuntime{engine: e}
	unlockBefore := false
	switch oid {
	case pgcatalog.SettingsOID:
		runtime.settings = scope.op.session.settings
		unlockBefore = true
	case pgcatalog.CursorsOID:
		runtime.cursors = scope.op.session.cursors
		unlockBefore = true
	case 12081, 12085:
		in.Catalog = scope.commentCatalog
		unlockBefore = true
	case 12004, 12005:
		runtime.input = scope.input
		unlockBefore = true
	case 12014, 12007, 12008:
		in.Roles = scope.commentRoles
		unlockBefore = true
	case pgcatalog.TablePrivilegesOID, pgcatalog.DataTypePrivilegesOID:
		in.Catalog, in.Roles = scope.commentCatalog, scope.deparseAuth.Roles
		runtime.auth, runtime.database = scope.deparseAuth, scope.op.session.databaseName
		runtime.temporary = catalogSourceTemporaryOwner(scope.deparseAuth)
	case ids.ForeignWrapperClassID, ids.ForeignServerClassID, ids.ForeignMappingClassID, ids.ForeignTableClassID, pgcatalog.UserMappingsOID:
		auth, err := e.authorizationLocked(scope.op)
		if err != nil {
			e.mu.Unlock()
			return nil, err
		}
		runtime.auth = auth
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
	case 2396:
		in.Roles = scope.commentRoles
	case 2224:
		runtime.sequenceBase = e.catalog.state
		if scope.owner.sequenceCatalog != nil {
			runtime.sequenceBase = scope.owner.sequenceCatalog
		}
		runtime.sequenceOwn = scope.owner.catalog
	default:
		switch oid {
		case 2609, 3079, ids.ParameterClassID:
			in.Catalog = scope.commentCatalog
		default:
			in.Catalog = e.privateCatalogLocked(scope.owner.catalog)
		}
		if oid == ids.ParameterClassID {
			in.Roles = scope.commentRoles
		} else {
			in.Roles = e.roles.read(e.catalog.owner(), in.Catalog, scope.deparseAuth.RoleRecord)
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
			runtime.presentation = viewPresentation{deparse: true, path: scope.deparsePath, output: scope.output, nonstandardStrings: scope.op.nonstandardStrings}
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

// Engine is the sole owner of querySource construction, including error paths.
func captureCatalogSourceRows(in catalogSourceInput, produce catalogSourceProducer) (*querySource, error) {
	rows, err := produce(in)
	if err != nil {
		return nil, err
	}
	return &querySource{ref: in.Ref, anonymous: rows}, nil
}
