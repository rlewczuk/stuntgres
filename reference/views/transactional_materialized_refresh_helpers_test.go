package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Each serial cell gets its own bounded scenario; handles belong to the pinned
// physical owner, including when the commands themselves use database/sql Tx.
func txRefreshRoutes(t *testing.T, modes []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn, string, bool, bool)) {
	t.Helper()
	for _, concurrent := range []bool{false, true} {
		for _, mode := range modes {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("concurrent=%v/%s/held=%v", concurrent, mode, held), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"txr_owner", "txr_actor"})
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							body(t, p, b, c, peer, mode, held, concurrent)
						})
					}
				})
			}
		}
	}
}

func txRefreshSQL(concurrent bool) string {
	if concurrent {
		return "REFRESH MATERIALIZED VIEW CONCURRENTLY txr_mv"
	}
	return "REFRESH MATERIALIZED VIEW txr_mv"
}

func txRefreshSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE txr_source(i INT)", 0))
	run(fixture.Exec("INSERT INTO txr_source VALUES(1)", 1))
	run(fixture.Exec("CREATE MATERIALIZED VIEW txr_mv AS SELECT i FROM txr_source", 1))
	run(fixture.Exec("CREATE UNIQUE INDEX txr_u ON txr_mv(i)", 0))
}

func txRefreshRows(name string, values ...int64) fixture.Step {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	return fixture.Select("SELECT i FROM "+name+" ORDER BY i", []string{"i"}, []string{"INT4"}, rows)
}

func txRefreshHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, held bool, queries ...string) func(fixture.Executor) fixture.Executor {
	t.Helper()
	statements := make(map[string]*sql.Stmt)
	if held {
		for _, q := range queries {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("quiet Prepare", q, err)
			}
			t.Cleanup(func() { stmt.Close() })
			statements[q] = stmt
		}
	}
	return func(e fixture.Executor) fixture.Executor {
		for _, q := range queries {
			if stmt := statements[q]; stmt != nil {
				e = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
			}
		}
		return e
	}
}

func txRefreshIdentity(t *testing.T, p *fixture.Pair, e fixture.Executor) []int64 {
	t.Helper()
	identity := transactionalViewsIdentity(t, p, e, "txr_mv")
	var index int64
	rows, err := e.QueryContext(p.Ctx, "SELECT indexrelid::BIGINT FROM pg_index WHERE indrelid='txr_mv'::regclass AND indisunique")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing eligible index", rows.Err())
	}
	if err := rows.Scan(&index); err != nil {
		t.Fatal(err)
	}
	if index <= 0 || rows.Next() || rows.Err() != nil {
		t.Fatal("index identity", index, rows.Err())
	}
	return append(identity, index)
}

func txRefreshSameIdentity(t *testing.T, p *fixture.Pair, e fixture.Executor, want []int64) {
	t.Helper()
	if got := txRefreshIdentity(t, p, e); !reflect.DeepEqual(got, want) {
		t.Fatal("refresh changed identity", got, want)
	}
}

func txRefreshMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(types) != 1 || types[0].Name() != "i" || types[0].DatabaseTypeName() != "INT4" {
			t.Fatal("refresh metadata", types)
		}
		nullable, known := types[0].Nullable()
		if pg {
			if known {
				t.Fatal("pq nullability unexpectedly known")
			}
		} else if !known || !nullable {
			t.Fatal("stored nullable column metadata", nullable, known)
		}
		want := reflect.TypeOf(int64(0))
		if pg {
			want = reflect.TypeOf(int32(0))
		}
		if types[0].ScanType() != want {
			t.Fatal("scan type", types[0].ScanType(), want)
		}
	}
}
