package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const xactSysSelect = "SELECT * FROM pg_catalog.pg_stat_xact_sys_tables WHERE relname=$1"

func xactSysGet(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) xactTableRow {
	t.Helper()
	var r xactTableRow
	if err := c.QueryRowContext(p.Ctx, xactSysSelect, name).Scan(r.destinations()...); err != nil {
		t.Fatal(name, err)
	}
	return r
}

// Only owned ordinary tables are removed; information_schema itself is never reset.
// Register before creation so cleanup also covers an interrupted setup. Reporter
// cleanup registered later runs first, settling its owner before DROP.
func xactSysCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, schema := range []string{"information_schema", "public"} {
			if _, err := b.DB.ExecContext(ctx, "DROP TABLE IF EXISTS "+schema+"."+name+" CASCADE"); err != nil {
				t.Error("owned SYS fixture cleanup", err)
			}
		}
	})
	tableStatCreate(t, p, b, c, "information_schema."+name)
}

func xactSysSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) {
	t.Helper()
	w, retire := fixture.StatisticsReportingConn(t, p, b)
	fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema."+name+" VALUES(1,'a'),(2,NULL),(3,'c')", 3)
	retire()
	tableStatAwait(t, p, b, name, "published SYS inserts3", func(r tableStatRow) bool { return r.Inserts == 3 })
}

func xactSysCumulative(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) tableStatRow {
	t.Helper()
	var r tableStatRow
	if err := c.QueryRowContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_sys_tables WHERE relname=$1", name).Scan(r.destinations()...); err != nil {
		t.Fatal(err)
	}
	return r
}
