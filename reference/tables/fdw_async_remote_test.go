package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type fdwAsyncCase struct {
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	reader   *fdwAsyncReader
	holder   *sql.DB
	released bool
}

func fdwAsyncScenario(t *testing.T, body func(*testing.T, *fdwAsyncCase)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "async_source")
	r := newFDWAsyncReader(t)
	p := fixture.NewScenarioWithDriverConfig(t, r.configure)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			c := &fdwAsyncCase{p: p, b: b, conn: conn, reader: r}
			t.Cleanup(func() {
				c.release()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_, _ = conn.ExecContext(ctx, "ROLLBACK")
				_, _ = conn.ExecContext(ctx, "DROP SERVER IF EXISTS async_slow_server CASCADE")
				_, _ = conn.ExecContext(ctx, "DROP SERVER IF EXISTS async_ready_server CASCADE")
				_, _ = conn.ExecContext(ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS async_native CASCADE")
				_ = conn.Close()
				if c.holder != nil {
					_ = c.holder.Close()
				}
			})
			c.setup(t)
			body(t, c)
		})
	}
}
func (c *fdwAsyncCase) run(t *testing.T, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, c.p.Ctx, c.conn, c.b.Recorder, s)
}
func (c *fdwAsyncCase) source(t *testing.T, q string) {
	t.Helper()
	db := c.reader.source
	if c.b.PG {
		db = fixture.Admin
	}
	if _, err := db.ExecContext(c.p.Ctx, q); err != nil {
		t.Fatalf("source %s: %v", q, err)
	}
}
func (c *fdwAsyncCase) setup(t *testing.T) {
	c.source(t, "CREATE SCHEMA async_source")
	c.source(t, "CREATE TABLE async_source.slow(id integer,label text)")
	c.source(t, "INSERT INTO async_source.slow VALUES(1,'slow'),(1,'slow'),(2,NULL)")
	c.source(t, "CREATE TABLE async_source.ready(id integer,label text)")
	c.source(t, "INSERT INTO async_source.ready VALUES(3,'ready'),(4,'tail')")
	c.source(t, "CREATE TABLE async_source.third(id integer,label text)")
	c.source(t, "INSERT INTO async_source.third VALUES(5,'third')")
	if c.b.PG {
		c.source(t, "CREATE FUNCTION async_source.gated(integer) RETURNS integer LANGUAGE plpgsql VOLATILE AS $$ BEGIN PERFORM pg_advisory_xact_lock(7182401); RETURN $1; END $$")
		c.source(t, "CREATE VIEW async_source.slow_gate AS SELECT async_source.gated(id) AS id,label FROM async_source.slow")
		var err error
		c.holder, err = sql.Open("postgres", fixture.DSN)
		if err != nil {
			t.Fatal(err)
		}
		c.holder.SetMaxOpenConns(1)
		if _, err = c.holder.ExecContext(c.p.Ctx, "SELECT pg_advisory_lock(7182401)"); err != nil {
			t.Fatal(err)
		}
		c.run(t, fixture.Exec("CREATE EXTENSION IF NOT EXISTS postgres_fdw", 0))
	} else {
		c.run(t, fixture.Exec("CREATE FOREIGN DATA WRAPPER async_native HANDLER fdw_async_handler VALIDATOR fdw_async_validator", 0))
	}
	for _, name := range []string{"slow", "ready"} {
		wrapper := "async_native"
		options := "async_capable 'true'"
		if c.b.PG {
			wrapper = "postgres_fdw"
			options += " ,host '127.0.0.1',port '5432',dbname 'stuntgres_reference',application_name 'async_" + name + "_remote'"
		}
		c.run(t, fixture.Exec("CREATE SERVER async_"+name+"_server FOREIGN DATA WRAPPER "+wrapper+" OPTIONS("+options+")", 0))
		mapping := ""
		if c.b.PG {
			mapping = " OPTIONS(user 'stuntgres',password 'stuntgres')"
		}
		c.run(t, fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER async_"+name+"_server"+mapping, 0))
		remote := name
		if c.b.PG && name == "slow" {
			remote = "slow_gate"
		}
		c.run(t, fixture.Exec("CREATE FOREIGN TABLE async_"+name+"(id integer,label text) SERVER async_"+name+"_server OPTIONS(schema_name 'async_source',table_name '"+remote+"',fetch_size '1')", 0))
	}
	c.run(t, fixture.Exec("CREATE FOREIGN TABLE async_third(id integer,label text) SERVER async_ready_server OPTIONS(schema_name 'async_source',table_name 'third',fetch_size '1')", 0))
}
func (c *fdwAsyncCase) release() {
	if c.released {
		return
	}
	c.released = true
	if c.b.PG && c.holder != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.holder.ExecContext(ctx, "SELECT pg_advisory_unlock(7182401)")
	} else {
		c.reader.unblock()
	}
}

// This observes completed FETCH, not DECLARE or merely worker admission. The
// slow backend must simultaneously be blocked behind the real advisory holder.
func (c *fdwAsyncCase) witness(ctx context.Context, both bool) error {
	if !c.b.PG {
		select {
		case <-c.reader.slow:
		case <-ctx.Done():
			return fmt.Errorf("slow HTTP response admission: %w", ctx.Err())
		}
		if both {
			select {
			case <-c.reader.ready:
			case <-ctx.Done():
				return fmt.Errorf("ready HTTP completion while slow withheld: %w", ctx.Err())
			}
		}
		return nil
	}
	timer := time.NewTicker(time.Millisecond)
	defer timer.Stop()
	for {
		var slow, ready bool
		err := fixture.Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.application_name='async_slow_remote' AND a.state='active' AND a.query LIKE 'FETCH %' AND l.locktype='advisory' AND NOT l.granted AND cardinality(pg_blocking_pids(a.pid))>0), EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name='async_ready_remote' AND state='idle in transaction' AND query LIKE 'FETCH %')`).Scan(&slow, &ready)
		if err != nil {
			return err
		}
		if slow && (!both || ready) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("FETCH witness slow=%v ready=%v: %w", slow, ready, ctx.Err())
		case <-timer.C:
		}
	}
}
func (c *fdwAsyncCase) idle(t *testing.T, scans bool) {
	t.Helper()
	if !c.b.PG {
		active, open, _ := c.reader.counts()
		if active != 0 || scans && open != 0 {
			t.Fatalf("remote resources: active=%d cursors=%d", active, open)
		}
		return
	}
	var active int
	if err := fixture.Admin.QueryRowContext(c.p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE application_name IN ('async_slow_remote','async_ready_remote') AND state='active'").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("%d active remote requests", active)
	}
}
