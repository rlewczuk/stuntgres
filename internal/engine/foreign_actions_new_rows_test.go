package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestForeignActionsLocksNewRows(t *testing.T) {
	for _, isolation := range []Isolation{RepeatableRead, Serializable} {
		for _, direction := range []string{"DELETE", "UPDATE"} {
			t.Run(fmt.Sprintf("%d/%s", isolation, direction), func(t *testing.T) {
				e := New()
				reader, writer := sessionStart(t, e, nil), sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE ON UPDATE CASCADE,note TEXT)", "INSERT INTO p VALUES(1),(9)"} {
					sessionSQL(t, writer, q)
				}
				op := transactionOperation(t, reader)
				ref, err := e.Begin(op, TxOptions{Isolation: isolation})
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				// Registered before the scope cleanup so its operation ends before rollback.
				t.Cleanup(func() { transactionEnd(t, reader, ref, false) })
				transactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 9)
				child := catalogRef(t, e, e.CatalogRead(), "c")
				root, exists := reader.transactions.roots[child.Address]
				if !exists || len(root.versions) != 0 {
					t.Fatal("first stable snapshot did not retain the empty child root", exists, root)
				}
				sessionSQL(t, writer, "INSERT INTO c VALUES(1,'committed')")
				committed := e.storage[child.Address].versions[0]
				if committed.born <= reader.transactions.frontier {
					t.Fatal("writer did not create a genuinely post-snapshot child")
				}
				q := "DELETE FROM p WHERE id=1"
				sql := `DELETE FROM ONLY "public"."c" WHERE $1 OPERATOR(pg_catalog.=) "pid"`
				if direction == "UPDATE" {
					q = "UPDATE p SET id=2 WHERE id=1"
					sql = `UPDATE ONLY "public"."c" SET "pid" = $1 WHERE $2 OPERATOR(pg_catalog.=) "pid"`
				}
				scope := foreignActionUnitScope(t, e, reader, q)
				err = scope.settleForeignActions()
				var d *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update", Context: `SQL statement "` + sql + `"`}
				if !errors.As(err, &d) || *d != want {
					t.Fatal(d, err, want)
				}
				if scope.Finish(err) != err {
					t.Fatal("Finish changed the action conflict")
				}
				parent := catalogRef(t, e, e.CatalogRead(), "p")
				if e.storage[parent.Address].ends != nil || e.storage[child.Address].ends != nil || e.storage[child.Address].versions[0].version != committed.version {
					t.Fatal("failed action altered committed parent/writer child")
				}
				transactionRows(t, writer, "SELECT pid FROM c", 1)
			})
		}
	}
}
