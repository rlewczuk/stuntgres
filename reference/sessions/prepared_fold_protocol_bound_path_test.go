//go:build !noservertests

package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// A portal's accepted occurrence identities are independent of future definition
// planning. Path changes and DISCARD must not move an already-bound INSERT.
func TestReferencePreparedFoldProtocolBoundPath(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, phase := range []string{"path_change", "discard_shadow"} {
				t.Run(phase, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					command := func(sql, tag string) {
						t.Helper()
						frames := w.query(t, sql)
						preparedFoldWireCheck(t, frames, "CZ", 'T')
						serverWantCompletions(t, frames, tag)
					}
					frames := w.query(t, "BEGIN")
					preparedFoldWireCheck(t, frames, "CZ", 'T')
					command("CREATE SCHEMA fold_bound_early", "CREATE SCHEMA")
					command("CREATE SCHEMA fold_bound_late", "CREATE SCHEMA")
					command("CREATE TABLE fold_bound_late.target(v text)", "CREATE TABLE")
					if phase == "path_change" {
						command("CREATE TABLE fold_bound_early.target(v text)", "CREATE TABLE")
					}
					path := "fold_bound_late,fold_bound_early"
					if phase == "discard_shadow" {
						path = "fold_bound_early,fold_bound_late"
					}
					frames = w.query(t, "SELECT set_config('search_path','"+path+"',false)")
					preparedFoldWireCheck(t, frames, "TDCSZ", 'T')
					if string(frames[3].body) != "search_path\x00"+path+"\x00" {
						t.Fatalf("search_path ParameterStatus=%q", frames[3].body)
					}
					command("SET extra_float_digits=3", "SET")
					frames = serverCycle(t, w, serverParse("bound_path", "INSERT INTO target VALUES((point '0.10000000000000002,1')::text)"), serverBind("old", "bound_path", nil, nil))
					preparedFoldWireCheck(t, frames, "12Z", 'T')
					inspect := func(table string, want [][]string) {
						t.Helper()
						frames := w.query(t, "SELECT v FROM "+table)
						tags := "TCZ"
						if len(want) != 0 {
							tags = "TDCZ"
						}
						preparedFoldWireCheck(t, frames, tags, 'T')
						if got := serverValues(t, frames); !reflect.DeepEqual(got, want) {
							t.Fatalf("%s rows=%v want=%v", table, got, want)
						}
					}
					inspect("fold_bound_late.target", nil)
					command("SET extra_float_digits=0", "SET")
					if phase == "path_change" {
						frames = w.query(t, "SELECT set_config('search_path','fold_bound_early,fold_bound_late',false)")
						preparedFoldWireCheck(t, frames, "TDCSZ", 'T')
						if string(frames[3].body) != "search_path\x00fold_bound_early,fold_bound_late\x00" {
							t.Fatalf("search_path ParameterStatus=%q", frames[3].body)
						}
					} else {
						command("CREATE TABLE fold_bound_early.target(v text)", "CREATE TABLE")
						command("DISCARD PLANS", "DISCARD PLANS")
					}
					frames = serverCycle(t, w, serverExecute("old", 0))
					preparedFoldWireCheck(t, frames, "CZ", 'T')
					serverWantCompletions(t, frames, "INSERT 0 1")
					inspect("fold_bound_late.target", [][]string{{"(0.10000000000000002,1)"}})
					inspect("fold_bound_early.target", nil)
					frames = serverCycle(t, w, serverBind("new", "bound_path", nil, nil), serverExecute("new", 0))
					preparedFoldWireCheck(t, frames, "2CZ", 'T')
					serverWantCompletions(t, frames, "INSERT 0 1")
					inspect("fold_bound_early.target", [][]string{{"(0.1,1)"}})
					inspect("fold_bound_late.target", [][]string{{"(0.10000000000000002,1)"}})
					frames = w.query(t, "ROLLBACK")
					preparedFoldWireCheck(t, frames, "CSZ", 'I')
					if string(frames[1].body) != "search_path\x00\"$user\", public\x00" {
						t.Fatalf("rollback search_path ParameterStatus=%q", frames[1].body)
					}
				})
			}
		})
	}
}
