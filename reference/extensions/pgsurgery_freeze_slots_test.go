package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Explicit maintenance creates actual redirect/dead/unused slots. No absent
// coordinate is classified by guessing what PostgreSQL might have pruned.
func TestReferencePgsurgeryFreezeSlotNotices(t *testing.T) {
	for _, disposition := range []string{"redirect", "dead", "unused"} {
		t.Run(disposition, func(t *testing.T) {
			freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				var notices []fixture.Diagnostic
				_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				b.Recorder = rec
				if disposition == "dead" {
					freezeRun(t, p, b, c, fixture.Exec("CREATE INDEX slot_index ON fs_part.heap(k)", 0))
				}
				a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
				z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
				var block, offset int
				if _, err := fmt.Sscanf(a.tid, "(%d,%d)", &block, &offset); err != nil {
					t.Fatal(err)
				}
				var expected []freezeHeader
				reason := "it is marked unused"
				if disposition == "redirect" {
					next := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='B' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
					var nextBlock, nextOffset int
					if _, err := fmt.Sscanf(next.tid, "(%d,%d)", &nextBlock, &nextOffset); err != nil || block != nextBlock {
						t.Fatal("small HOT fixture must share a page", next.tid, err)
					}
					reason = fmt.Sprintf("it redirects to item %d", nextOffset)
					expected = []freezeHeader{next, z}
				} else {
					freezeRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.heap WHERE k=1", 1))
					expected = []freezeHeader{z}
					if disposition == "dead" {
						reason = "it is marked dead"
					}
				}
				vacuum := "VACUUM (TRUNCATE OFF) fs_part.heap"
				if disposition == "dead" {
					vacuum = "VACUUM (INDEX_CLEANUP OFF,TRUNCATE OFF) fs_part.heap"
				}
				freezeRun(t, p, b, c, fixture.Exec(vacuum, 0))
				notices = nil
				freezeApply(t, p, b, c, a.tid)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf("skipping tid (%d, %d) for relation \"heap\" because %s", block, offset, reason)}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("slot notices got %#v, want %#v", notices, want)
				}
				freezeExpect(t, p, b, c, expected...)
			})
		})
	}
}
