package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Each projection exercises the same scenarios and demands its own named view.
// SYS fixtures are genuine owned system-namespace heaps.
type tableStatFamily struct{ system, user bool }

func (f tableStatFamily) view() string {
	if f.system {
		return "pg_stat_sys_tables"
	}
	if f.user {
		return "pg_stat_user_tables"
	}
	return "pg_stat_all_tables"
}
func (f tableStatFamily) query() string {
	return "SELECT * FROM pg_catalog." + f.view() + " WHERE relname=$1"
}
func (f tableStatFamily) setup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	if f.system {
		fixture.StatisticsExec(t, p, b, c, "SET search_path=information_schema,public", 0)
	}
}
func (f tableStatFamily) scenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		f.setup(t, p, b, c)
		body(t, p, b, c)
	})
}
func (f tableStatFamily) reporting(t *testing.T, p *fixture.Pair, b fixture.Backend) (*sql.Conn, func()) {
	t.Helper()
	c, retire := fixture.StatisticsReportingConn(t, p, b)
	f.setup(t, p, b, c)
	return c, retire
}
func (f tableStatFamily) connection(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c := fixture.IdentityConnection(t, p.Ctx, b.DB)
	f.setup(t, p, b, c)
	return c
}
func (f tableStatFamily) create(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	if f.system {
		qualified := name
		if !strings.Contains(name, ".") {
			qualified = "information_schema." + name
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			c, err := b.DB.Conn(ctx)
			if err != nil {
				t.Error("owned SYS cleanup connection", err)
				return
			}
			defer c.Close()
			if _, err := c.ExecContext(ctx, "DROP TABLE IF EXISTS "+qualified+" CASCADE"); err != nil {
				t.Error("owned SYS cleanup", err)
			}
		})
	}
	tableStatCreate(t, p, b, c, name)
}
func (f tableStatFamily) read(ctx context.Context, c *sql.Conn, name string) (tableStatRow, error) {
	var r tableStatRow
	err := c.QueryRowContext(ctx, f.query(), name).Scan(r.destinations()...)
	return r, err
}
func (f tableStatFamily) get(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) tableStatRow {
	t.Helper()
	r, err := f.read(p.Ctx, c, name)
	if err != nil {
		t.Fatal(f.view(), name, err)
	}
	return r
}
func (f tableStatFamily) await(t *testing.T, p *fixture.Pair, b fixture.Backend, name, label string, accept func(tableStatRow) bool) tableStatRow {
	t.Helper()
	return tableStatAwaitView(t, p, b, f.view(), name, label, accept)
}
func (f tableStatFamily) seed(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) {
	t.Helper()
	w, retire := f.reporting(t, p, b)
	fixture.StatisticsExec(t, p, b, w, "INSERT INTO "+name+" VALUES(1,'a'),(2,NULL),(3,'c')", 3)
	retire()
	f.await(t, p, b, name, "published inserts3", func(r tableStatRow) bool { return r.Inserts == 3 })
}
func (f tableStatFamily) build(t *testing.T, p *fixture.Pair, b fixture.Backend, name string, scans, tuples int64, commands ...string) tableStatRow {
	t.Helper()
	w, retire := f.reporting(t, p, b)
	for _, q := range commands {
		fixture.StatisticsExec(t, p, b, w, q, 0)
	}
	retire()
	return f.await(t, p, b, name, "published utility heap scans", func(r tableStatRow) bool { return r.Seq == scans && r.Read == tuples })
}
