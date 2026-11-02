package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func freezeSiblingDMLCases(t *testing.T) {
	for _, action := range []string{"update_one", "delete_one", "update_both", "delete_both", "update_both_rollback", "delete_both_rollback"} {
		t.Run("siblings_"+action, func(t *testing.T) {
			freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				horizon := freezeHorizon(t, p, b)
				defer horizon.Close()
				defer horizon.ExecContext(p.Ctx, "ROLLBACK")
				a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
				z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
				next := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='B' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
				if a.tid == next.tid {
					t.Fatal("predecessor and successor share TID")
				}
				freezeApply(t, p, b, c, a.tid)
				a = freezeRepaired(a)
				freezeExpect(t, p, b, c, a, next, z)
				freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				switch action {
				case "update_one":
					changed := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='A2' WHERE ctid=$1::tid RETURNING k,v,ctid,xmin,xmax", a.tid)
					if changed.k != 1 || changed.v != "A2" || changed.tid == a.tid || changed.tid == next.tid || changed.xmin == "2" || changed.xmax != "0" {
						t.Fatal("targeted predecessor UPDATE", changed)
					}
					freezeRun(t, p, b, c, fixture.Exec("COMMIT", 0))
					freezeExpect(t, p, b, c, changed, next, z)
				case "delete_one":
					deleted := freezeOne(t, p, c, "DELETE FROM fs_part.heap WHERE ctid=$1::tid RETURNING k,v,ctid,xmin,xmax", a.tid)
					if deleted.tid != a.tid || deleted.v != "A" || deleted.xmin != "2" || deleted.xmax == "0" {
						t.Fatal("targeted predecessor DELETE", deleted)
					}
					freezeRun(t, p, b, c, fixture.Exec("COMMIT", 0))
					freezeExpect(t, p, b, c, next, z)
				default:
					update := strings.HasPrefix(action, "update")
					q := "DELETE FROM fs_part.heap WHERE k=1 RETURNING k,v,ctid,xmin,xmax"
					if update {
						q = "UPDATE fs_part.heap SET v=v||'2' WHERE k=1 RETURNING k,v,ctid,xmin,xmax"
					}
					changed := freezeCapture(t, p, c, q)
					if len(changed) != 2 {
						t.Fatalf("physical affected versions %d, want two", len(changed))
					}
					var left, right freezeHeader
					for _, row := range changed {
						if row.k != 1 {
							t.Fatal("mutated sentinel", row)
						}
						if row.v == "A" || row.v == "A2" {
							left = row
						} else if row.v == "B" || row.v == "B2" {
							right = row
						} else {
							t.Fatal("unexpected sibling payload", row)
						}
					}
					if left.tid == "" || right.tid == "" || left.tid == right.tid {
						t.Fatal("lost independent sibling identities", changed)
					}
					rollback := strings.HasSuffix(action, "rollback")
					if update {
						if left.v != "A2" || right.v != "B2" || left.tid == a.tid || right.tid == next.tid || left.xmin == "2" || right.xmin == "2" || left.xmax != "0" || right.xmax != "0" {
							t.Fatal("updated sibling headers", changed)
						}
						if rollback {
							a.xmax, next.xmax = left.xmin, right.xmin
						}
					} else {
						if left.tid != a.tid || right.tid != next.tid || left.xmin != "2" || right.xmin != next.xmin || left.xmax == "0" || right.xmax == "0" {
							t.Fatal("deleted sibling headers", changed)
						}
						if rollback {
							a.xmax, next.xmax = left.xmax, right.xmax
						}
					}
					if rollback {
						freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
						freezeExpect(t, p, b, c, a, next, z)
					} else {
						freezeRun(t, p, b, c, fixture.Exec("COMMIT", 0))
						if update {
							freezeExpect(t, p, b, c, left, right, z)
						} else {
							freezeExpect(t, p, b, c, z)
						}
					}
				}
			})
		})
	}
}
