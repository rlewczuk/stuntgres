package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	results := scope.inquiry.searchPathResults
	if scope.inquiry.searchPathResults[0].Type() == nil || scope.foreignActionAuth != nil || scope.ruleAction.auth != nil {
		engine := scope.engine()
		engine.mu.Lock()
		defer engine.mu.Unlock()
		auth, explicit, err := scope.schemaInquiryPathLocked(engine)
		if err != nil {
			return sqltype.Value{}, err
		}
		effective := effectivePath(auth, explicit, analyze.RelationLookup)
		names := make([]string, len(effective))
		for i, path := range [2][]ids.ObjectAddress{explicit, effective} {
			for j, a := range path {
				names[j] = auth.Catalog.Get(a).Name()
			}
			results[i], err = sqltype.Names(result, names[:len(path)])
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if scope.foreignActionAuth == nil && scope.ruleAction.auth == nil {
			scope.inquiry.searchPathResults = results
		}
	}
	if implicit {
		return results[1], nil
	}
	return results[0], nil
}

func (h *statementHost) MyTempNamespace() (uint32, error) {
	scope := h.scope()
	engine := scope.engine()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	auth, err := scope.namespaceAuthorizationLocked(engine)
	if err != nil {
		return 0, err
	}
	return uint32(temporaryNamespace(auth).ObjectID), nil
}

func (h *statementHost) OtherTempNamespace(oid uint32) (bool, error) {
	scope := h.scope()
	engine := scope.engine()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	auth, err := scope.namespaceAuthorizationLocked(engine)
	if err != nil {
		return false, err
	}
	ns, _ := auth.Catalog.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(oid)})
	return ns != nil && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != auth.session.SessionToken, nil
}
