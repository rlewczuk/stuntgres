package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"stuntgres/pgserver"
	"stuntgres/reference/fixture"
)

func embeddedConfig() pgserver.Config {
	return pgserver.Config{Listen: "127.0.0.1:0", Accounts: []pgserver.Account{{Username: fixture.ServerUser, Password: fixture.ServerPassword, Databases: []string{fixture.ServerDatabase}}}}
}

func embeddedConnect(t *testing.T, ctx context.Context, b fixture.EmbeddedServerBackend, pipe bool) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(b.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if pipe && b.DialFunc != nil {
		cfg.DialFunc = b.DialFunc
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { embeddedCloseClient(t, c.PgConn()) })
	return c
}

func embeddedCloseClient(t *testing.T, c *pgconn.PgConn) {
	t.Helper()
	cleanup, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
	defer cancel()
	if err := c.Close(cleanup); err != nil {
		t.Errorf("client close: %v", err)
	}
	select {
	case <-c.CleanupDone():
	case <-cleanup.Done():
		t.Error("client cleanup did not join")
	}
}

func embeddedExec(t *testing.T, ctx context.Context, c *pgx.Conn, q string, args ...any) {
	t.Helper()
	if _, err := c.Exec(ctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
func embeddedInt(t *testing.T, ctx context.Context, c *pgx.Conn, q string, want int, args ...any) {
	t.Helper()
	var n int
	if err := c.QueryRow(ctx, q, args...).Scan(&n); err != nil || n != want {
		t.Fatalf("%s: %d %v, want %d", q, n, err, want)
	}
}
func embeddedDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message})
}

func embeddedPGXClient(t *testing.T, ctx context.Context, c *pgx.Conn) {
	t.Helper()
	embeddedInt(t, ctx, c, "SELECT 42", 42, pgx.QueryExecModeSimpleProtocol)
	embeddedInt(t, ctx, c, "SELECT $1::integer + 1", 42, int32(41))
	if _, err := c.Prepare(ctx, "embedded_held", "SELECT $1::integer + 1"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		embeddedInt(t, ctx, c, "embedded_held", 42, int32(41))
	}
	rows, err := c.Query(ctx, "SELECT $1::integer AS answer", pgx.QueryResultFormats{1}, int32(42))
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatalf("binary row: %v", rows.Err())
	}
	fields := rows.FieldDescriptions()
	if len(fields) != 1 || fields[0].Name != "answer" || fields[0].DataTypeOID != 23 || fields[0].Format != 1 || !reflect.DeepEqual(rows.RawValues(), [][]byte{{0, 0, 0, 42}}) {
		rows.Close()
		t.Fatalf("pgx binary fields=%v values=%x", fields, rows.RawValues())
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatalf("binary trailing row/error: %v", rows.Err())
	}
	rows.Close()
	var pid uint32
	if err := c.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil || pid < 2 || pid != c.PgConn().PID() {
		t.Fatalf("physical PID %d / %d: %v", pid, c.PgConn().PID(), err)
	}
	_, err = c.Exec(ctx, "SELECT 1 / 0")
	embeddedDiagnostic(t, err, "22012", "division by zero")
	embeddedInt(t, ctx, c, "SELECT 42", 42)
}

func TestReferenceEmbeddedServerClients(t *testing.T) {
	f := fixture.NewEmbeddedServerFixture(t, embeddedConfig(), fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			t.Run("pgx_tcp", func(t *testing.T) {
				c, err := pgx.Connect(f.Ctx, b.DSN)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { embeddedCloseClient(t, c.PgConn()) })
				embeddedPGXClient(t, f.Ctx, c)
			})
			t.Run("pgx_pipe", func(t *testing.T) {
				endpoint := b
				if b.DialFunc != nil {
					cfg := embeddedConfig()
					cfg.Listen = ""
					pipe := fixture.NewEmbeddedServerFixture(t, cfg, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
					endpoint = pipe.Backends()[0]
					if pipe.Server.Addr() != nil {
						t.Fatal("pipe-only server opened TCP")
					}
					if _, err := pipe.Server.DSN(fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase); err == nil {
						t.Fatal("pipe-only DSN succeeded")
					}
				}
				embeddedPGXClient(t, f.Ctx, embeddedConnect(t, f.Ctx, endpoint, true))
			})
			t.Run("pgxpool", func(t *testing.T) {
				cfg, err := pgxpool.ParseConfig(b.DSN)
				if err != nil {
					t.Fatal(err)
				}
				cfg.ConnConfig.DialFunc = b.DialFunc
				// ParseConfig's default dialer is required for the actual oracle TCP route.
				if b.DialFunc == nil {
					cfg, err = pgxpool.ParseConfig(b.DSN)
					if err != nil {
						t.Fatal(err)
					}
				}
				pool, err := pgxpool.NewWithConfig(f.Ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer pool.Close()
				c, err := pool.Acquire(f.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Release()
				embeddedPGXClient(t, f.Ctx, c.Conn())
			})
			t.Run("pgconn", func(t *testing.T) {
				cfg, err := pgconn.ParseConfig(b.DSN)
				if err != nil {
					t.Fatal(err)
				}
				if b.DialFunc != nil {
					cfg.DialFunc = b.DialFunc
				}
				c, err := pgconn.ConnectConfig(f.Ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { embeddedCloseClient(t, c) })
				results, err := c.Exec(f.Ctx, "SELECT 42").ReadAll()
				if err != nil || len(results) != 1 || !reflect.DeepEqual(results[0].Rows, [][][]byte{{[]byte("42")}}) {
					t.Fatalf("simple: %v %v", results, err)
				}
				r := c.ExecParams(f.Ctx, "SELECT $1::integer + 1", [][]byte{[]byte("41")}, []uint32{23}, nil, nil).Read()
				if r.Err != nil || !reflect.DeepEqual(r.Rows, [][][]byte{{[]byte("42")}}) {
					t.Fatalf("params: %+v", r)
				}
				if _, err := c.Prepare(f.Ctx, "embedded_held", "SELECT $1::integer + 1", []uint32{23}); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					r = c.ExecPrepared(f.Ctx, "embedded_held", [][]byte{[]byte("41")}, nil, nil).Read()
					if r.Err != nil || !reflect.DeepEqual(r.Rows, [][][]byte{{[]byte("42")}}) {
						t.Fatalf("held: %+v", r)
					}
				}
				r = c.ExecParams(f.Ctx, "SELECT $1::integer AS answer", [][]byte{{0, 0, 0, 42}}, []uint32{23}, []int16{1}, []int16{1}).Read()
				if r.Err != nil || r.CommandTag.String() != "SELECT 1" || len(r.FieldDescriptions) != 1 || r.FieldDescriptions[0].Name != "answer" || r.FieldDescriptions[0].DataTypeOID != 23 || r.FieldDescriptions[0].Format != 1 || !reflect.DeepEqual(r.Rows, [][][]byte{{{0, 0, 0, 42}}}) {
					t.Fatalf("binary: %+v", r)
				}
				_, err = c.Exec(f.Ctx, "SELECT 1 / 0").ReadAll()
				embeddedDiagnostic(t, err, "22012", "division by zero")
				results, err = c.Exec(f.Ctx, "SELECT 42").ReadAll()
				if err != nil || len(results) != 1 || string(results[0].Rows[0][0]) != "42" {
					t.Fatalf("recovery: %v %v", results, err)
				}
			})
			t.Run("pq", func(t *testing.T) {
				connector, err := pq.NewConnector(b.DSN)
				if err != nil {
					t.Fatal(err)
				}
				db := sql.OpenDB(connector)
				defer db.Close()
				c, err := db.Conn(f.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				scalar := func(q string, args ...any) {
					t.Helper()
					var n int
					if err := c.QueryRowContext(f.Ctx, q, args...).Scan(&n); err != nil || n != 42 {
						t.Fatalf("%s: %d %v", q, n, err)
					}
				}
				scalar("SELECT 42")
				scalar("SELECT $1::integer + 1", 41)
				held, err := c.PrepareContext(f.Ctx, "SELECT $1::integer + 1")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				for range 2 {
					var n int
					if err := held.QueryRowContext(f.Ctx, 41).Scan(&n); err != nil || n != 42 {
						t.Fatalf("held: %d %v", n, err)
					}
				}
				var data []byte
				if err := c.QueryRowContext(f.Ctx, "SELECT $1::bytea", []byte{0, 255}).Scan(&data); err != nil || !bytes.Equal(data, []byte{0, 255}) {
					t.Fatalf("bytea: %x %v", data, err)
				}
				_, err = c.ExecContext(f.Ctx, "SELECT 1 / 0")
				embeddedDiagnostic(t, err, "22012", "division by zero")
				scalar("SELECT 42")
			})
		})
	}
}

func TestReferenceEmbeddedServerCopy(t *testing.T) {
	cfg := embeddedConfig()
	cfg.Listen = ""
	f := fixture.NewEmbeddedServerFixture(t, cfg, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := embeddedConnect(t, f.Ctx, b, true)
			embeddedExec(t, f.Ctx, c, "CREATE TABLE embedded_copy(id integer, v text)")
			n, err := c.CopyFrom(f.Ctx, pgx.Identifier{"embedded_copy"}, []string{"id", "v"}, pgx.CopyFromRows([][]any{{int32(1), "alpha"}, {int32(2), nil}, {int32(3), ""}}))
			if err != nil || n != 3 {
				t.Fatalf("CopyFrom: %d %v", n, err)
			}
			rows, err := c.Query(f.Ctx, "SELECT id,v FROM embedded_copy ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			var got [][]any
			for rows.Next() {
				values, err := rows.Values()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				got = append(got, values)
			}
			rows.Close()
			if rows.Err() != nil || !reflect.DeepEqual(got, [][]any{{int32(1), "alpha"}, {int32(2), nil}, {int32(3), ""}}) {
				t.Fatalf("copied rows: %#v %v", got, rows.Err())
			}
			var out bytes.Buffer
			tag, err := c.PgConn().CopyTo(f.Ctx, &out, "COPY (SELECT id,v FROM embedded_copy ORDER BY id) TO STDOUT")
			if err != nil || tag.String() != "COPY 3" || tag.RowsAffected() != 3 || out.String() != "1\talpha\n2\t\\N\n3\t\n" {
				t.Fatalf("CopyTo: %s %q %v", tag, out.String(), err)
			}
		})
	}
}
