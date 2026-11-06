// Package dual provides a database/sql connector that runs every driver call
// on a native connector, mirrors it on a paired connection of a reference
// connector, returns the native outcome unchanged and logs one JSON record per
// call with the differences between both sides.
//
// The reference is any driver.Connector the caller supplies, for example
// lib/pq's pq.NewConnector or pgx's stdlib.GetConnector on a real PostgreSQL.
// The package itself depends only on the standard library. See README.md for
// the contract.
package dual

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"slices"
	"sync/atomic"
	"time"
)

type connector struct {
	native, reference driver.Connector
	opts              Options
	nextID            atomic.Uint64
	log               *logWriter
}

// NewConnector returns a connector whose connections run each call on native
// first and then on a paired connection of reference, bounded by
// opts.ReferenceTimeout. A nil reference behaves as a reference whose every
// Connect fails. NewConnector panics when native is nil.
func NewConnector(native, reference driver.Connector, opts Options) driver.Connector {
	if native == nil {
		panic("dual: nil native connector")
	}
	if opts.ReferenceTimeout <= 0 {
		opts.ReferenceTimeout = defaultReferenceTimeout
	}
	opts.Uncompared = slices.Clone(opts.Uncompared)
	return &connector{native: native, reference: reference, opts: opts, log: &logWriter{w: opts.Log}}
}

// Driver returns the native connector's driver. Its Open bypasses mirroring.
func (cn *connector) Driver() driver.Driver { return cn.native.Driver() }

// Close closes the native connector when it is an io.Closer. The reference
// connector belongs to the caller and is never closed.
func (cn *connector) Close() error {
	if c, ok := cn.native.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Connect opens a native connection and pairs it with a reference connection.
// A failed or slow reference never fails the native connection: the pair is
// marked failed (08001) and every later record of the connection says so.
func (cn *connector) Connect(ctx context.Context) (driver.Conn, error) {
	start := time.Now()
	native, err := cn.native.Connect(ctx)
	c := &conn{cn: cn, native: native, id: cn.nextID.Add(1)}
	c.out = emitter{log: cn.log, next: 1}
	e := c.open(CallConnect, start, "", nil)
	e.nerr = err
	if err != nil {
		e.rec.Unmirrored = "native connect failed"
		e.finish()
		return nil, err
	}
	c.connectReference(ctx, e)
	e.finish()
	return wrapConn(c), nil
}

func (c *conn) connectReference(ctx context.Context, e *entry) {
	if c.cn.reference == nil {
		e.failure = "reference connection failed: no reference connector"
		c.ref.fail(e.failure)
		return
	}
	timeout := c.cn.opts.ReferenceTimeout
	t := runTask(ctx, timeout, false, c.cn.reference.Connect, func(late driver.Conn) {
		if late != nil {
			closeQuietly(late)
		}
	})
	e.refDur, e.rerr = t.duration, t.err
	var reason string
	switch {
	case t.abandoned:
		reason = "reference did not respond within " + timeout.String()
	case t.panicked != nil:
		reason = fmt.Sprintf("panic: %v", t.panicked)
	case t.err != nil:
		reason = t.err.Error()
		if t.value != nil {
			go closeQuietly(t.value)
		}
	case t.value == nil:
		reason = "no connection"
	default:
		c.ref.conn = t.value
		return
	}
	e.failure = "reference connection failed: " + reason
	c.ref.fail(e.failure)
}
