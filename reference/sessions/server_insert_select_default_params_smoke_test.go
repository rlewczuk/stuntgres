//go:build !noservertests

package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerInsertSelectDefaultParamsFeatureSmoke(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			dsn := f.DSNFor(backend)
			wire, peer := serverAuthenticated(t, dsn), serverAuthenticated(t, dsn)
			insertDefaultCommand(t, wire, "CREATE TABLE smoke_raw(id int DEFAULT 7,c int)", "CREATE TABLE", 'I')
			insertDefaultPrepare(t, wire, "", "INSERT INTO smoke_raw(c) SELECT $1", []uint32{23}, false, 'I')
			insertDefaultExecute(t, wire, "", insertDefaultArgs(1), 1, 'I')
			insertDefaultRead(t, peer, "SELECT id,c FROM smoke_raw", []string{"id", "c"}, []uint32{23, 23}, [][]string{{"7", "1"}}, 'I')

			insertDefaultCommand(t, peer, insertDefaultAttachments, "CREATE TABLE", 'I')
			pq := insertDefaultPQ(t, f.Ctx, dsn)
			tx, err := pq.BeginTx(f.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			client := insertDefaultSQLAdapter(t, f.Ctx, tx, "exec")
			args := []any{1, nil, "12345-test.png", 1, nil, "12345-test.png"}
			client.exec(insertDefaultFiderSix, args, 1)
			insertDefaultClientCount(t, client, "attachments", 1)
			client.exec(insertDefaultFiderSix, args, 0)
			insertDefaultClientCount(t, client, "attachments", 1)
			client.query("SELECT id,tenant_id,comment_id,attachment_bkey FROM attachments", func(rows insertDefaultRows) {
				var id, tenant int64
				var comment sql.NullInt64
				var key string
				if !rows.Next() {
					t.Fatal("missing Fider row")
				}
				if err := rows.Scan(&id, &tenant, &comment, &key); err != nil {
					t.Fatal(err)
				}
				if id != 1 || tenant != 1 || comment.Valid || key != "12345-test.png" {
					t.Fatalf("Fider row=(%d,%d,%+v,%q)", id, tenant, comment, key)
				}
				if rows.Next() {
					t.Fatal("extra Fider row")
				}
			})
			serverScalar(t, peer, "SELECT count(*) FROM attachments", "0")
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			insertDefaultRead(t, peer, "SELECT id,tenant_id,comment_id,attachment_bkey FROM attachments", []string{"id", "tenant_id", "comment_id", "attachment_bkey"}, []uint32{23, 23, 23, 1043}, [][]string{{"1", "1", "<NULL>", "12345-test.png"}}, 'I')

			insertDefaultCommand(t, peer, "CREATE TABLE smoke_cached(id int DEFAULT 7,c int)", "CREATE TABLE", 'I')
			pgx := insertDefaultPGX(t, f.Ctx, dsn)
			cached := insertDefaultPGXAdapter(t, f.Ctx, pgx, nil)
			const query = "INSERT INTO smoke_cached(c) SELECT $1"
			cached.exec(query, []any{1}, 1)
			cached.exec(query, []any{2}, 1)
			insertDefaultClientPairs(t, cached, "SELECT id,c FROM smoke_cached ORDER BY c", [][2]int64{{7, 1}, {7, 2}})
			insertDefaultPeerAlter(t, peer, "smoke_cached", 9)
			cached.exec(query, []any{3}, 1)
			insertDefaultClientPairs(t, cached, "SELECT id,c FROM smoke_cached ORDER BY c", [][2]int64{{7, 1}, {7, 2}, {9, 3}})
			insertDefaultRead(t, peer, "SELECT id,c FROM smoke_cached ORDER BY c", []string{"id", "c"}, []uint32{23, 23}, [][]string{{"7", "1"}, {"7", "2"}, {"9", "3"}}, 'I')
		})
	}
}
