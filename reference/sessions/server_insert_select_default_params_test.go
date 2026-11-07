//go:build !noservertests

package reference_test

import (
	"fmt"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerInsertSelectDefaultParamsRaw(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			dsn := f.DSNFor(backend)
			for _, named := range []bool{false, true} {
				for _, explicit := range []bool{false, true} {
					for _, zeroHints := range []bool{false, true} {
						label := fmt.Sprintf("named_%t/transaction_%t/zero_hints_%t", named, explicit, zeroHints)
						t.Run(label, func(t *testing.T) {
							w, peer := serverAuthenticated(t, dsn), serverAuthenticated(t, dsn)
							table := fmt.Sprintf("raw_minimal_%t_%t_%t", named, explicit, zeroHints)
							insertDefaultCommand(t, w, "CREATE TABLE "+table+" (id int DEFAULT 7, c int)", "CREATE TABLE", 'I')
							ready := byte('I')
							if explicit {
								insertDefaultCommand(t, w, "BEGIN", "BEGIN", 'T')
								ready = 'T'
							}
							name := ""
							if named {
								name = "insert_minimal"
							}
							insertDefaultPrepare(t, w, name, "INSERT INTO "+table+"(c) SELECT $1", []uint32{23}, zeroHints, ready)
							insertDefaultHealthy(t, w, table, 0, ready)
							serverScalar(t, peer, "SELECT count(*) FROM "+table, "0")
							var want [][]string
							for value := 1; value <= 7; value++ {
								insertDefaultExecute(t, w, name, insertDefaultArgs(value), 1, ready)
								insertDefaultHealthy(t, w, table, value, ready)
								want = append(want, []string{"7", strconv.Itoa(value)})
							}
							if explicit {
								serverScalar(t, peer, "SELECT count(*) FROM "+table, "0")
								insertDefaultCommand(t, w, "COMMIT", "COMMIT", 'I')
							}
							insertDefaultRead(t, peer, "SELECT id,c FROM "+table+" ORDER BY c", []string{"id", "c"}, []uint32{23, 23}, want, 'I')
							insertDefaultCommand(t, w, "BEGIN", "BEGIN", 'T')
							// BEGIN is deliberately before a new unnamed definition.
							if !named {
								insertDefaultPrepare(t, w, name, "INSERT INTO "+table+"(c) SELECT $1", []uint32{23}, zeroHints, 'T')
							}
							insertDefaultExecute(t, w, name, insertDefaultArgs(99), 1, 'T')
							insertDefaultHealthy(t, w, table, 8, 'T')
							insertDefaultCommand(t, w, "ROLLBACK", "ROLLBACK", 'I')
							insertDefaultRead(t, peer, "SELECT id,c FROM "+table+" ORDER BY c", []string{"id", "c"}, []uint32{23, 23}, want, 'I')
						})
					}
				}
			}
			for _, family := range insertDefaultFamilies {
				for _, named := range []bool{false, true} {
					for _, explicit := range []bool{false, true} {
						for sourceIndex := range insertDefaultSources("unused") {
							t.Run(fmt.Sprintf("%s/named_%t/transaction_%t/source_%d", family.name, named, explicit, sourceIndex), func(t *testing.T) {
								table := fmt.Sprintf("raw_family_%s_%t_%t_%d", family.name, named, explicit, sourceIndex)
								w, peer := serverAuthenticated(t, dsn), serverAuthenticated(t, dsn)
								for _, query := range family.setup(table) {
									serverWantCode(t, w.query(t, query), "")
								}
								ready := byte('I')
								if explicit {
									insertDefaultCommand(t, w, "BEGIN", "BEGIN", 'T')
									ready = 'T'
								}
								name := ""
								if named {
									name = "family_insert"
								}
								source := insertDefaultSources(table)[sourceIndex]
								insertDefaultPrepare(t, w, name, "INSERT INTO "+table+"(c) "+source.suffix, source.oids, false, ready)
								insertDefaultHealthy(t, w, table, 0, ready)
								if family.name == "serial" || family.name == "nextval" {
									sequence := table + "_id_seq"
									if family.name == "nextval" {
										sequence = table + "_seq"
									}
									serverScalar(t, peer, "SELECT is_called FROM "+sequence, "f")
								}
								var want [][]string
								n := 0
								for _, use := range source.uses {
									insertDefaultExecute(t, w, name, insertDefaultArgs(use.args...), use.count, ready)
									for range use.count {
										id := 7
										if family.name != "constant" {
											id = family.first + n
										}
										want = append(want, []string{strconv.Itoa(id), fmt.Sprint(use.args[0])})
										n++
									}
									insertDefaultHealthy(t, w, table, n, ready)
								}
								if family.name == "now" {
									// Metadata is independent of clock rendering. Boolean equality
									// pins transaction-stability without comparing backend clocks.
									insertDefaultNow(t, w, table, n, explicit, ready)
								}
								if explicit {
									serverScalar(t, peer, "SELECT count(*) FROM "+table, "0")
									insertDefaultCommand(t, w, "COMMIT", "COMMIT", 'I')
								}
								if family.name != "now" {
									insertDefaultRead(t, peer, "SELECT id,c FROM "+table+" ORDER BY c,id", []string{"id", "c"}, []uint32{family.oid, 23}, want, 'I')
								} else {
									insertDefaultRead(t, peer, "SELECT stamp FROM "+table+" WHERE false", []string{"stamp"}, []uint32{1184}, nil, 'I')
								}
							})
						}
					}
				}
			}
			insertDefaultRawSupplied(t, dsn)
			insertDefaultRawAttachments(t, dsn)
		})
	}
}

func insertDefaultHealthy(t *testing.T, w *serverWire, table string, count int, ready byte) {
	t.Helper()
	frames := serverCycle(t, w, serverParse("health", "SELECT count(*) FROM "+table), serverBind("", "health", nil, nil), serverExecute("", 0), serverClose('S', "health"))
	insertDefaultFrames(t, "healthy same-session SELECT", frames, "12DC3Z", ready)
	serverWantRaw(t, frames, [][][]byte{{[]byte(strconv.Itoa(count))}})
	serverWantCompletions(t, frames, "SELECT 1")
}

func insertDefaultNow(t *testing.T, w *serverWire, table string, count int, explicit bool, ready byte) {
	t.Helper()
	query := "SELECT stamp IS NOT NULL AS present FROM " + table + " ORDER BY c"
	names, oids := []string{"present"}, []uint32{16}
	if explicit {
		query = "SELECT stamp IS NOT NULL AS present, stamp = transaction_timestamp() AS same FROM " + table + " ORDER BY c"
		names = append(names, "same")
		oids = append(oids, 16)
	}
	var want [][]string
	for range count {
		row := []string{"t"}
		if explicit {
			row = append(row, "t")
		}
		want = append(want, row)
	}
	insertDefaultRead(t, w, query, names, oids, want, ready)
}

func insertDefaultRawAttachments(t *testing.T, dsn string) {
	t.Helper()
	for _, nine := range []bool{false, true} {
		for _, named := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(fmt.Sprintf("fider/nine_%t/named_%t/transaction_%t", nine, named, explicit), func(t *testing.T) {
					w, peer := serverAuthenticated(t, dsn), serverAuthenticated(t, dsn)
					serverWantCode(t, w.query(t, "DROP TABLE IF EXISTS attachments"), "")
					schema := insertDefaultAttachments
					if nine {
						schema = insertDefaultAttachmentsNine
					}
					insertDefaultCommand(t, w, schema, "CREATE TABLE", 'I')
					ready := byte('I')
					if explicit {
						insertDefaultCommand(t, w, "BEGIN", "BEGIN", 'T')
						ready = 'T'
					}
					name := ""
					if named {
						name = "fider"
					}
					query, oids := insertDefaultFiderSix, []uint32{23, 23, 1043, 23, 23, 25}
					if nine {
						query = insertDefaultFiderNine
						oids = []uint32{23, 23, 23, 23, 1043, 23, 23, 23, 25}
					}
					insertDefaultPrepare(t, w, name, query, oids, false, ready)
					insertDefaultHealthy(t, w, "attachments", 0, ready)
					cases := []struct {
						tenant  int
						comment any
						key     string
					}{{1, nil, "12345-test.png"}, {1, 4, "12345-test.png"}, {2, nil, "12345-test.png"}, {1, nil, "other.png"}}
					for i, c := range cases {
						args := []any{c.tenant, c.comment, c.key, c.tenant, c.comment, c.key}
						if nine {
							args = []any{c.tenant, 10, c.comment, 20, c.key, c.tenant, 10, c.comment, c.key}
						}
						insertDefaultExecute(t, w, name, insertDefaultArgs(args...), 1, ready)
						insertDefaultHealthy(t, w, "attachments", i+1, ready)
						insertDefaultExecute(t, w, name, insertDefaultArgs(args...), 0, ready)
						insertDefaultHealthy(t, w, "attachments", i+1, ready)
					}
					if explicit {
						serverScalar(t, peer, "SELECT count(*) FROM attachments", "0")
						insertDefaultCommand(t, w, "COMMIT", "COMMIT", 'I')
					}
					// Failed NOT EXISTS executions do not request the serial default.
					rows := [][]string{{"1", "1", "<NULL>", "12345-test.png"}, {"2", "1", "4", "12345-test.png"}, {"3", "2", "<NULL>", "12345-test.png"}, {"4", "1", "<NULL>", "other.png"}}
					insertDefaultRead(t, peer, "SELECT id,tenant_id,comment_id,attachment_bkey FROM attachments ORDER BY id", []string{"id", "tenant_id", "comment_id", "attachment_bkey"}, []uint32{23, 23, 23, 1043}, rows, 'I')
					if nine {
						insertDefaultRead(t, peer, "SELECT post_id,user_id FROM attachments ORDER BY id", []string{"post_id", "user_id"}, []uint32{23, 23}, [][]string{{"10", "20"}, {"10", "20"}, {"10", "20"}, {"10", "20"}}, 'I')
					}
				})
			}
		}
	}
}
