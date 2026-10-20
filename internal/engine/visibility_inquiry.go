package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func namedObjectVisible(view catalog.Read, path []ids.ObjectAddress, d catalog.Descriptor) bool {
	return namedObjectVisibleAt(view, nil, path, d)
}

func namedObjectVisibleAt(view catalog.Read, reader definitionReader, path []ids.ObjectAddress, d catalog.Descriptor) bool {
	for _, namespace := range path {
		key := catalog.DescriptorNameKey(d)
		key.Namespace = namespace
		if found := definitionLookupAt(view, reader, key); found != (ids.ObjectAddress{}) {
			return found == d.Address()
		}
	}
	return false
}
func (c *EvalContext) VisibilityInquiry(kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing statement authority")
	}
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
		engine := scope.op.session.Engine()
		engine.mu.Lock()
		defer engine.mu.Unlock()
		auth, err := scope.namespaceAuthorizationLocked(engine)
		if err != nil {
			return sqltype.Value{}, err
		}
		actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return sqltype.Value{}, err
		}
		if !actor.Superuser {
			return sqltype.Value{}, sqlError("42501", "must be superuser to import system collations")
		}
		if _, exists := auth.Catalog.Descriptor(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(oid)}); !exists {
			return sqltype.Value{}, sqlError("3F000", fmt.Sprintf("schema with OID %d does not exist", oid))
		}
		return sqltype.Value{}, sqlError("0A000", "system collation import is unsupported in embedded mode")
	}
	if scope.visibilityCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing visibility snapshot")
	}
	d, exists := scope.visibilityCatalog.Descriptor(ids.ObjectAddress{ClassID: kind.Class(), ObjectID: ids.OID(oid)})
	if !exists {
		if kind == functions.CollationVersionInquiry || kind == functions.DatabaseCollationVersionInquiry {
			objectKind := "collation"
			if kind == functions.DatabaseCollationVersionInquiry {
				objectKind = "database"
			}
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("%s with OID %d does not exist", objectKind, oid))
		}
		return sqltype.Null(result), nil
	}
	if kind == functions.CollationVersionInquiry || kind == functions.DatabaseCollationVersionInquiry {
		var version *string
		if kind == functions.DatabaseCollationVersionInquiry {
			version, err = DatabaseCollationActualVersion()
		} else {
			version, err = CollationActualVersion(d.(*catalog.Collation))
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		if version == nil {
			return sqltype.Null(result), nil
		}
		return sqltype.String(result, *version), nil
	}
	path := scope.visibilityRelationPath
	if kind == functions.CollationVisibility || kind == functions.ConversionVisibility {
		path = scope.visibilityOtherPath
	}
	visible := false
	if kind == functions.CollationVisibility {
		visible = visibleCollation(scope.visibilityCatalog, path, d)
	} else {
		visible = namedObjectVisible(scope.visibilityCatalog, path, d)
	}
	return sqltype.Boolean(result, visible), nil
}

// Engine.mu is held. The execution frontier owns catalog and path, never mutable
// session settings or an authorization view subsequently consulted by Eval.
func (e *Engine) captureVisibilitySnapshotLocked(scope *StatementScope) error {
	if scope.visibilityCatalog.State() != nil {
		return nil
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return err
	}
	explicit, _, err := e.explicitPath(auth)
	if err != nil {
		return err
	}
	// effectivePath filters TEMP in place for conversion lookup. Give that
	// derived path its own backing store; the relation path remains immutable.
	relations := e.effectivePath(auth, explicit, relationLookup)
	otherExplicit := make([]ids.ObjectAddress, len(explicit))
	copy(otherExplicit, explicit)
	others := e.effectivePath(auth, otherExplicit, conversionLookup)
	scope.visibilityCatalog = auth.Catalog
	scope.visibilityRelationPath, scope.visibilityOtherPath = relations, others
	return nil
}
