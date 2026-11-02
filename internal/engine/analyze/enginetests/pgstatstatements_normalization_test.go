package enginetests

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/parser"
)

func TestPgStatStatementsFingerprintParameters(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		source, want string
		highest      int
	}{
		{"SELECT $1::integer,22", "SELECT $1::integer,$2", 1},
		{"SELECT $2::integer,$1::integer IN (11,-222)", "SELECT $1::integer,$2::integer IN ($3 /*, ... */)", 0},
		{"SELECT $3::integer,11 IN ($1::integer,$2::integer)", "SELECT $1::integer,$2 IN ($3 /*, ... */)", 0},
		{"SELECT $2::integer,$1::integer IN (11)", "SELECT $2::integer,$1::integer IN ($3)", 2},
		{"SELECT 11 IN (11,-222),$1::integer", "SELECT $1 IN ($2 /*, ... */),$3::integer", 0},
	} {
		t.Run(tc.source, func(t *testing.T) {
			jumble := pgssAnalyzedJumble(t, e, tc.source)
			if jumble.HighestParameter != tc.highest {
				t.Fatalf("highest parameter %d, want %d", jumble.HighestParameter, tc.highest)
			}
			got, _ := parser.NormalizeStatementText(tc.source, 0, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
			if got != tc.want {
				t.Fatalf("normalized %q, want %q", got, tc.want)
			}
		})
	}
}
