//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerAbortedDescribe(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_aborted_describe(v integer)"), "")
			for _, c := range []struct {
				name                        string
				kind                        byte
				object, tags, code, message string
				parameters                  []byte
			}{
				{"statement_rows", 'S', "rows", "EZ", "25P02", "current transaction is aborted, commands ignored until end of transaction block", nil},
				{"portal_rows", 'P', "rows", "EZ", "25P02", "current transaction is aborted, commands ignored until end of transaction block", nil},
				{"statement_nodata", 'S', "nodata", "tnZ", "", "", []byte{0, 1, 0, 0, 0, 23}},
				{"portal_nodata", 'P', "nodata", "nZ", "", "", nil},
				{"statement_empty", 'S', "empty", "tnZ", "", "", []byte{0, 0}},
				{"portal_empty", 'P', "empty", "nZ", "", "", nil},
				{"statement_missing", 'S', "missing", "EZ", "26000", "prepared statement \"missing\" does not exist", nil},
				{"portal_missing", 'P', "missing", "EZ", "34000", "portal \"missing\" does not exist", nil},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					serverWantCode(t, w.query(t, "BEGIN"), "")
					frames := serverCycle(t, w,
						serverParse("rows", "SELECT 42 AS answer"), serverBind("rows", "rows", nil, nil),
						serverParse("nodata", "INSERT INTO server_aborted_describe VALUES ($1)", 23), serverBind("nodata", "nodata", nil, [][]byte{[]byte("7")}),
						serverParse("empty", ""), serverBind("empty", "empty", nil, nil))
					serverWantTags(t, frames, "121212Z")
					serverWantCode(t, w.query(t, "SAVEPOINT mark"), "")
					serverNotificationError(t, w.query(t, "SELECT 1/0"), "22012", "division by zero", "")
					frames = serverCycle(t, w, serverDescribe(c.kind, c.object))
					serverWantTags(t, frames, c.tags)
					serverWantReady(t, frames, 'E')
					if c.code != "" {
						serverNotificationError(t, frames, c.code, c.message, "")
					} else {
						serverWantCode(t, frames, "")
					}
					if c.parameters != nil {
						found := false
						for _, fr := range frames {
							if fr.tag == 't' {
								found = true
								if string(fr.body) != string(c.parameters) {
									t.Fatalf("ParameterDescription %x want %x", fr.body, c.parameters)
								}
							}
						}
						if !found {
							t.Fatal("missing ParameterDescription")
						}
					}
					serverWantCode(t, w.query(t, "ROLLBACK TO mark"), "")
					frames = serverCycle(t, w, serverExecute("rows", 0))
					serverWantTags(t, frames, "DCZ")
					serverWantValues(t, frames, [][]string{{"42"}})
					serverWantReady(t, frames, 'T')
					serverScalar(t, peer, "SELECT count(*) FROM server_aborted_describe", "0")
					serverWantCode(t, w.query(t, "ROLLBACK"), "")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}

func TestReferenceServerParsePrecedence(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, c := range []struct {
				name, sql, code, message, position string
				aborted                            bool
			}{
				{"semantic_before_duplicate", "SELECT missing", "42703", "column \"missing\" does not exist", "8", false},
				{"valid_duplicate", "SELECT 7", "42P05", "prepared statement \"s\" already exists", "", false},
				{"aborted_before_duplicate", "SELECT 7", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", true},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					serverWantTags(t, serverCycle(t, w, serverParse("s", "SELECT 42")), "1Z")
					if c.aborted {
						serverWantCode(t, w.query(t, "BEGIN"), "")
						serverNotificationError(t, w.query(t, "SELECT 1/0"), "22012", "division by zero", "")
					}
					frames := serverCycle(t, w, serverParse("s", c.sql))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, c.code, c.message, c.position)
					if c.aborted {
						serverWantReady(t, frames, 'E')
						serverWantCode(t, w.query(t, "ROLLBACK"), "")
					} else {
						serverWantReady(t, frames, 'I')
					}
					frames = serverCycle(t, w, serverBind("old", "s", nil, nil), serverExecute("old", 0))
					serverWantTags(t, frames, "2DCZ")
					serverWantValues(t, frames, [][]string{{"42"}})
				})
			}
		})
	}
}
