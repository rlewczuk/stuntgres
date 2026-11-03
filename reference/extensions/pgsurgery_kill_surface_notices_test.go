package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			t.Run("sorted_groups_duplicates_repeat", func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					var notices []fixture.Diagnostic
					_, nc, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					b.Recorder = rec
					a := freezeOne(t, p, nc, "INSERT INTO ks_part.heap VALUES(1,'removed') RETURNING k,v,ctid,xmin,xmax")
					z := freezeOne(t, p, nc, "INSERT INTO ks_part.heap VALUES(2,'kept') RETURNING k,v,ctid,xmin,xmax")
					var block, offset int
					if _, err := fmt.Sscanf(a.tid, "(%d,%d)", &block, &offset); err != nil {
						t.Fatal(err)
					}
					run := func(input string, want []fixture.Diagnostic) {
						t.Helper()
						notices = nil
						s := ksVoid(killSQL, "ks_part.heap", input)
						s.Prepared = prepared
						freezeRun(t, p, b, nc, s)
						if !reflect.DeepEqual(notices, want) {
							t.Fatalf("notices got %#v, want %#v", notices, want)
						}
						ksRows(t, p, b, nc, z)
					}
					zero := fmt.Sprintf("(%d,0)", block)
					high := fmt.Sprintf("(%d,65535)", block)
					// Sorted blocks/offsets come from the released algorithm, not row order.
					run(freezeTIDs("(4294967295,65535)", "(4294967294,2)", high, a.tid, zero, "(4294967294,1)", a.tid, zero), []fixture.Diagnostic{
						ksNotice("00000", fmt.Sprintf("skipping tid (%d, 0) for relation \"heap\" because the item number is out of range", block)),
						ksNotice("00000", fmt.Sprintf("skipping tid (%d, 0) for relation \"heap\" because the item number is out of range", block)),
						ksNotice("00000", fmt.Sprintf("skipping tid (%d, 65535) for relation \"heap\" because the item number is out of range", block)),
						ksNotice("22023", "skipping block 4294967294 for relation \"heap\" because the block number is out of range"),
						ksNotice("22023", "skipping block 4294967295 for relation \"heap\" because the block number is out of range"),
					})
					dead := ksNotice("00000", fmt.Sprintf("skipping tid (%d, %d) for relation \"heap\" because it is marked dead", block, offset))
					run(freezeTIDs(a.tid), []fixture.Diagnostic{dead})
					run(freezeTIDs(a.tid, a.tid), []fixture.Diagnostic{dead, dead})
					// Existing TID input normalizes a negative block and accepts whitespace.
					q := "SELECT ks_part.heap_force_kill('ks_part.heap'::regclass,ARRAY[$1::text::tid]) AS killed"
					for _, input := range []string{"(-1,0)", "(4294967295,65535)", " (4294967295, 1)junk"} {
						notices = nil
						s := ksVoid(q, input)
						s.Prepared = prepared
						freezeRun(t, p, b, nc, s)
						want := []fixture.Diagnostic{ksNotice("22023", "skipping block 4294967295 for relation \"heap\" because the block number is out of range")}
						if !reflect.DeepEqual(notices, want) {
							t.Fatalf("normalized TID notices %#v, want %#v", notices, want)
						}
					}
				})
			})
			t.Run("empty_heap", func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					var notices []fixture.Diagnostic
					_, nc, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					b.Recorder = rec
					s := ksVoid(killSQL, "ks_part.heap", "{}")
					s.Prepared = prepared
					freezeRun(t, p, b, nc, s)
					if len(notices) != 0 {
						t.Fatalf("empty-array notices %#v", notices)
					}
					s.Args[1] = freezeTIDs("(0,0)", "(0,1)", "(0,1)")
					freezeRun(t, p, b, nc, s)
					want := []fixture.Diagnostic{ksNotice("22023", "skipping block 0 for relation \"heap\" because the block number is out of range")}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("empty-heap notices %#v, want %#v", notices, want)
					}
					ksRows(t, p, b, nc)
				})
			})
			for _, disposition := range []string{"redirect", "dead", "unused"} {
				t.Run("maintenance_"+disposition, func(t *testing.T) {
					ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						var notices []fixture.Diagnostic
						_, nc, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						b.Recorder = rec
						if disposition == "dead" {
							freezeRun(t, p, b, nc, fixture.Exec("CREATE INDEX ks_slot_index ON ks_part.heap(k)", 0))
						}
						a := freezeOne(t, p, nc, "INSERT INTO ks_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
						z := freezeOne(t, p, nc, "INSERT INTO ks_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
						var block, offset int
						if _, err := fmt.Sscanf(a.tid, "(%d,%d)", &block, &offset); err != nil {
							t.Fatal(err)
						}
						expected := []freezeHeader{z}
						reason := "it is marked unused"
						if disposition == "redirect" {
							next := freezeOne(t, p, nc, "UPDATE ks_part.heap SET v='B' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
							var nextBlock, nextOffset int
							if _, err := fmt.Sscanf(next.tid, "(%d,%d)", &nextBlock, &nextOffset); err != nil || block != nextBlock {
								t.Fatal("small HOT fixture must share a page", next.tid, err)
							}
							reason = fmt.Sprintf("it redirects to item %d", nextOffset)
							expected = []freezeHeader{next, z}
						} else {
							freezeRun(t, p, b, nc, fixture.Exec("DELETE FROM ks_part.heap WHERE k=1", 1))
							if disposition == "dead" {
								reason = "it is marked dead"
							}
						}
						vacuum := "VACUUM (TRUNCATE OFF) ks_part.heap"
						if disposition == "dead" {
							vacuum = "VACUUM (INDEX_CLEANUP OFF,TRUNCATE OFF) ks_part.heap"
						}
						freezeRun(t, p, b, nc, fixture.Exec(vacuum, 0))
						notices = nil
						s := ksVoid(killSQL, "ks_part.heap", freezeTIDs(a.tid))
						s.Prepared = prepared
						freezeRun(t, p, b, nc, s)
						want := []fixture.Diagnostic{ksNotice("00000", fmt.Sprintf("skipping tid (%d, %d) for relation \"heap\" because %s", block, offset, reason))}
						if !reflect.DeepEqual(notices, want) {
							t.Fatalf("maintenance notices %#v, want %#v", notices, want)
						}
						ksRows(t, p, b, nc, expected...)
					})
				})
			}
		})
	}
}

func ksNotice(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: code, Message: message}
}
