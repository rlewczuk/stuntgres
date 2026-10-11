package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNoticesIsolation(t *testing.T) {
	p := fixture.NewScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			counts := [2]int{}
			var connectors [3]driver.Connector
			if backend.PG {
				base, err := pq.NewConnector(fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				connectors[0] = pq.ConnectorWithNoticeHandler(base, func(*pq.Error) { counts[0]++ })
				connectors[1] = pq.ConnectorWithNoticeHandler(base, func(*pq.Error) { counts[1]++ })
				connectors[2] = pq.ConnectorWithNoticeHandler(base, nil)
			} else {
				base := p.NativeConnector
				connectors[0] = base.WithNoticeHandler(func(stuntgres.Error) { counts[0]++ })
				connectors[1] = base.WithNoticeHandler(func(stuntgres.Error) { counts[1]++ })
				connectors[2] = base.WithNoticeHandler(nil)
			}
			var conns [3]*sql.Conn
			for i, connector := range connectors {
				db := sql.OpenDB(connector)
				t.Cleanup(func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				})
				c, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := c.Close(); err != nil {
						t.Error(err)
					}
				})
				conns[i] = c
			}
			exec := func(c *sql.Conn, q string) {
				result, err := c.ExecContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				count, err := result.RowsAffected()
				if count != 0 || err != nil {
					t.Fatal(count, err)
				}
			}
			exec(conns[0], "CREATE TABLE shared(id INT)")
			for _, c := range conns {
				exec(c, "CREATE TABLE IF NOT EXISTS shared(other TEXT)")
			}
			if counts != [2]int{1, 1} {
				t.Fatal(counts)
			}
			exec(conns[0], "SET client_min_messages TO error")
			exec(conns[0], "CREATE TABLE IF NOT EXISTS shared(id INT)")
			exec(conns[1], "CREATE TABLE IF NOT EXISTS shared(id INT)")
			if counts != [2]int{1, 2} {
				t.Fatal(counts)
			}
			for i, c := range conns {
				want := "notice"
				if i == 0 {
					want = "error"
				}
				var got string
				if err := c.QueryRowContext(p.Ctx, "SHOW client_min_messages").Scan(&got); err != nil || got != want {
					t.Fatal(i, got, err)
				}
			}
			if !backend.PG {
				// Exact opaque DSN and Driver isolation are native host controls,
				// not a fictional PostgreSQL process-local database allocator.
				for i, d := range []driver.DriverContext{p.NativeConnector.Driver().(driver.DriverContext), stuntgres.NewDriver()} {
					key := dsn
					if i == 0 {
						key += "/other"
					}
					connector, err := d.OpenConnector(key)
					if err != nil {
						t.Fatal(err)
					}
					db := sql.OpenDB(connector)
					t.Cleanup(func() { db.Close() })
					rows, err := db.QueryContext(p.Ctx, "SELECT * FROM shared")
					if rows != nil {
						rows.Close()
						t.Fatal("foreign identity returned rows", i)
					}
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "shared" does not exist`, Position: "15"})
				}
			}
		})
	}
}

func TestReferenceNoticesPhysicalReplacement(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			first, second := 0, 0
			var configured, changed driver.Connector
			if backend.PG {
				base, err := pq.NewConnector(fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				configured = pq.ConnectorWithNoticeHandler(base, func(*pq.Error) { first++ })
				changed = pq.ConnectorWithNoticeHandler(base, func(*pq.Error) { second++ })
			} else {
				original := p.NativeConnector.WithNoticeHandler(func(stuntgres.Error) { first++ })
				configured = original
				changed = original.WithNoticeHandler(func(stuntgres.Error) { second++ })
			}
			db := sql.OpenDB(configured)
			t.Cleanup(func() { db.Close() })
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			c, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { c.Close() }()
			exec := func(q string) {
				result, err := c.ExecContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				count, err := result.RowsAffected()
				if count != 0 || err != nil {
					t.Fatal(count, err)
				}
			}
			show := func(want string) {
				var got string
				if err := c.QueryRowContext(p.Ctx, "SHOW client_min_messages").Scan(&got); err != nil || got != want {
					t.Fatal(got, err)
				}
				rows, err := c.QueryContext(p.Ctx, "SHOW client_min_messages")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].Name() != "client_min_messages" || types[0].DatabaseTypeName() != "TEXT" {
					t.Fatal(types, err)
				}
				if !rows.Next() {
					t.Fatal("missing SHOW", rows.Err())
				}
				if err := rows.Scan(&got); err != nil || got != want {
					t.Fatal(got, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("SHOW exhaustion", rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			}
			exec("CREATE TABLE replacement(id INT)")
			result, err := c.ExecContext(p.Ctx, "INSERT INTO replacement VALUES(7)")
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); n != 1 || err != nil {
				t.Fatal(n, err)
			}
			exec("SET client_min_messages TO warning")
			var physical any
			if err := c.Raw(func(raw any) error { physical = raw; return nil }); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			c, err = db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Raw(func(raw any) error {
				if raw != physical {
					t.Fatal("borrower did not reuse physical session")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			show("warning")
			other := sql.OpenDB(changed)
			t.Cleanup(func() { other.Close() })
			exec("SET client_min_messages TO notice")
			exec("CREATE TABLE IF NOT EXISTS replacement(id INT)")
			if first != 1 || second != 0 {
				t.Fatal(first, second)
			}
			if _, err := other.ExecContext(p.Ctx, "CREATE TABLE IF NOT EXISTS replacement(id INT)"); err != nil {
				t.Fatal(err)
			}
			if first != 1 || second != 1 {
				t.Fatal(first, second)
			}
			exec("SET client_min_messages TO error")
			if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			c.Close()
			c, err = db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Raw(func(raw any) error {
				if raw == physical {
					t.Fatal("physical session not replaced")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			show("notice")
			exec("CREATE TABLE IF NOT EXISTS replacement(id INT)")
			if first != 2 || second != 1 {
				t.Fatal(first, second)
			}
			var got int
			if err := c.QueryRowContext(p.Ctx, "SELECT id FROM replacement").Scan(&got); err != nil || got != 7 {
				t.Fatal(got, err)
			}
		})
	}
}
