package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var _ functions.SessionInquiryRuntime = (*EvalContext)(nil)

func (c *EvalContext) CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: current_schemas lacks statement authority")
	}
	results := scope.inquiry.searchPathResults
	if scope.inquiry.searchPathResults[0].Type() == nil || scope.foreignActionAuth != nil || scope.ruleActionAuth != nil {
		engine := scope.op.session.Engine()
		engine.mu.Lock()
		defer engine.mu.Unlock()
		auth, explicit, err := scope.schemaInquiryPathLocked(engine)
		if err != nil {
			return sqltype.Value{}, err
		}
		effective := engine.effectivePath(auth, explicit, relationLookup)
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
		if scope.foreignActionAuth == nil && scope.ruleActionAuth == nil {
			scope.inquiry.searchPathResults = results
		}
	}
	if implicit {
		return results[1], nil
	}
	return results[0], nil
}

func (c *EvalContext) MyTempNamespace() (uint32, error) {
	scope := c.liveScope()
	if scope == nil {
		return 0, fmt.Errorf("stuntgres: namespace inquiry lacks statement authority")
	}
	engine := scope.op.session.Engine()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	auth, err := scope.namespaceAuthorizationLocked(engine)
	if err != nil {
		return 0, err
	}
	return uint32(temporaryNamespace(auth).ObjectID), nil
}
func (c *EvalContext) OtherTempNamespace(oid uint32) (bool, error) {
	scope := c.liveScope()
	if scope == nil {
		return false, fmt.Errorf("stuntgres: namespace inquiry lacks statement authority")
	}
	engine := scope.op.session.Engine()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	auth, err := scope.namespaceAuthorizationLocked(engine)
	if err != nil {
		return false, err
	}
	ns, _ := auth.Catalog.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(oid)})
	return ns != nil && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != auth.session.token, nil
}
