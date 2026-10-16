//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerNullableBinaryProviders(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, explicit := range []bool{false, true} {
				suffix := "implicit"
				if explicit {
					suffix = "explicit"
				}
				t.Run("null_binary_input/"+suffix, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					if explicit {
						serverWantCode(t, w.query(t, "BEGIN"), "")
					}
					frames := serverCycle(t, w, serverParse("acl", "SELECT $1 AS v", 1033), serverBind("", "acl", []int{1}, [][]byte{nil}), serverDescribe('P', ""), serverExecute("", 0))
					serverWantTags(t, frames, "1EZ")
					serverNotificationError(t, frames, "42883", "no binary input function available for type aclitem", "", "unnamed portal parameter $1")
					serverWantCompletions(t, frames)
					state := byte('I')
					if explicit {
						state = 'E'
					}
					serverWantReady(t, frames, state)
					if explicit {
						serverWantCode(t, w.query(t, "ROLLBACK"), "")
					}
					serverScalar(t, w, "SELECT 42", "42")
				})
				for _, empty := range []bool{false, true} {
					name := "null_binary_output/" + suffix
					if empty {
						name = "empty_binary_output/" + suffix
					}
					t.Run(name, func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(b))
						if explicit {
							serverWantCode(t, w.query(t, "BEGIN"), "")
						}
						sql := "SELECT $1 AS v"
						if empty {
							sql += " WHERE false"
						}
						frames := serverCycle(t, w, serverParse("acl", sql, 1033), serverBind("", "acl", nil, [][]byte{nil}, 1), serverDescribe('P', ""), serverExecute("", 0))
						// RowDescription itself is permitted. Send-provider lookup belongs to
						// the first actual tuple, even if that tuple contains only a NULL datum.
						columns := serverColumns(t, frames)
						expected := serverColumn{"v", 0, 0, 1033, 16, -1, 1}
						if len(columns) != 1 || columns[0] != expected {
							t.Fatalf("ACLITEM metadata %+v want %+v", columns, expected)
						}
						state := byte('I')
						if explicit {
							state = 'T'
						}
						if empty {
							serverWantTags(t, frames, "12TCZ")
							serverWantCode(t, frames, "")
							serverWantCompletions(t, frames, "SELECT 0")
						} else {
							serverWantTags(t, frames, "12TEZ")
							serverNotificationError(t, frames, "42883", "no binary output function available for type aclitem", "")
							serverWantCompletions(t, frames)
							if explicit {
								state = 'E'
							}
						}
						if len(serverRawRows(t, frames)) != 0 {
							t.Fatal("missing binary provider emitted a DataRow")
						}
						serverWantReady(t, frames, state)
						if explicit {
							serverWantCode(t, w.query(t, "ROLLBACK"), "")
						}
						serverScalar(t, w, "SELECT 42", "42")
					})
				}
			}
		})
	}
}
