//go:build !noservertests

package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func preparedFoldWireColumns(t *testing.T, frames []serverFrame, nested bool) {
	t.Helper()
	want := serverColumn{name: "p", oid: 25, length: -1, typmod: -1}
	if nested {
		want = serverColumn{name: "n", oid: 23, length: 4, typmod: -1}
	}
	if got := serverDescriptions(t, frames); !reflect.DeepEqual(got, [][]serverColumn{{want}}) {
		t.Fatalf("descriptions=%v want=%v", got, want)
	}
}

func preparedFoldWireCheck(t *testing.T, frames []serverFrame, tags string, state byte) {
	t.Helper()
	serverWantCode(t, frames, "")
	serverWantTags(t, frames, tags)
	if len(frames) == 0 || string(frames[len(frames)-1].body) != string(state) {
		t.Fatalf("ReadyForQuery=%s want=%c", serverFrameSummary(frames), state)
	}
}

func preparedFoldWireRead(t *testing.T, w *serverWire, portal string, nested, long bool) {
	t.Helper()
	frames := serverCycle(t, w, serverDescribe('P', portal), serverExecute(portal, 0))
	preparedFoldWireCheck(t, frames, "TDCZ", 'T')
	preparedFoldWireColumns(t, frames, nested)
	value := "(0.1,1)"
	if long {
		value = "(0.10000000000000002,1)"
	}
	if nested {
		value = "7"
		if long {
			value = "23"
		}
	}
	if got := serverValues(t, frames); !reflect.DeepEqual(got, [][]string{{value}}) {
		t.Fatalf("rows=%v want=%q", got, value)
	}
	if string(frames[2].body) != "SELECT 1\x00" {
		t.Fatalf("command=%q want SELECT 1", frames[2].body)
	}
}

func TestReferencePreparedFoldProtocolLifetime(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, nested := range []bool{false, true} {
				label, query := "text", preparedFoldPointSQL
				if nested {
					label, query = "nested", preparedFoldLengthSQL
				}
				for _, phase := range []string{"before_first_bind", "rebind_describe_discard", "replace_with_old_portal"} {
					t.Run(label+"/"+phase, func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(b))
						// serverDial independently registers socket cleanup. There are no
						// workers to join, and closing the session rolls back a failed TX.
						command := func(sql, tag string, state byte) {
							t.Helper()
							frames := w.query(t, sql)
							preparedFoldWireCheck(t, frames, "CZ", state)
							if string(frames[0].body) != tag+"\x00" {
								t.Fatalf("command=%q want=%q", frames[0].body, tag)
							}
						}
						parse := func() {
							t.Helper()
							frames := serverCycle(t, w, serverParse("fold_wire", query), serverDescribe('S', "fold_wire"))
							preparedFoldWireCheck(t, frames, "1tTZ", 'T')
							if !reflect.DeepEqual(frames[1].body, []byte{0, 0}) {
								t.Fatalf("parameter description=%x", frames[1].body)
							}
							preparedFoldWireColumns(t, frames, nested)
						}
						bind := func(portal string) {
							t.Helper()
							frames := serverCycle(t, w, serverBind(portal, "fold_wire", nil, nil), serverDescribe('P', portal))
							preparedFoldWireCheck(t, frames, "2TZ", 'T')
							preparedFoldWireColumns(t, frames, nested)
						}
						closePortal := func(portal string) {
							t.Helper()
							preparedFoldWireCheck(t, serverCycle(t, w, serverClose('P', portal)), "3Z", 'T')
						}
						command("SET extra_float_digits TO 3", "SET", 'I')
						command("BEGIN", "BEGIN", 'T')
						parse()
						if phase == "before_first_bind" {
							command("SET extra_float_digits TO 0", "SET", 'T')
							bind("fold_first")
							preparedFoldWireRead(t, w, "fold_first", nested, false)
							closePortal("fold_first")
						} else {
							// Bind, not Execute, is the proposed planning boundary.
							bind("fold_old")
							command("SET extra_float_digits TO 0", "SET", 'T')
							if phase == "replace_with_old_portal" {
								preparedFoldWireCheck(t, serverCycle(t, w, serverClose('S', "fold_wire")), "3Z", 'T')
								parse()
								bind("fold_new")
								// A planned portal keeps its old owner even after the
								// statement name has been closed and reused.
								preparedFoldWireRead(t, w, "fold_old", nested, true)
								preparedFoldWireRead(t, w, "fold_new", nested, false)
								closePortal("fold_old")
								closePortal("fold_new")
								bind("fold_after_old")
								preparedFoldWireRead(t, w, "fold_after_old", nested, false)
								closePortal("fold_after_old")
							} else {
								preparedFoldWireRead(t, w, "fold_old", nested, true)
								closePortal("fold_old")
								frames := serverCycle(t, w, serverDescribe('S', "fold_wire"))
								preparedFoldWireCheck(t, frames, "tTZ", 'T')
								if !reflect.DeepEqual(frames[0].body, []byte{0, 0}) {
									t.Fatalf("parameter description=%x", frames[0].body)
								}
								preparedFoldWireColumns(t, frames, nested)
								bind("fold_again")
								preparedFoldWireRead(t, w, "fold_again", nested, true)
								closePortal("fold_again")
								command("DISCARD PLANS", "DISCARD PLANS", 'T')
								bind("fold_discarded")
								preparedFoldWireRead(t, w, "fold_discarded", nested, false)
								closePortal("fold_discarded")
							}
						}
						preparedFoldWireCheck(t, serverCycle(t, w, serverClose('S', "fold_wire")), "3Z", 'T')
						command("ROLLBACK", "ROLLBACK", 'I')
					})
				}
			}
		})
	}
}
