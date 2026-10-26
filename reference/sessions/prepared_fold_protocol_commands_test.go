//go:build !noservertests

package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// These expectations follow the Bind planning boundary, not the current native
// command dispatch: point_out at digits3 is long even if Execute sees digits0.
// The nested immutable length is 23 versus 7. No INSERT RETURNING is required.
func TestReferencePreparedFoldProtocolCommands(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, kind := range []string{"insert", "update", "delete", "merge", "modifying_cte"} {
				for _, nested := range []bool{false, true} {
					label := "text"
					if nested {
						label = "nested"
					}
					for _, phase := range []string{"before_first_bind", "rebind_describe_discard", "replace_with_old_portal"} {
						t.Run(kind+"/"+label+"/"+phase, func(t *testing.T) {
							w := serverAuthenticated(t, f.DSNFor(b))
							// The existing wire helper owns socket cleanup; a failed case
							// rolls back its private table when that socket closes.
							command := func(sql, tag string, state byte) {
								t.Helper()
								frames := w.query(t, sql)
								preparedFoldWireCheck(t, frames, "CZ", state)
								serverWantCompletions(t, frames, tag)
							}
							expression := "(point '0.10000000000000002,1')::text"
							typ, column, seed := "text", "p", "'seed'"
							longSQL, shortSQL := "'(0.10000000000000002,1)'", "'(0.1,1)'"
							longValue, shortValue := "(0.10000000000000002,1)", "(0.1,1)"
							if nested {
								expression = "length(" + expression + ")"
								typ, column, seed = "integer", "n", "0"
								longSQL, shortSQL = "23", "7"
								longValue, shortValue = "23", "7"
							}
							query, completion := "", ""
							switch kind {
							case "insert":
								query = "INSERT INTO fold_command(id,v) VALUES(1," + expression + ")"
								completion = "INSERT 0 1"
							case "update":
								query = "UPDATE fold_command SET v=" + expression + " WHERE id=1"
								completion = "UPDATE 1"
							case "delete":
								query = "DELETE FROM fold_command WHERE v=" + expression
								completion = "DELETE 1"
							case "merge":
								query = "MERGE INTO fold_command AS dst USING (VALUES(1)) AS src(id) ON dst.id=src.id WHEN MATCHED THEN UPDATE SET v=" + expression
								completion = "MERGE 1"
							case "modifying_cte":
								// This is a SELECT syntactically, but not a plain SELECT
								// portal: its UPDATE must also plan without effects.
								query = "WITH changed AS (UPDATE fold_command SET v=" + expression + " WHERE id=1 RETURNING v) SELECT coalesce(v,v) AS " + column + " FROM changed"
								completion = "SELECT 1"
							}
							command("SET extra_float_digits TO 3", "SET", 'I')
							command("BEGIN", "BEGIN", 'T')
							command("CREATE TABLE fold_command(id integer,v "+typ+")", "CREATE TABLE", 'T')
							var baseline [][]string
							reset := func() {
								t.Helper()
								frames := w.query(t, "DELETE FROM fold_command")
								preparedFoldWireCheck(t, frames, "CZ", 'T')
								baseline = nil
								if kind == "delete" {
									command("INSERT INTO fold_command VALUES(1,"+longSQL+"),(2,"+shortSQL+")", "INSERT 0 2", 'T')
									baseline = [][]string{{"1", longValue}, {"2", shortValue}}
								} else if kind != "insert" {
									command("INSERT INTO fold_command VALUES(1,"+seed+")", "INSERT 0 1", 'T')
									value := "seed"
									if nested {
										value = "0"
									}
									baseline = [][]string{{"1", value}}
								}
							}
							inspect := func(want [][]string) {
								t.Helper()
								frames := w.query(t, "SELECT id,v FROM fold_command ORDER BY id")
								tags := "TCZ"
								for range want {
									tags = tags[:len(tags)-2] + "DCZ"
								}
								preparedFoldWireCheck(t, frames, tags, 'T')
								if got := serverValues(t, frames); !reflect.DeepEqual(got, want) {
									t.Fatalf("table rows=%v want=%v", got, want)
								}
							}
							reset()
							inspect(baseline)
							metadata := func(frames []serverFrame) {
								t.Helper()
								if kind == "modifying_cte" {
									preparedFoldWireColumns(t, frames, nested)
								} else if got := serverDescriptions(t, frames); len(got) != 0 {
									t.Fatalf("command has row descriptions: %v", got)
								}
							}
							describe := func(parse bool) {
								t.Helper()
								packets := [][]byte{serverDescribe('S', "fold_command_stmt")}
								tags := "tnZ"
								if kind == "modifying_cte" {
									tags = "tTZ"
								}
								if parse {
									packets = append([][]byte{serverParse("fold_command_stmt", query)}, packets...)
									tags = "1" + tags
								}
								frames := serverCycle(t, w, packets...)
								preparedFoldWireCheck(t, frames, tags, 'T')
								parameterIndex := 0
								if parse {
									parameterIndex = 1
								}
								if !reflect.DeepEqual(frames[parameterIndex].body, []byte{0, 0}) {
									t.Fatalf("parameter description=%x", frames[parameterIndex].body)
								}
								metadata(frames)
								inspect(baseline)
							}
							bind := func(portal string) {
								t.Helper()
								frames := serverCycle(t, w, serverBind(portal, "fold_command_stmt", nil, nil), serverDescribe('P', portal))
								tags := "2nZ"
								if kind == "modifying_cte" {
									tags = "2TZ"
								}
								preparedFoldWireCheck(t, frames, tags, 'T')
								metadata(frames)
								// Inspect on the SAME wire/TX: a separate observer could
								// miss accidental uncommitted effects during Bind.
								inspect(baseline)
							}
							execute := func(portal string, long bool) {
								t.Helper()
								value := shortValue
								if long {
									value = longValue
								}
								frames := serverCycle(t, w, serverExecute(portal, 0))
								tags := "CZ"
								var wantRows [][]string
								if kind == "modifying_cte" {
									tags = "DCZ"
									wantRows = [][]string{{value}}
								}
								preparedFoldWireCheck(t, frames, tags, 'T')
								serverWantCompletions(t, frames, completion)
								if got := serverValues(t, frames); !reflect.DeepEqual(got, wantRows) {
									t.Fatalf("Execute rows=%v want=%v", got, wantRows)
								}
								wantTable := [][]string{{"1", value}}
								if kind == "delete" {
									wantTable = [][]string{{"1", longValue}}
									if long {
										wantTable = [][]string{{"2", shortValue}}
									}
								}
								inspect(wantTable)
								preparedFoldWireCheck(t, serverCycle(t, w, serverClose('P', portal)), "3Z", 'T')
								// Ordinary writes do not retire the definition lifetime.
								reset()
							}
							describe(true)
							if phase == "before_first_bind" {
								command("SET extra_float_digits TO 0", "SET", 'T')
								bind("fold_first")
								execute("fold_first", false)
							} else {
								bind("fold_old")
								command("SET extra_float_digits TO 0", "SET", 'T')
								if phase == "replace_with_old_portal" {
									preparedFoldWireCheck(t, serverCycle(t, w, serverClose('S', "fold_command_stmt")), "3Z", 'T')
									describe(true)
									bind("fold_new")
									execute("fold_old", true)
									execute("fold_new", false)
									// Executing the retired owner's portal must not publish
									// its long candidate into the replacement definition.
									bind("fold_after_old")
									execute("fold_after_old", false)
								} else {
									execute("fold_old", true)
									describe(false)
									bind("fold_again")
									execute("fold_again", true)
									command("DISCARD PLANS", "DISCARD PLANS", 'T')
									bind("fold_discarded")
									execute("fold_discarded", false)
								}
							}
							preparedFoldWireCheck(t, serverCycle(t, w, serverClose('S', "fold_command_stmt")), "3Z", 'T')
							command("ROLLBACK", "ROLLBACK", 'I')
						})
					}
				}
			}
		})
	}
}
