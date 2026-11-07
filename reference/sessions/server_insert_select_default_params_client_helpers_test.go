//go:build !noservertests

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lib/pq"
)

type insertDefaultSQLTarget interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

type insertDefaultRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

type insertDefaultClient struct {
	exec  func(string, []any, int64)
	query func(string, func(insertDefaultRows))
}

func insertDefaultPQ(t *testing.T, ctx context.Context, dsn string) *sql.Conn {
	t.Helper()
	connector, err := pq.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func insertDefaultPGX(t *testing.T, ctx context.Context, dsn string) *pgx.Conn {
	t.Helper()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if config.DefaultQueryExecMode != pgx.QueryExecModeCacheStatement || config.StatementCacheCapacity <= 0 {
		t.Fatalf("pgx defaults mode=%v cache=%d", config.DefaultQueryExecMode, config.StatementCacheCapacity)
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func insertDefaultSQLAdapter(t *testing.T, ctx context.Context, target insertDefaultSQLTarget, mode string) insertDefaultClient {
	t.Helper()
	statements := map[string]*sql.Stmt{}
	t.Cleanup(func() {
		for _, statement := range statements {
			statement.Close()
		}
	})
	return insertDefaultClient{
		exec: func(query string, args []any, count int64) {
			t.Helper()
			if mode == "query" {
				rows, err := target.QueryContext(ctx, query, args...)
				if err != nil {
					t.Fatalf("pq QueryContext %s: %v", query, err)
				}
				unexpected := false
				for rows.Next() {
					unexpected = true
				}
				err = rows.Err()
				closeErr := rows.Close()
				if unexpected || err != nil || closeErr != nil {
					t.Fatalf("pq INSERT drain rows=%t Err=%v Close=%v", unexpected, err, closeErr)
				}
				return
			}
			var result sql.Result
			var err error
			if mode == "prepared" {
				statement := statements[query]
				if statement == nil {
					statement, err = target.PrepareContext(ctx, query)
					if err != nil {
						t.Fatalf("pq PrepareContext %s: %v", query, err)
					}
					statements[query] = statement
				}
				result, err = statement.ExecContext(ctx, args...)
			} else {
				result, err = target.ExecContext(ctx, query, args...)
			}
			if err != nil {
				t.Fatalf("pq %s %s: %v", mode, query, err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != count {
				t.Fatalf("pq %s affected=%d want=%d: %v", mode, n, count, err)
			}
		},
		query: func(query string, consume func(insertDefaultRows)) {
			t.Helper()
			rows, err := target.QueryContext(ctx, query)
			if err != nil {
				t.Fatalf("pq read %s: %v", query, err)
			}
			defer rows.Close()
			consume(rows)
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func insertDefaultPGXAdapter(t *testing.T, ctx context.Context, conn *pgx.Conn, tx pgx.Tx) insertDefaultClient {
	t.Helper()
	return insertDefaultClient{
		exec: func(query string, args []any, count int64) {
			t.Helper()
			var tag string
			var affected int64
			if tx != nil {
				result, err := tx.Exec(ctx, query, args...)
				if err != nil {
					t.Fatalf("pgx cached transaction %s: %v", query, err)
				}
				tag, affected = result.String(), result.RowsAffected()
			} else {
				result, err := conn.Exec(ctx, query, args...)
				if err != nil {
					t.Fatalf("pgx cached %s: %v", query, err)
				}
				tag, affected = result.String(), result.RowsAffected()
			}
			want := fmt.Sprintf("INSERT 0 %d", count)
			if tag != want || affected != count {
				t.Fatalf("pgx tag=%q affected=%d want=%q/%d", tag, affected, want, count)
			}
		},
		query: func(query string, consume func(insertDefaultRows)) {
			t.Helper()
			var rows pgx.Rows
			var err error
			if tx != nil {
				rows, err = tx.Query(ctx, query)
			} else {
				rows, err = conn.Query(ctx, query)
			}
			if err != nil {
				t.Fatalf("pgx read %s: %v", query, err)
			}
			defer rows.Close()
			consume(rows)
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func insertDefaultClientPairs(t *testing.T, client insertDefaultClient, query string, want [][2]int64) {
	t.Helper()
	client.query(query, func(rows insertDefaultRows) {
		var got [][2]int64
		for rows.Next() {
			var pair [2]int64
			if err := rows.Scan(&pair[0], &pair[1]); err != nil {
				t.Fatal(err)
			}
			got = append(got, pair)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("typed rows=%v want=%v", got, want)
		}
	})
}

func insertDefaultClientCount(t *testing.T, client insertDefaultClient, table string, want int64) {
	t.Helper()
	client.query("SELECT count(*) FROM "+table, func(rows insertDefaultRows) {
		var count int64
		if !rows.Next() {
			t.Fatal("missing count row")
		}
		if err := rows.Scan(&count); err != nil || count != want {
			t.Fatalf("count=%d want=%d: %v", count, want, err)
		}
		if rows.Next() {
			t.Fatal("extra count row")
		}
	})
}

func insertDefaultClientFider(t *testing.T, client insertDefaultClient, nine bool) {
	t.Helper()
	query := insertDefaultFiderSix
	if nine {
		query = insertDefaultFiderNine
	}
	cases := []struct {
		tenant  int
		comment any
		key     string
	}{{1, nil, "12345-test.png"}, {1, 4, "12345-test.png"}, {2, nil, "12345-test.png"}, {1, nil, "other.png"}}
	for i, c := range cases {
		args := []any{c.tenant, c.comment, c.key, c.tenant, c.comment, c.key}
		if nine {
			args = []any{c.tenant, 10, c.comment, 20, c.key, c.tenant, 10, c.comment, c.key}
		}
		client.exec(query, args, 1)
		insertDefaultClientCount(t, client, "attachments", int64(i+1))
		client.exec(query, args, 0)
		insertDefaultClientCount(t, client, "attachments", int64(i+1))
	}
	client.query("SELECT id,tenant_id,comment_id,attachment_bkey FROM attachments ORDER BY id", func(rows insertDefaultRows) {
		i := 0
		for rows.Next() {
			if i >= len(cases) {
				t.Fatal("extra attachment")
			}
			var id, tenant int64
			var comment sql.NullInt64
			var key string
			if err := rows.Scan(&id, &tenant, &comment, &key); err != nil {
				t.Fatal(err)
			}
			c := cases[i]
			if id != int64(i+1) || tenant != int64(c.tenant) || key != c.key || comment.Valid != (c.comment != nil) || (comment.Valid && comment.Int64 != int64(c.comment.(int))) {
				t.Fatalf("attachment id=%d tenant=%d comment=%+v key=%q want=%+v", id, tenant, comment, key, c)
			}
			i++
		}
		if i != len(cases) {
			t.Fatalf("attachments=%d want=%d", i, len(cases))
		}
	})
	if nine {
		insertDefaultClientPairs(t, client, "SELECT post_id,user_id FROM attachments ORDER BY id", [][2]int64{{10, 20}, {10, 20}, {10, 20}, {10, 20}})
	}
}
