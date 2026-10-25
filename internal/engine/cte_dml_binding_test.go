package engine

import (
	"context"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCTEDMLBindingOwner(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE TABLE t(id INT,v INT)")
	sessionSQL(t, session, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	scope, op := cteDMLUnitScope(t, session, context.Background())
	ast := statement(t, "WITH one AS (UPDATE t SET v=v+$1 WHERE id=1 RETURNING id,v), two AS (UPDATE t SET v=v+$2 WHERE id=2 RETURNING id,v) UPDATE t SET v=v+$3 WHERE id=3 RETURNING id,v")
	description, err := scope.Describe(ast)
	if err != nil {
		t.Fatal(err)
	}
	if description.Parameters.Count != 3 || len(description.Parameters.Types) != 3 || len(description.Results) != 2 {
		t.Fatal(description)
	}
	for _, usage := range description.Parameters.Types {
		if usage.Type.ID() != sqltype.Int4 {
			t.Fatal(usage)
		}
	}
	if len(scope.mutations) != 3 || description.analysis == nil || len(description.analysis.targets) != 3 {
		t.Fatal("occurrences were conflated", len(scope.mutations))
	}
	seen := make(map[parser.Statement]bool)
	for i := range scope.mutations {
		mutation := scope.mutations[i]
		if i == scope.ActiveMutation {
			mutation = scope.StatementMutation
		}
		if seen[mutation.Statement] || mutation.LockedRef != ref || description.analysis.targets[mutation.Statement] != ref {
			t.Fatal("wrong exact target occurrence", i, mutation.LockedRef)
		}
		seen[mutation.Statement] = true
		if mutation.InsertReturning != nil || mutation.UniqueBinding != nil || mutation.PartitionInsert != nil {
			t.Fatal("Prepare retained executable target state")
		}
	}
	if scope.Stage.Rows != nil || scope.Stage.Deletes != nil || scope.View != nil || scope.cteInvocation != nil {
		t.Fatal("Prepare executed a modifier or captured data")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	transactionRows(t, session, "SELECT v FROM t ORDER BY id", 10, 20, 30)
	lockEmpty(t, e)
}
