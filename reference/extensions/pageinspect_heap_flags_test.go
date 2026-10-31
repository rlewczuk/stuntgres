package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Literal source-order names: expectations do not call a decoder or enumerate
// production constants. Array ordering is contractual, unlike unordered rows.
var heapRawFlagNames = []string{
	"HEAP_HASNULL", "HEAP_HASVARWIDTH", "HEAP_HASEXTERNAL", "HEAP_HASOID_OLD",
	"HEAP_XMAX_KEYSHR_LOCK", "HEAP_COMBOCID", "HEAP_XMAX_EXCL_LOCK", "HEAP_XMAX_LOCK_ONLY",
	"HEAP_XMIN_COMMITTED", "HEAP_XMIN_INVALID", "HEAP_XMAX_COMMITTED", "HEAP_XMAX_INVALID",
	"HEAP_XMAX_IS_MULTI", "HEAP_UPDATED", "HEAP_MOVED_OFF", "HEAP_MOVED_IN",
	"HEAP_KEYS_UPDATED", "HEAP_HOT_UPDATED", "HEAP_ONLY_TUPLE",
}

const heapAllCombined = "{HEAP_XMAX_SHR_LOCK,HEAP_XMIN_FROZEN,HEAP_MOVED}"

func TestReferencePageinspectHeapFlags(t *testing.T) {
	t.Run("individual-bits", func(t *testing.T) {
		heapCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for i, name := range heapRawFlagNames {
				a, z := int64(0), int64(0)
				if i < 16 {
					a = 1 << i
				} else {
					z = 1 << (i - 3)
				}
				heapHeld(t, p, b, c, heapFlagsSource, []fixture.Step{heapFlagsStep(heapFlagsSource, a, z, "{"+name+"}", "{}")})
			}
		})
	})
	t.Run("combinations-and-truncation", func(t *testing.T) {
		heapCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			all := "{" + strings.Join(heapRawFlagNames, ",") + "}"
			var steps []fixture.Step
			for _, tc := range []struct {
				a, z          int64
				raw, combined string
			}{
				{0, 0, "{}", "{}"},
				{16, 0, "{HEAP_XMAX_KEYSHR_LOCK}", "{}"},
				{80, 0, "{HEAP_XMAX_KEYSHR_LOCK,HEAP_XMAX_EXCL_LOCK}", "{HEAP_XMAX_SHR_LOCK}"},
				{768, 0, "{HEAP_XMIN_COMMITTED,HEAP_XMIN_INVALID}", "{HEAP_XMIN_FROZEN}"},
				{49152, 0, "{HEAP_MOVED_OFF,HEAP_MOVED_IN}", "{HEAP_MOVED}"},
				{0, 8191, "{}", "{}"},
				{0, 57344, "{HEAP_KEYS_UPDATED,HEAP_HOT_UPDATED,HEAP_ONLY_TUPLE}", "{}"},
				{65536, 65536, "{}", "{}"},
				{65537, 0, "{HEAP_HASNULL}", "{}"},
				{-2147483648, 0, "{}", "{}"},
				{-1, -1, all, heapAllCombined},
				{2147483647, 2147483647, all, heapAllCombined},
				{2816, 2, "{HEAP_XMIN_COMMITTED,HEAP_XMIN_INVALID,HEAP_XMAX_INVALID}", "{HEAP_XMIN_FROZEN}"},
				{777, 32777, "{HEAP_HASNULL,HEAP_HASOID_OLD,HEAP_XMIN_COMMITTED,HEAP_XMIN_INVALID,HEAP_ONLY_TUPLE}", "{HEAP_XMIN_FROZEN}"},
			} {
				s := heapFlagsStep(heapFlagsSource, tc.a, tc.z, tc.raw, tc.combined)
				s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
					var raw, combined string
					if err := rows.Scan(&raw, &combined); err != nil {
						t.Fatal(err)
					}
					if raw != tc.raw || combined != tc.combined {
						t.Fatal("typed array scan", raw, combined, tc)
					}
				}
				steps = append(steps, s)
			}
			heapHeld(t, p, b, c, heapFlagsSource, steps)
			heapHeld(t, p, b, c, heapFlagsScalar, []fixture.Step{
				fixture.Select(heapFlagsScalar, []string{"heap_tuple_infomask_flags"}, []string{"RECORD"}, [][]any{{"({},{})"}}, int64(0), int64(0)),
				fixture.Select(heapFlagsScalar, []string{"heap_tuple_infomask_flags"}, []string{"RECORD"}, [][]any{{"({HEAP_HASNULL},{})"}}, int64(1), int64(0)),
			})
		})
	})
}
