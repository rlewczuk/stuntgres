package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotValues(t *testing.T) {
	t.Run("bounds-and-members", func(t *testing.T) {
		snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			xid := snapshotType(b, "XID8")
			snapshotBoth(t, p, b, b.DB, fixture.Select("SELECT pg_snapshot_xmin('10:20:10,14,15') AS lo,pg_snapshot_xmax('10:20:10,14,15') AS hi", []string{"lo", "hi"}, []string{xid, xid}, [][]any{{"10", "20"}}))
			snapshotBoth(t, p, b, b.DB, fixture.Select("SELECT x FROM pg_snapshot_xip('10:20:10,14,15') AS s(x) ORDER BY x", []string{"x"}, []string{xid}, [][]any{{"10"}, {"14"}, {"15"}}))
			for _, tc := range []struct {
				id   string
				want bool
			}{{"0", true}, {"9", true}, {"10", false}, {"11", true}, {"14", false}, {"15", false}, {"19", true}, {"20", false}, {"21", false}, {"18446744073709551615", false}} {
				snapshotVisible(t, p, b, b.DB, tc.id, "10:20:10,14,15", tc.want)
			}
			// Numeric subxids have no tag here: absence is arithmetic, not proof
			// that a subtransaction committed. The documented subxid warning applies.
			snapshotVisible(t, p, b, b.DB, "13", "10:20:10,14,15", true)
		})
	})
	for _, tc := range []struct{ input, canonical, lo, hi string }{
		{"10:10:", "10:10:", "10", "10"},
		{"12:16:14,14", "12:16:14", "12", "16"},
		{"1:18446744073709551615:3", "1:18446744073709551615:3", "1", "18446744073709551615"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				q := "SELECT " + snapshotQuote(tc.input) + "::pg_snapshot::text AS s,pg_snapshot_xmin(" + snapshotQuote(tc.input) + ")::text AS lo,pg_snapshot_xmax(" + snapshotQuote(tc.input) + ")::text AS hi"
				snapshotBoth(t, p, b, b.DB, fixture.Select(q, []string{"s", "lo", "hi"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{tc.canonical, tc.lo, tc.hi}}))
				if tc.lo == tc.hi {
					snapshotVisible(t, p, b, b.DB, "9", tc.input, true)
					snapshotVisible(t, p, b, b.DB, "10", tc.input, false)
				}
			})
		})
	}
	t.Run("large-membership", func(t *testing.T) {
		snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			var members []string
			var rows [][]any
			for n := 100; n < 170; n += 2 {
				s := strconv.Itoa(n)
				members = append(members, s)
				rows = append(rows, []any{s})
			}
			s := "100:170:" + strings.Join(members, ",")
			snapshotBoth(t, p, b, b.DB, fixture.Select("SELECT x FROM pg_snapshot_xip("+snapshotQuote(s)+") AS s(x) ORDER BY x", []string{"x"}, []string{snapshotType(b, "XID8")}, rows))
			// Expectations come from parity of independent fixture integers, not xip output.
			for n := 99; n <= 171; n++ {
				snapshotVisible(t, p, b, b.DB, strconv.Itoa(n), s, n < 100 || (n < 170 && n%2 == 1))
			}
		})
	})
}

func TestReferenceSnapshotNullAndSRF(t *testing.T) {
	for _, kind := range []string{"scalars", "from", "target", "ordinality", "lateral"} {
		t.Run(kind, func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				xid := snapshotType(b, "XID8")
				var steps []fixture.Step
				switch kind {
				case "scalars":
					steps = []fixture.Step{fixture.Select("SELECT pg_snapshot_xmin(NULL) AS lo,pg_snapshot_xmax(NULL) AS hi,pg_visible_in_snapshot(NULL,'10:20:') AS a,pg_visible_in_snapshot('11',NULL) AS b,pg_visible_in_snapshot(NULL,NULL) AS c", []string{"lo", "hi", "a", "b", "c"}, []string{xid, xid, "BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil, nil, nil}})}
				case "from", "target":
					for _, arg := range []string{"NULL::pg_snapshot", "'10:20:'"} {
						q := "SELECT x FROM pg_snapshot_xip(" + arg + ") AS s(x)"
						if kind == "target" {
							q = "SELECT pg_snapshot_xip(" + arg + ") AS x"
						}
						steps = append(steps, fixture.Select(q, []string{"x"}, []string{xid}, nil))
					}
				case "ordinality":
					steps = []fixture.Step{fixture.Select("SELECT x,n FROM pg_snapshot_xip('10:20:10,14,15') WITH ORDINALITY AS s(x,n) ORDER BY n", []string{"x", "n"}, []string{xid, "INT8"}, [][]any{{"10", int64(1)}, {"14", int64(2)}, {"15", int64(3)}})}
				case "lateral":
					steps = []fixture.Step{fixture.Select("SELECT v.id,s.x FROM (VALUES (1,'10:20:10,14'::pg_snapshot),(2,'10:20:'::pg_snapshot),(3,NULL::pg_snapshot)) AS v(id,snap) LEFT JOIN LATERAL pg_snapshot_xip(v.snap) AS s(x) ON true ORDER BY v.id,s.x", []string{"id", "x"}, []string{"INT4", xid}, [][]any{{int64(1), "10"}, {int64(1), "14"}, {int64(2), nil}, {int64(3), nil}})}
				}
				for _, s := range steps {
					snapshotBoth(t, p, b, b.DB, s)
				}
			})
		})
	}
}

func TestReferenceSnapshotParsing(t *testing.T) {
	// PostgreSQL 18.6 oracle confirmed these libc-compatible parsing quirks.
	for i, tc := range []struct{ input, canonical string }{
		{"12:16:14,", "12:16:14"}, {"010:020:014", "10:20:14"},
		{"+12:+16:+14", "12:16:14"}, {" 12: 16: 14", "12:16:14"},
		{"1:18446744073709551616:", "1:18446744073709551615:"},
		{"1:-1:", "1:18446744073709551615:"},
		{"-18446744073709551615:2:", "1:2:"},
		{"1:-18446744073709551616:", "1:18446744073709551615:"},
	} {
		t.Run(fmt.Sprintf("valid-%d", i), func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				snapshotBoth(t, p, b, b.DB, fixture.Select("SELECT "+snapshotQuote(tc.input)+"::pg_snapshot::text AS s", []string{"s"}, []string{"TEXT"}, [][]any{{tc.canonical}}))
				snapshotBoth(t, p, b, b.DB, fixture.Select("SELECT $1::pg_snapshot::text AS s", []string{"s"}, []string{"TEXT"}, [][]any{{tc.canonical}}, tc.input))
			})
		})
	}
	for i, input := range []string{"", "garbage", "31:12:", "0:1:", "1:0:", "12:13:0", "12:13:13", "12:16:14,13", "12", "12:16", "12:16::", "12:16:14x", "12 :16:", "12:16:14 ", "12:16:14,,"} {
		t.Run(fmt.Sprintf("invalid-%d", i), func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := snapshotConn(t, p, b)
				message := "invalid input syntax for type pg_snapshot: \"" + input + "\""
				identityError(t, p, b, c, "SELECT "+snapshotQuote(input)+"::pg_snapshot", fixture.Diagnostic{Code: "22P02", Message: message, Position: "8"}, "prepare")
				parameter := "..."
				if input == "" {
					parameter = ""
				}
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: message, Context: "unnamed portal parameter $1 = '" + parameter + "'"}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT $1::pg_snapshot", want, input)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				snapshotBoth(t, p, b, c, fixture.Select("SELECT '10:20:'::pg_snapshot::text AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"10:20:"}}))
			})
		})
	}
}
