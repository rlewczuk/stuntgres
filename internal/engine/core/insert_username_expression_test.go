package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestInsertUsernameScalarEpochBridge(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE TEMP TABLE iu_bridge_consumer(id integer)")
	namespace := e.CatalogRead().MustNamespace(owner.TemporaryNamespace)
	enginetest.SessionSQL(t, owner, "CREATE EXTENSION insert_username SCHEMA "+catalog.QuoteIdentifier(namespace.Name()))
	before := e.CatalogRead()
	installed := before.MustExtension(before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "insert_username"}))
	provider := before.MustProcedure(installed.Members[0])
	consumer := before.MustRelation(before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace.Address(), Name: "iu_bridge_consumer"}))
	trigger, _ := before.LookupType(2279)
	n := plan.InsertUsernameExpr{
		Info: expr.Info{Usage: trigger.Usage(), Property: expr.Properties{Volatility: expr.Volatile}, RoutineReference: provider},
		Ref: before.Owner().Domain.Ref(before.Epoch(), provider.Address()),
		Namespace: namespace.Name(),
	}
	// Advance an isolated catalog generation while retaining the authentic
	// temporary namespace, provider and consumer, as in retained TEMP units.
	e.Mu.Lock()
	advanced := catalog.CloneState(before.State())
	advanced.SetEpoch(before.Epoch() + 1)
	e.Catalog.PublishLocked(advanced)
	e.Mu.Unlock()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	current := auth.Catalog
	if current.Epoch() <= n.Ref.Epoch || current.Get(consumer.Address()) != consumer || current.Get(provider.Address()) != provider {
		t.Fatal("fixture did not retain genuine objects across catalog generations")
	}
	if !stored.ReferenceEpochValid(current, consumer, n.Ref) || stored.ReferenceEpochValid(current, nil, n.Ref) {
		t.Fatal("fixture lacks independently admissible contextual epoch authority")
	}
	evaluation := exec.Context{
		EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: current},
		EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineConsumer: consumer},
	}
	original := n
	_, err = n.Eval(&evaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != "insert_username: not fired by trigger manager" {
		t.Fatal("lawful stored-consumer bridge did not reach authentic helper", err)
	}
	withoutConsumer := evaluation
	withoutConsumer.RoutineConsumer = nil
	if _, err := n.Eval(&withoutConsumer); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("same older reference gained authority without retained consumer", err)
	}
	if n.Ref != original.Ref || n.RoutineReference != original.RoutineReference || n.Namespace != original.Namespace || n.ResultUsage() != original.ResultUsage() || n.Ref.Epoch != before.Epoch() {
		t.Fatal("contextual epoch admission mutated source recipe")
	}
}
