package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLegacySnapshotValues(t *testing.T) {
	for _, tc := range []struct {
		input, canonical string
		lo, hi           int64
		members          [][]any
	}{
		{"12:13:", "12:13:", 12, 13, nil},
		{"12:12:", "12:12:", 12, 12, nil},
		{"12:16:14,14", "12:16:14", 12, 16, [][]any{{int64(14)}}},
		{"12:20:13,15,18", "12:20:13,15,18", 12, 20, [][]any{{int64(13)}, {int64(15)}, {int64(18)}}},
		{"1:9223372036854775809:3", "1:9223372036854775809:3", 1, -9223372036854775807, [][]any{{int64(3)}}},
		{"9223372036854775807:9223372036854775809:9223372036854775808", "9223372036854775807:9223372036854775809:9223372036854775808", 9223372036854775807, -9223372036854775807, [][]any{{int64(-9223372036854775808)}}},
		{"4294967295:4294967297:4294967296", "4294967295:4294967297:4294967296", 4294967295, 4294967297, [][]any{{int64(4294967296)}}},
		{"1:18446744073709551615:3", "1:18446744073709551615:3", 1, -1, [][]any{{int64(3)}}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				for _, arg := range []string{snapshotQuote(tc.input), "$1"} {
					var args []any
					if arg == "$1" {
						args = []any{tc.input}
					}
					q := "SELECT " + arg + "::txid_snapshot AS s,txid_snapshot_xmin(" + arg + ") AS lo,txid_snapshot_xmax(" + arg + ") AS hi"
					identityBoth(t, p, b, b.DB, legacySnapshotStep(b, q, []string{"s", "lo", "hi"}, []string{"TXID_SNAPSHOT", "INT8", "INT8"}, [][]any{{tc.canonical, tc.lo, tc.hi}}, args...))
					identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT x FROM txid_snapshot_xip("+arg+") AS s(x) ORDER BY x", []string{"x"}, []string{"INT8"}, tc.members, args...))
				}
				identityBoth(t, p, b, b.DB, fixture.Select("SELECT $1::pg_snapshot::text AS s,pg_snapshot_xmin($1)::text AS lo,pg_snapshot_xmax($1)::text AS hi", []string{"s", "lo", "hi"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{tc.canonical, strconv.FormatUint(uint64(tc.lo), 10), strconv.FormatUint(uint64(tc.hi), 10)}}, tc.input))
			})
		})
	}
	t.Run("visibility", func(t *testing.T) {
		legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for i, want := range []bool{true, true, false, true, false, true, true, false, true, false, false} {
				id := int64(11 + i)
				identityBoth(t, p, b, b.DB, fixture.Select(fmt.Sprintf("SELECT txid_visible_in_snapshot(%d,'12:20:13,15,18') AS v", id), []string{"v"}, []string{"BOOL"}, [][]any{{want}}))
				identityBoth(t, p, b, b.DB, fixture.Select("SELECT txid_visible_in_snapshot($1,$2) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want}}, id, "12:20:13,15,18"))
			}
			for _, id := range []int64{-1, -9223372036854775808} {
				identityBoth(t, p, b, b.DB, fixture.Select("SELECT txid_visible_in_snapshot($1,'10:20:') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}, id))
			}
		})
	})
	t.Run("large-membership", func(t *testing.T) {
		legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			var members []string
			var values [][]any
			for n := 100; n < 170; n += 2 {
				members = append(members, strconv.Itoa(n))
				values = append(values, []any{int64(n)})
			}
			s := "100:170:" + strings.Join(members, ",")
			identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT x FROM txid_snapshot_xip($1) AS s(x) ORDER BY x", []string{"x"}, []string{"INT8"}, values, s))
			for n := 99; n <= 171; n++ {
				identityBoth(t, p, b, b.DB, fixture.Select("SELECT txid_visible_in_snapshot($1,$2) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{n < 100 || (n < 170 && n%2 == 1)}}, int64(n), s))
			}
		})
	})
}

func TestReferenceLegacySnapshotNullAndSRF(t *testing.T) {
	legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT txid_snapshot_xmin(NULL) AS lo,txid_snapshot_xmax(NULL) AS hi,txid_status(NULL) AS status,txid_visible_in_snapshot(NULL,'10:20:') AS a,txid_visible_in_snapshot(11,NULL) AS b,txid_visible_in_snapshot(NULL,NULL) AS c", []string{"lo", "hi", "status", "a", "b", "c"}, []string{"INT8", "INT8", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil, nil, nil, nil}}))
		for _, arg := range []string{"NULL::txid_snapshot", "'12:13:'"} {
			for _, q := range []string{"SELECT x FROM txid_snapshot_xip(" + arg + ") AS s(x)", "SELECT txid_snapshot_xip(" + arg + ") AS x"} {
				identityBoth(t, p, b, b.DB, legacySnapshotStep(b, q, []string{"x"}, []string{"INT8"}, nil))
			}
		}
		identityBoth(t, p, b, b.DB, fixture.Select("SELECT v.id,s.x FROM (VALUES (1,'12:20:13,15'::txid_snapshot),(2,'12:20:'::txid_snapshot),(3,NULL::txid_snapshot)) AS v(id,snap) LEFT JOIN LATERAL txid_snapshot_xip(v.snap) AS s(x) ON true ORDER BY v.id,s.x", []string{"id", "x"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(13)}, {int64(1), int64(15)}, {int64(2), nil}, {int64(3), nil}}))
		identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT x,n FROM txid_snapshot_xip('12:20:13,15') WITH ORDINALITY AS s(x,n) ORDER BY n", []string{"x", "n"}, []string{"INT8", "INT8"}, [][]any{{int64(13), int64(1)}, {int64(15), int64(2)}}))
	})
}
