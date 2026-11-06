package dual

import (
	"context"
	"database/sql/driver"
)

// database/sql chooses code paths by the presence of SessionResetter,
// Validator, NamedValueChecker (conn) and NamedValueChecker, ColumnConverter
// (stmt). The wrappers expose each exactly when the native object has it: the
// method carriers below embed beside *conn or *stmt in one variant per subset.

type resetter struct{ c *conn }

func (r resetter) ResetSession(ctx context.Context) error { return r.c.resetSession(ctx) }

type validator struct{ c *conn }

func (v validator) IsValid() bool { return v.c.isValid() }

type connChecker struct{ c *conn }

func (k connChecker) CheckNamedValue(nv *driver.NamedValue) error {
	return k.c.native.(driver.NamedValueChecker).CheckNamedValue(nv)
}

type (
	connR struct {
		*conn
		resetter
	}
	connV struct {
		*conn
		validator
	}
	connK struct {
		*conn
		connChecker
	}
	connRV struct {
		*conn
		resetter
		validator
	}
	connRK struct {
		*conn
		resetter
		connChecker
	}
	connVK struct {
		*conn
		validator
		connChecker
	}
	connRVK struct {
		*conn
		resetter
		validator
		connChecker
	}
)

func wrapConn(c *conn) driver.Conn {
	_, r := c.native.(driver.SessionResetter)
	_, v := c.native.(driver.Validator)
	_, k := c.native.(driver.NamedValueChecker)
	switch {
	case r && v && k:
		return &connRVK{c, resetter{c}, validator{c}, connChecker{c}}
	case r && v:
		return &connRV{c, resetter{c}, validator{c}}
	case r && k:
		return &connRK{c, resetter{c}, connChecker{c}}
	case v && k:
		return &connVK{c, validator{c}, connChecker{c}}
	case r:
		return &connR{c, resetter{c}}
	case v:
		return &connV{c, validator{c}}
	case k:
		return &connK{c, connChecker{c}}
	}
	return c
}

type stmtChecker struct{ s *stmt }

func (k stmtChecker) CheckNamedValue(nv *driver.NamedValue) error {
	return k.s.native.(driver.NamedValueChecker).CheckNamedValue(nv)
}

type converter struct{ s *stmt }

func (v converter) ColumnConverter(idx int) driver.ValueConverter {
	return v.s.native.(driver.ColumnConverter).ColumnConverter(idx)
}

type (
	stmtK struct {
		*stmt
		stmtChecker
	}
	stmtC struct {
		*stmt
		converter
	}
	stmtKC struct {
		*stmt
		stmtChecker
		converter
	}
)

func wrapStmt(s *stmt) driver.Stmt {
	_, k := s.native.(driver.NamedValueChecker)
	_, v := s.native.(driver.ColumnConverter)
	switch {
	case k && v:
		return &stmtKC{s, stmtChecker{s}, converter{s}}
	case k:
		return &stmtK{s, stmtChecker{s}}
	case v:
		return &stmtC{s, converter{s}}
	}
	return s
}

var (
	_ driver.ConnPrepareContext = (*conn)(nil)
	_ driver.ExecerContext      = (*conn)(nil)
	_ driver.QueryerContext     = (*conn)(nil)
	_ driver.ConnBeginTx        = (*conn)(nil)
	_ driver.Pinger             = (*conn)(nil)
	_ driver.StmtExecContext    = (*stmt)(nil)
	_ driver.StmtQueryContext   = (*stmt)(nil)
	_ driver.SessionResetter    = (*connRVK)(nil)
	_ driver.Validator          = (*connRVK)(nil)
	_ driver.NamedValueChecker  = (*connRVK)(nil)
	_ driver.NamedValueChecker  = (*stmtKC)(nil)
	_ driver.ColumnConverter    = (*stmtKC)(nil)
)
