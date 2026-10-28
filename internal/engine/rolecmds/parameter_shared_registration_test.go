package rolecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestParameterACLSharedRegistration(t *testing.T) {
	e, seed := engine.New(), engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE ROLE registration_reader LOGIN")
	// This is the actual detached template-built runtime, not a synthetic root.
	// Its construction is complete but final registration has not happened.
	target := databaseCreateUnitTarget(t, e, seed, "shared_parameter_later")
	before := target.Catalog.State
	key := ids.NameKey{ClassID: ids.ParameterClassID, Name: "registration.value"}
	enginetest.SessionSQL(t, s, "GRANT SET ON PARAMETER registration.value TO registration_reader")
	address := e.Catalog.State.NameAddress(key)
	parameter := e.Catalog.State.Get(address)
	if parameter == nil || before.NameAddress(key) != (ids.ObjectAddress{}) { t.Fatal("missing distinguishing grant during construction") }
	if _, err := databaseCreateUnitPublish(e, context.Background(), "shared_parameter_later", target); err != nil { t.Fatal(err) }
	if target.Catalog.State.Get(address) != parameter || target.Catalog.State.NameAddress(key) != address || target.Catalog.State.ParameterIncarnation() != e.Catalog.State.ParameterIncarnation() { t.Fatal("final registration adopted stale template ACL authority") }
	if target.Catalog.State == before || before.Get(address) != nil || before.NameAddress(key) != (ids.ObjectAddress{}) { t.Fatal("registration mutated detached predecessor backing") }
	if _, ok := parameter.(*catalog.Parameter); !ok { t.Fatal("not canonical parameter authority") }
}
