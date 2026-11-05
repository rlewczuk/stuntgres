package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func analyzeProgressCrossDatabase(t *testing.T) {
	e := engine.New()
	admin := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, admin, "CREATE DATABASE analyze_other_database")
	other, err := e.SelectDatabase(context.Background(), "analyze_other_database")
	if err != nil {
		t.Fatal(err)
	}
	worker := enginetest.StartSession(t, other, nil)
	enginetest.SessionSQL(t, worker, "CREATE TABLE analyze_database_unit(k INT)")
	relation := enginetest.CatalogRef(t, other, other.CatalogRead(), "analyze_database_unit").Address
	scope, _ := analyzeProgressScope(t, other, worker)
	p := analyzeProgressBegin(other, scope, relation)
	defer p.End()
	observer := enginetest.TransactionOperation(t, admin)
	defer observer.Done()
	f := analyzeProgressOne(t, e, observer)
	if f.PID != int64(worker.Token().ID()) || f.Database != other.Catalog.DatabaseAddress().ObjectID || f.Database == e.Catalog.DatabaseAddress().ObjectID || !f.DatabaseExists || f.DatabaseName != "analyze_other_database" || f.Relation != relation.ObjectID {
		t.Fatal("observer database substituted for real cluster identity", f)
	}
}
