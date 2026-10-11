package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLockSkipBounds(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			tail string
			want []int64
			held []int
			free []int
		}{
			{"LIMIT 2", []int64{2, 3}, []int{2, 3}, []int{4}},
			{"OFFSET 1 LIMIT 1", []int64{3}, []int{2, 3}, []int{4}},
			{"LIMIT 0", nil, nil, []int{2, 3, 4}},
			{"OFFSET 3 LIMIT 1", nil, []int{2, 3, 4}, nil},
		} {
			for _, source := range []string{"t", "t a JOIN u b USING(id)", "plain"} {
				t.Run(fmt.Sprintf("%s/%s/%v", source, tc.tail, prepared), func(t *testing.T) {
					fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
						fixture.LockExec(t, p.Ctx, a, "CREATE VIEW plain AS SELECT id,v FROM t", 0)
						holder := fixture.QueryLockBegin(t, p.Ctx, a)
						fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
						reader := fixture.QueryLockBegin(t, p.Ctx, peer)
						fixture.QueryLockRows(t, p, b, reader, "SELECT id FROM "+source+" ORDER BY id "+tc.tail+" FOR UPDATE SKIP LOCKED", prepared, "id", tc.want...)
						c, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						for _, id := range tc.free {
							fixture.RowAffected(t, p.Ctx, c, fmt.Sprintf("UPDATE t SET v=v WHERE id=%d", id), prepared, 1)
						}
						for _, id := range tc.held {
							queryLockMigratedBlocked(t, p.Ctx, c, b.PG, fmt.Sprintf("UPDATE t SET v=v WHERE id=%d", id), prepared)
						}
						// SKIP does not interfere with the original owner or its reentry.
						fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", true, "id", 1)
						if err := reader.Rollback(); err != nil {
							t.Fatal(err)
						}
						fixture.RowAffected(t, p.Ctx, c, "UPDATE t SET v=v WHERE id>1", false, 3)
					})
				})
			}
		}
	}
	// Actual bound parameters and held prepared re-execution, not repeated Prepare.
	fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		holder := fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
		stmt, err := peer.PrepareContext(p.Ctx, "SELECT id FROM t ORDER BY id OFFSET $1 LIMIT $2 FOR UPDATE SKIP LOCKED")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, args := range [][]any{{0, 2}, {1, 1}, {0, 0}} {
			rows, err := stmt.QueryContext(p.Ctx, args...)
			want := [][]any{{int64(2)}, {int64(3)}}
			if args[0] == 1 {
				want = [][]any{{int64(3)}}
			}
			if args[1] == 0 {
				want = [][]any{}
			}
			queryLockMigratedRows(t, rows, err, []string{"id"}, []string{"INT4"}, want)
		}
	})
	// Stored local bounds count survivors too, including upper aggregate demand.
	for _, query := range []string{"SELECT id FROM marked", "SELECT id FROM marked ORDER BY id LIMIT 1"} {
		t.Run(query, func(t *testing.T) {
			fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				fixture.LockExec(t, p.Ctx, a, "CREATE VIEW marked AS SELECT id,v FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE SKIP LOCKED", 0)
				holder := fixture.QueryLockBegin(t, p.Ctx, a)
				fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
				reader := fixture.QueryLockBegin(t, p.Ctx, peer)
				fixture.QueryLockRows(t, p, b, reader, query, true, "id", 3)
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				queryLockMigratedBlocked(t, p.Ctx, c, b.PG, "UPDATE t SET v=v WHERE id=2", false)
				queryLockMigratedBlocked(t, p.Ctx, c, b.PG, "UPDATE t SET v=v WHERE id=3", false)
				fixture.RowAffected(t, p.Ctx, c, "UPDATE t SET v=v WHERE id=4", false, 1)
			})
		})
	}
}

func TestReferenceQueryLockInheritedViewBounds(t *testing.T) {
	queryLockInheritedViewBounds(t)
}

// Independently hypothesized inherited-view count contract. Root must discover
// this added path on pinned PostgreSQL before treating it as behavioral evidence.
func queryLockInheritedViewBounds(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, definition, query  string
			want                     []int64
			held, allSkipped, nested bool
		}{
			{"limit", "LIMIT 2", "SELECT id FROM limited ORDER BY id FOR UPDATE OF limited SKIP LOCKED", []int64{2, 3}, true, false, false},
			{"offset", "OFFSET 1 LIMIT 1", "SELECT id FROM limited ORDER BY id FOR UPDATE OF limited SKIP LOCKED", []int64{3}, true, false, false},
			{"inner-zero", "LIMIT 0", "SELECT id FROM limited FOR UPDATE OF limited SKIP LOCKED", nil, false, false, false},
			{"outer-zero", "LIMIT 2", "SELECT id FROM limited LIMIT 0 FOR UPDATE OF limited SKIP LOCKED", nil, false, false, false},
			{"all-skipped", "LIMIT 2", "SELECT id FROM limited ORDER BY id FOR UPDATE OF limited SKIP LOCKED", nil, false, true, false},
			{"unselected-sibling", "LIMIT 2", "SELECT limited.id FROM limited JOIN u USING(id) ORDER BY limited.id FOR UPDATE OF limited SKIP LOCKED", []int64{2, 3}, true, false, false},
			{"nested", "LIMIT 2", "SELECT id FROM nested ORDER BY id FOR UPDATE OF nested SKIP LOCKED", []int64{2, 3}, true, false, true},
		} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					fixture.LockExec(t, p.Ctx, a, "CREATE VIEW limited AS SELECT id,v FROM t ORDER BY id "+tc.definition, 0)
					if tc.nested {
						fixture.LockExec(t, p.Ctx, a, "CREATE VIEW nested AS SELECT id,v FROM limited OFFSET 0 LIMIT 2", 0)
					}
					holder := fixture.QueryLockBegin(t, p.Ctx, a)
					if tc.allSkipped {
						fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t ORDER BY id FOR UPDATE", false, "id", 1, 2, 3, 4)
					} else {
						fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
					}
					reader := fixture.QueryLockBegin(t, p.Ctx, peer)
					fixture.QueryLockRows(t, p, b, reader, tc.query, prepared, "id", tc.want...)
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					if !tc.allSkipped {
						for _, id := range []int{2, 3} {
							q := fmt.Sprintf("UPDATE t SET v=v WHERE id=%d", id)
							if tc.held {
								queryLockMigratedBlocked(t, p.Ctx, c, b.PG, q, false)
							} else {
								fixture.RowAffected(t, p.Ctx, c, q, false, 1)
							}
						}
						fixture.RowAffected(t, p.Ctx, c, "UPDATE t SET v=v WHERE id=4", false, 1)
					}
					fixture.RowAffected(t, p.Ctx, c, "UPDATE u SET v=v", false, 4)
					fixture.QueryLockNowait(t, p, b, c, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", true, "t")
					if err := reader.Rollback(); err != nil {
						t.Fatal(err)
					}
					if !tc.allSkipped {
						fixture.RowAffected(t, p.Ctx, c, "UPDATE t SET v=v WHERE id=2", false, 1)
					}
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested-independent-merge/prepared=%v", prepared), func(t *testing.T) {
			fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				fixture.LockExec(t, p.Ctx, a, "CREATE VIEW limited AS SELECT id,v FROM t ORDER BY id LIMIT 2 FOR SHARE OF t SKIP LOCKED", 0)
				fixture.LockExec(t, p.Ctx, a, "CREATE VIEW nested AS SELECT id,v FROM limited OFFSET 0 LIMIT 2", 0)
				holder := fixture.QueryLockBegin(t, p.Ctx, a)
				fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
				fixture.QueryLockNowait(t, p, b, peer, "SELECT id FROM nested FOR UPDATE OF nested NOWAIT", prepared, "t")
				fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id=2", false, 1)
			})
		})
	}
}
