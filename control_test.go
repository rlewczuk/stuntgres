package stuntgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
)

func TestControlInvalidInputs(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("invalid")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })
	for _, err := range []error{stuntgres.Reset(ctx, nil), stuntgres.Dump(ctx, nil, io.Discard), stuntgres.Restore(ctx, nil, strings.NewReader("")), stuntgres.Dump(ctx, db, nil), stuntgres.Restore(ctx, db, nil)} {
		if err == nil {
			t.Fatal("nil control input accepted")
		}
	}
	foreign := sql.OpenDB(foreignConnector{})
	defer foreign.Close()
	for _, err := range []error{stuntgres.Reset(ctx, foreign), stuntgres.Dump(ctx, foreign, io.Discard), stuntgres.Restore(ctx, foreign, strings.NewReader(""))} {
		if err == nil {
			t.Fatal("foreign connection accepted")
		}
	}
}

type foreignConnector struct{}

func (foreignConnector) Connect(context.Context) (driver.Conn, error) { return foreignConn{}, nil }
func (foreignConnector) Driver() driver.Driver                        { return foreignDriver{} }

type foreignDriver struct{}

func (foreignDriver) Open(string) (driver.Conn, error) { return foreignConn{}, nil }

type foreignConn struct{}

func (foreignConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("foreign prepare unsupported")
}
func (foreignConn) Close() error              { return nil }
func (foreignConn) Begin() (driver.Tx, error) { return nil, errors.New("foreign begin unsupported") }
