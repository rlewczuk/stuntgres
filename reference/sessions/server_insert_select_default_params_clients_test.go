//go:build !noservertests

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func insertDefaultWithClient(t *testing.T, ctx context.Context, dsn, route string, explicit bool, body func(insertDefaultClient)) {
	t.Helper()
	if route == "pgx" {
		conn := insertDefaultPGX(t, ctx, dsn)
		if explicit {
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			body(insertDefaultPGXAdapter(t, ctx, conn, tx))
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		} else {
			body(insertDefaultPGXAdapter(t, ctx, conn, nil))
		}
		return
	}
	conn := insertDefaultPQ(t, ctx, dsn)
	var target insertDefaultSQLTarget = conn
	var tx *sql.Tx
	if explicit {
		var err error
		tx, err = conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		target = tx
	}
	body(insertDefaultSQLAdapter(t, ctx, target, route))
	if tx != nil {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceServerInsertSelectDefaultParamsClients(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			dsn := f.DSNFor(backend)
			for _, route := range []string{"query", "exec", "prepared", "pgx"} {
				for _, explicit := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/transaction_%t/minimal", route, explicit), func(t *testing.T) {
						w := serverAuthenticated(t, dsn)
						table := fmt.Sprintf("client_minimal_%s_%t", route, explicit)
						insertDefaultCommand(t, w, "CREATE TABLE "+table+" (id int DEFAULT 7,c int)", "CREATE TABLE", 'I')
						var want [][2]int64
						insertDefaultWithClient(t, f.Ctx, dsn, route, explicit, func(client insertDefaultClient) {
							for value := 1; value <= 7; value++ {
								client.exec("INSERT INTO "+table+"(c) SELECT $1", []any{value}, 1)
								want = append(want, [2]int64{7, int64(value)})
								insertDefaultClientPairs(t, client, "SELECT id,c FROM "+table+" ORDER BY c", want)
							}
							if explicit {
								serverScalar(t, w, "SELECT count(*) FROM "+table, "0")
							}
						})
						serverScalar(t, w, "SELECT count(*) FROM "+table, "7")
					})
					for _, family := range insertDefaultFamilies {
						for sourceIndex := range insertDefaultSources("unused") {
							t.Run(fmt.Sprintf("%s/transaction_%t/%s/source_%d", route, explicit, family.name, sourceIndex), func(t *testing.T) {
								table := fmt.Sprintf("client_%s_%t_%s_%d", route, explicit, family.name, sourceIndex)
								w := serverAuthenticated(t, dsn)
								for _, query := range family.setup(table) {
									serverWantCode(t, w.query(t, query), "")
								}
								insertDefaultWithClient(t, f.Ctx, dsn, route, explicit, func(client insertDefaultClient) {
									source := insertDefaultSources(table)[sourceIndex]
									var want [][2]int64
									n := 0
									for _, use := range source.uses {
										client.exec("INSERT INTO "+table+"(c) "+source.suffix, use.args, int64(use.count))
										for range use.count {
											id := 7
											if family.name != "constant" {
												id = family.first + n
											}
											want = append(want, [2]int64{int64(id), int64(use.args[0].(int))})
											n++
										}
										insertDefaultClientCount(t, client, table, int64(n))
									}
									if family.name != "now" {
										insertDefaultClientPairs(t, client, "SELECT id,c FROM "+table+" ORDER BY c,id", want)
									} else {
										query := "SELECT stamp IS NOT NULL FROM " + table + " ORDER BY c"
										if explicit {
											query = "SELECT stamp IS NOT NULL AND stamp = transaction_timestamp() FROM " + table + " ORDER BY c"
										}
										client.query(query, func(rows insertDefaultRows) {
											seen := 0
											for rows.Next() {
												var valid bool
												if err := rows.Scan(&valid); err != nil || !valid {
													t.Fatalf("timestamp valid=%t: %v", valid, err)
												}
												seen++
											}
											if seen != n {
												t.Fatalf("timestamps=%d want=%d", seen, n)
											}
										})
									}
									if explicit {
										serverScalar(t, w, "SELECT count(*) FROM "+table, "0")
									}
								})
							})
						}
					}
					for _, nine := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/transaction_%t/fider_nine_%t", route, explicit, nine), func(t *testing.T) {
							w := serverAuthenticated(t, dsn)
							serverWantCode(t, w.query(t, "DROP TABLE IF EXISTS attachments"), "")
							schema := insertDefaultAttachments
							if nine {
								schema = insertDefaultAttachmentsNine
							}
							insertDefaultCommand(t, w, schema, "CREATE TABLE", 'I')
							insertDefaultWithClient(t, f.Ctx, dsn, route, explicit, func(client insertDefaultClient) {
								insertDefaultClientFider(t, client, nine)
								if explicit {
									serverScalar(t, w, "SELECT count(*) FROM attachments", "0")
								}
							})
							insertDefaultRead(t, w, "SELECT id,tenant_id,comment_id,attachment_bkey FROM attachments ORDER BY id", []string{"id", "tenant_id", "comment_id", "attachment_bkey"}, []uint32{23, 23, 23, 1043}, [][]string{{"1", "1", "<NULL>", "12345-test.png"}, {"2", "1", "4", "12345-test.png"}, {"3", "2", "<NULL>", "12345-test.png"}, {"4", "1", "<NULL>", "other.png"}}, 'I')
						})
					}
				}
			}
		})
	}
}
