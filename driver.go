// Package stuntgres implements a process-local database/sql driver for a bounded
// PostgreSQL SQL subset. Importing it registers the driver name "stuntgres".
package stuntgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqlstate"
	"sync"
)

// Error reports a PostgreSQL SQLSTATE without depending on a network driver.
type Error = sqlstate.Error

// Driver owns independent databases named by exact, opaque DSN strings.
// Databases live for the lifetime of their Driver, not their connections.
type Driver struct {
	mu      sync.Mutex
	engines map[string]*engine.Engine
}

func NewDriver() *Driver { return &Driver{} }
func init()              { sql.Register("stuntgres", NewDriver()) }
func (d *Driver) Open(name string) (driver.Conn, error) {
	c, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return c.Connect(context.Background())
}
func (d *Driver) OpenConnector(name string) (driver.Connector, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.engines == nil {
		d.engines = make(map[string]*engine.Engine)
	}
	e := d.engines[name]
	if e == nil {
		e = engine.New()
		d.engines[name] = e
	}
	return &connector{owner: d, engine: e}, nil
}

type connector struct {
	owner  *Driver
	engine *engine.Engine
}

func (c *connector) Driver() driver.Driver { return c.owner }
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &conn{engine: c.engine}, nil
}

var _ driver.DriverContext = (*Driver)(nil)
