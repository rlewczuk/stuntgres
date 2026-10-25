package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) VisibilityInquiry(kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	var oid uint32
	var err error
	if kind == functions.ImportSystemCollationsInquiry {
		oid, err = value.ObjectOID()
	} else {
		oid, err = value.OID()
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if kind == functions.ImportSystemCollationsInquiry {
		engine := scope.Engine()
		engine.Mu.Lock()
		defer engine.Mu.Unlock()
		auth, err := scope.namespaceAuthorizationLocked(engine)
		if err != nil {
			return sqltype.Value{}, err
		}
		actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return sqltype.Value{}, err
		}
		if !actor.Superuser {
			return sqltype.Value{}, SQLError("42501", "must be superuser to import system collations")
		}
		if _, exists := auth.Catalog.Descriptor(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(oid)}); !exists {
			return sqltype.Value{}, SQLError("3F000", fmt.Sprintf("schema with OID %d does not exist", oid))
		}
		return sqltype.Value{}, SQLError("0A000", "system collation import is unsupported in embedded mode")
	}
	if scope.Inquiry.visibilityCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing visibility snapshot")
	}
	in := &scope.Inquiry
	return regobj.VisibilityInquiry(in.visibilityCatalog, in.visibilityRelationPath, in.visibilityOtherPath, kind, oid, result)
}

// Engine.mu is held. The execution frontier owns catalog and path, never mutable
// session settings or an authorization view subsequently consulted by Eval.
func (e *Engine) captureVisibilitySnapshotLocked(scope *StatementScope) error {
	if scope.Inquiry.visibilityCatalog.State() != nil {
		return nil
	}
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return err
	}
	explicit, _, err := ExplicitPath(auth)
	if err != nil {
		return err
	}
	// effectivePath filters TEMP in place for conversion lookup. Give that
	// derived path its own backing store; the relation path remains immutable.
	relations := EffectivePath(auth, explicit, analyze.RelationLookup)
	otherExplicit := make([]ids.ObjectAddress, len(explicit))
	copy(otherExplicit, explicit)
	others := EffectivePath(auth, otherExplicit, analyze.ConversionLookup)
	scope.Inquiry.visibilityCatalog = auth.Catalog
	scope.Inquiry.visibilityRelationPath, scope.Inquiry.visibilityOtherPath = relations, others
	return nil
}
