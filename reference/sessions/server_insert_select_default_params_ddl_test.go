//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerInsertSelectDefaultParamsAlterDefault(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			dsn := f.DSNFor(backend)
			for _, named := range []bool{false, true} {
				for _, warm := range []bool{false, true} {
					t.Run(fmt.Sprintf("raw/named_%t/warm_%t", named, warm), func(t *testing.T) {
						a, b := serverAuthenticated(t, dsn), serverAuthenticated(t, dsn)
						table := fmt.Sprintf("alter_raw_%t_%t", named, warm)
						insertDefaultCommand(t, b, "CREATE TABLE "+table+"(id int DEFAULT 7,c int)", "CREATE TABLE", 'I')
						name := ""
						if named {
							name = "before_alter"
						}
						insertDefaultPrepare(t, a, name, "INSERT INTO "+table+"(c) SELECT $1", []uint32{23}, false, 'I')
						var want [][]string
						if warm {
							insertDefaultExecute(t, a, name, insertDefaultArgs(0), 1, 'I')
							want = append(want, []string{"7", "0"})
						}
						// Both sessions remain connected. Every portal has finished its
						// autocommit Sync before the peer requests AccessExclusiveLock.
						insertDefaultPeerAlter(t, b, table, 9)
						insertDefaultDescribe(t, a, name, []uint32{23}, 'I')
						insertDefaultExecute(t, a, name, insertDefaultArgs(1), 1, 'I')
						want = append(want, []string{"9", "1"})
						insertDefaultPeerAlter(t, b, table, 11)
						insertDefaultDescribe(t, a, name, []uint32{23}, 'I')
						insertDefaultExecute(t, a, name, insertDefaultArgs(2), 1, 'I')
						want = append(want, []string{"11", "2"})
						insertDefaultHealthy(t, a, table, len(want), 'I')
						insertDefaultRead(t, b, "SELECT id,c FROM "+table+" ORDER BY c", []string{"id", "c"}, []uint32{23, 23}, want, 'I')
					})
				}
			}
			for _, warm := range []bool{false, true} {
				t.Run(fmt.Sprintf("libpq_prepared/warm_%t", warm), func(t *testing.T) {
					peer := serverAuthenticated(t, dsn)
					table := fmt.Sprintf("alter_pq_%t", warm)
					insertDefaultCommand(t, peer, "CREATE TABLE "+table+"(id int DEFAULT 7,c int)", "CREATE TABLE", 'I')
					conn := insertDefaultPQ(t, f.Ctx, dsn)
					statement, err := conn.PrepareContext(f.Ctx, "INSERT INTO "+table+"(c) SELECT $1")
					if err != nil {
						t.Fatal(err)
					}
					defer statement.Close()
					execute := func(value int) {
						t.Helper()
						result, err := statement.ExecContext(f.Ctx, value)
						if err != nil {
							t.Fatalf("held pq statement after peer DDL: %v", err)
						}
						count, err := result.RowsAffected()
						if err != nil || count != 1 {
							t.Fatalf("pq affected=%d want=1: %v", count, err)
						}
					}
					var want [][2]int64
					if warm {
						execute(0)
						want = append(want, [2]int64{7, 0})
					}
					insertDefaultPeerAlter(t, peer, table, 9)
					execute(1)
					want = append(want, [2]int64{9, 1})
					insertDefaultPeerAlter(t, peer, table, 11)
					execute(2)
					want = append(want, [2]int64{11, 2})
					insertDefaultClientPairs(t, insertDefaultSQLAdapter(t, f.Ctx, conn, "exec"), "SELECT id,c FROM "+table+" ORDER BY c", want)
					serverScalar(t, peer, "SELECT count(*) FROM "+table, fmt.Sprint(len(want)))
				})
			}
			t.Run("pgx_default_cache_warm", func(t *testing.T) {
				peer := serverAuthenticated(t, dsn)
				const table = "alter_pgx"
				insertDefaultCommand(t, peer, "CREATE TABLE "+table+"(id int DEFAULT 7,c int)", "CREATE TABLE", 'I')
				conn := insertDefaultPGX(t, f.Ctx, dsn)
				client := insertDefaultPGXAdapter(t, f.Ctx, conn, nil)
				query := "INSERT INTO " + table + "(c) SELECT $1"
				client.exec(query, []any{0}, 1)
				insertDefaultPeerAlter(t, peer, table, 9)
				client.exec(query, []any{1}, 1)
				insertDefaultPeerAlter(t, peer, table, 11)
				client.exec(query, []any{2}, 1)
				insertDefaultClientPairs(t, client, "SELECT id,c FROM "+table+" ORDER BY c", [][2]int64{{7, 0}, {9, 1}, {11, 2}})
				insertDefaultRead(t, peer, "SELECT id,c FROM "+table+" ORDER BY c", []string{"id", "c"}, []uint32{23, 23}, [][]string{{"7", "0"}, {"9", "1"}, {"11", "2"}}, 'I')
			})
		})
	}
}

func insertDefaultPeerAlter(t *testing.T, peer *serverWire, table string, value int) {
	t.Helper()
	// Explicit peer commit is the synchronization edge, not a sleep or retry.
	insertDefaultCommand(t, peer, "BEGIN", "BEGIN", 'T')
	insertDefaultCommand(t, peer, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN id SET DEFAULT %d", table, value), "ALTER TABLE", 'T')
	insertDefaultCommand(t, peer, "COMMIT", "COMMIT", 'I')
}
