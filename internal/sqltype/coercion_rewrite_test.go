package sqltype

import "testing"

func TestEventTriggerRewriteSemantics(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"UTC", false}, {"Etc/UTC", false}, {"Etc/GMT", false},
		{"Etc/GMT+1", true}, {"Europe/London", true}, {"Africa/Abidjan", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zone, err := ResolveZone(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if got := zone.TimestampConversionRewrites(); got != tc.want {
				t.Fatal("historical offset policy", got, "want", tc.want)
			}
			for _, pair := range [][2]ID{{Timestamp, Timestamptz}, {Timestamptz, Timestamp}} {
				plan, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Assignment)
				if err != nil {
					t.Fatal(err)
				}
				if got := plan.HeapRewriteRequired(zone); got != tc.want {
					t.Fatal("checked timestamp cast rewrite", pair, got, "want", tc.want)
				}
			}
		})
	}
	if (*Zone)(nil).TimestampConversionRewrites() {
		t.Fatal("default UTC unexpectedly rewrites")
	}
}
