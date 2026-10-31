package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func accessMethodClassUnit(t *testing.T, e *core.Engine, name string) (*catalog.AccessMethod, *catalog.OperatorFamily, *catalog.OperatorClass, *catalog.OperatorFamilySupport) {
	t.Helper()
	method := enginetest.AccessMethodUnit(t, e, name, catalog.BtreeAccessMethod, 330)
	family, class, member := operatorClassUnitDefinitions(t, e, name, 351)
	family.Method, class.Method = method.Address().ObjectID, method.Address().ObjectID
	class.Default = true
	enginetest.AccessMethodPublish(t, e, method, family, class, member)
	return method, family, class, member
}
