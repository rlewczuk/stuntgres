package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	results := scope.Inquiry.searchPathResults
	if scope.Inquiry.searchPathResults[0].Type() == nil || scope.ForeignActionAuth != nil || scope.ruleAction.Auth != nil {
		engine := scope.Engine()
		engine.Mu.Lock()
		defer engine.Mu.Unlock()
		auth, explicit, err := scope.schemaInquiryPathLocked(engine)
		if err != nil {
			return sqltype.Value{}, err
		}
		effective := EffectivePath(auth, explicit, analyze.RelationLookup)
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
		if scope.ForeignActionAuth == nil && scope.ruleAction.Auth == nil {
			scope.Inquiry.searchPathResults = results
		}
	}
	if implicit {
		return results[1], nil
	}
	return results[0], nil
}

func (h *statementHost) MyTempNamespace() (uint32, error) {
	scope := h.scope()
	engine := scope.Engine()
	engine.Mu.Lock()
	defer engine.Mu.Unlock()
	auth, err := scope.namespaceAuthorizationLocked(engine)
	if err != nil {
		return 0, err
	}
	return uint32(temporaryNamespace(auth).ObjectID), nil
}

func (h *statementHost) OtherTempNamespace(oid uint32) (bool, error) {
	scope := h.scope()
	engine := scope.Engine()
	engine.Mu.Lock()
	defer engine.Mu.Unlock()
	auth, err := scope.namespaceAuthorizationLocked(engine)
	if err != nil {
		return false, err
	}
	ns, _ := auth.Catalog.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(oid)})
	return ns != nil && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != auth.Session.SessionToken, nil
}

func (h *statementHost) ListeningChannels(result *sqltype.Descriptor) ([]sqltype.Value, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	if err := session.ValidateOperation(DomainOf(e), scope.Op); err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	var channels map[string]struct{}
	if state := PayloadOf(scope.Op.Session).notifications.State; state != nil {
		channels = state.Channels
	}
	e.Mu.Unlock()
	if len(channels) == 0 {
		return nil, nil
	}
	values := make([]sqltype.Value, len(channels))
	i := 0
	for channel := range channels {
		values[i] = sqltype.String(result, channel)
		i++
	}
	return values, nil
}

func (h *statementHost) NotificationQueueUsage() (float64, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := session.ValidateOperation(DomainOf(e), scope.Op); err != nil {
		return 0, err
	}
	return e.notificationQueueUsageLocked(), nil
}
