package typedef

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/parser"
)

func TestDomainAlterDefaultReplayInternal(t *testing.T) {
	r := DomainDefaultSnapshot{Schema: "public", Domain: "child", Usage: &DomainDefaultUsage{Type: []string{"pg_catalog", "int4"}, Typmod: -1}}
	prefix, err := EncodeDomainDefault(r)
	if err != nil {
		t.Fatal(err)
	}
	decoded, recognized, err := DecodeDomainDefault(strings.TrimPrefix(strings.TrimSpace(prefix), "-- "))
	if err != nil || !recognized || !reflect.DeepEqual(decoded, r) {
		t.Fatal("snapshot roundtrip", decoded, recognized, err)
	}
	script := prefix + "CREATE DOMAIN public.child AS integer DEFAULT 7;"
	statements, err := parser.Parse(script)
	if err != nil {
		t.Fatal(err)
	}
	positioned := PositionedDomainDefault{Record: r, End: len(prefix) - 1}
	pairs, err := PairDomainDefaults(script, statements, []PositionedDomainDefault{positioned})
	if err != nil || len(pairs) != 1 {
		t.Fatal("pair", pairs, err)
	}
	for _, kind := range []string{"duplicate", "target", "absence", "unpaired", "intervening"} {
		t.Run(kind, func(t *testing.T) {
			entries := []PositionedDomainDefault{positioned}
			source := script
			ss := statements
			switch kind {
			case "duplicate":
				entries = append(entries, positioned)
			case "target":
				entries[0].Record.Domain = "other"
			case "absence":
				entries[0].Record.Absent = true
				entries[0].Record.Usage = nil
			case "unpaired":
				ss = nil
			case "intervening":
				source = prefix + "CREATE DOMAIN public.other AS integer; CREATE DOMAIN public.child AS integer DEFAULT 7;"
				ss, err = parser.Parse(source)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := PairDomainDefaults(source, ss, entries); err == nil {
				t.Fatal("invalid capture paired", kind)
			}
		})
	}
	for _, raw := range []string{
		`{"schema":"public","schema":"public","domain":"d","own":false,"absent":true}`,
		`{"schema":"public","domain":"d","own":true,"absent":true}`,
		`{"schema":"public","domain":"d","own":false,"absent":false}`,
		`{"schema":"public","domain":"d","absent":true}`,
		`{"schema":"public","domain":"d","own":false,"absent":true,"usage":null}`,
		`{"schema":"public","domain":"d","own":false,"absent":false,"usage":{"type":["pg_catalog","int4"]}}`,
		`{"schema":"public","domain":"d","own":false,"absent":true,"extra":1}`,
		`{"schema":"public","domain":"d","own":false,"absent":true} {}`,
	} {
		comment := domainDefaultMarker + " " + base64.StdEncoding.EncodeToString([]byte(raw))
		if _, recognized, err := DecodeDomainDefault(comment); !recognized || err == nil {
			t.Fatal("invalid record accepted", raw, recognized, err)
		}
	}
}
