package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func blockingUnitOwner(t *testing.T, e *core.Engine) (*session.Session, *lock.Ticket) {
	t.Helper()
	s := sessionStart(t, e, nil)
	transactionBegin(t, s)
	return s, s.Transactions.Ticket
}

func blockingUnitResource(e *core.Engine) lock.Resource {
	return lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999})}
}
