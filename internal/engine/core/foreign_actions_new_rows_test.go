package core_test

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestForeignActionsLocksNewRows(t *testing.T) {
	for _, isolation := range []session.Isolation{session.RepeatableRead, session.Serializable} {
		for _, direction := range []string{"DELETE", "UPDATE"} {
			t.Run(fmt.Sprintf("%d/%s", isolation, direction), func(t *testing.T) {
				e := engine.New()
				reader, writer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE ON UPDATE CASCADE,note TEXT)", "INSERT INTO p VALUES(1),(9)"} {
					enginetest.SessionSQL(t, writer, q)
				}
				op := enginetest.TransactionOperation(t, reader)
				ref, err := e.Begin(op, session.TxOptions{Isolation: isolation})
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				// Registered before the scope cleanup so its operation ends before rollback.
				t.Cleanup(func() { enginetest.TransactionEnd(t, reader, ref, false) })
				enginetest.TransactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 9)
				child := enginetest.CatalogRef(t, e, e.CatalogRead(), "c")
				root, exists := reader.Transactions.Roots[child.Address]
				if !exists || len(root.Versions) != 0 {
					t.Fatal("first stable snapshot did not retain the empty child root", exists, root)
				}
				enginetest.SessionSQL(t, writer, "INSERT INTO c VALUES(1,'committed')")
				committed := e.Storage.Relations[child.Address].Versions[0]
				if committed.Born <= reader.Transactions.Frontier {
					t.Fatal("writer did not create a genuinely post-snapshot child")
				}
				q := "DELETE FROM p WHERE id=1"
				sql := `DELETE FROM ONLY "public"."c" WHERE $1 OPERATOR(pg_catalog.=) "pid"`
				if direction == "UPDATE" {
					q = "UPDATE p SET id=2 WHERE id=1"
					sql = `UPDATE ONLY "public"."c" SET "pid" = $1 WHERE $2 OPERATOR(pg_catalog.=) "pid"`
				}
				scope := foreignActionUnitScope(t, e, reader, q)
				err = dml.SettleForeignActions(scope.ActivateRuntime(scope.Op.Context()), scope.WriteHost())
				var d *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update", Context: `SQL statement "` + sql + `"`}
				if !errors.As(err, &d) || *d != want {
					t.Fatal(d, err, want)
				}
				if scope.Finish(err) != err {
					t.Fatal("Finish changed the action conflict")
				}
				parent := enginetest.CatalogRef(t, e, e.CatalogRead(), "p")
				if e.Storage.Relations[parent.Address].Ends != nil || e.Storage.Relations[child.Address].Ends != nil || e.Storage.Relations[child.Address].Versions[0].Version != committed.Version {
					t.Fatal("failed action altered committed parent/writer child")
				}
				enginetest.TransactionRows(t, writer, "SELECT pid FROM c", 1)
			})
		}
	}
}
