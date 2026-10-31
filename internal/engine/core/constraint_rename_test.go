package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestConstraintRenameDeferredIdentity(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT,CONSTRAINT old EXCLUDE(k WITH =) DEFERRABLE)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1)")
	parent := e.Catalog.State.MustRelation(roleTableRef(t, e, "t").Address)
	oid := parent.Constraints.Exclusion[0].OID
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "SET CONSTRAINTS old DEFERRED")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, s, "SAVEPOINT saved")
	modes := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
	enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	if !core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).ByConstraint[oid] || len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events) != 1 || core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events[0].Constraint != oid {
		t.Fatal("rename reset selected mode/event")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO saved")
	if !reflect.DeepEqual(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)), modes) {
		t.Fatal("rollback lost deferred obligation")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

type alterDropProvider struct {
	catalog.Provider
	callback func()
}

func (p alterDropProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(v, d); err != nil {
		return err
	}
	p.callback()
	return nil
}
