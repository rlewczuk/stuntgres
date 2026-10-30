package routine

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestRoutineDefaultDeclarations(t *testing.T) {
	expected := map[ids.OID][]catalog.RoutineLiteral{
		3779: {{SourceType: 16, Text: "false"}, {SourceType: 16, Text: "false"}},
		3786: {{SourceType: 16, Text: "false"}, {SourceType: 16, Text: "false"}, {SourceType: 16, Text: "false"}},
		3782: {{SourceType: 1009, Text: "{}"}}, 3783: {{SourceType: 1009, Text: "{}"}},
		3784: {{SourceType: 1009, Text: "{}"}}, 3785: {{SourceType: 1009, Text: "{}"}},
		3464: {{SourceType: 23, Text: "0"}, {SourceType: 23, Text: "0"}, {SourceType: 23, Text: "0"}, {SourceType: 23, Text: "0"}, {SourceType: 23, Text: "0"}, {SourceType: 23, Text: "0"}, {SourceType: 1700, Text: "0.0"}},
		3579: {{SourceType: 16, Text: "false"}}, 3261: {{SourceType: 16, Text: "false"}},
		4005: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}}, 4006: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}}, 4007: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}}, 4008: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}}, 4009: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}},
		1177: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}},
		1179: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}},
		1180: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}},
		2023: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}},
		2030: {{SourceType: 3802, Text: "{}"}, {SourceType: 16, Text: "false"}},
		3960: {{SourceType: 16, Text: "false"}}, 3961: {{SourceType: 16, Text: "false"}},
		4350: {{SourceType: 25, Text: "NFC"}}, 4351: {{SourceType: 25, Text: "NFC"}},
		6212: {{SourceType: 23, Text: "0"}, {SourceType: 23, Text: "1"}},
		3305: {{SourceType: 16, Text: "true"}}, 5054: {{SourceType: 16, Text: "true"}, {SourceType: 25, Text: "use_json_null"}}, 1268: {{SourceType: 16, Text: "true"}},
	}
	seen, defaults := 0, 0
	for _, d := range BuiltinProcedures(authorization.PrincipalRef{}) {
		oid := d.Address().ObjectID
		if oid == 2096 {
			t.Fatal("lookup-only identity materialized")
		}
		want, ok := expected[oid]
		if !ok {
			if d.LiteralDefaults != nil || d.Builtin.Defaults != 0 {
				t.Fatal("unexpected default declaration", oid)
			}
			continue
		}
		seen++
		defaults += len(want)
		arity := strings.Count(string(d.Signature), ",") + 1
		if d.LiteralDefaults.Len() != len(want) || int(d.Builtin.Defaults) != len(want) || len(want) > arity {
			t.Fatal("recipe count", oid)
		}
		for i, v := range want {
			if d.LiteralDefaults.At(i) != v {
				t.Fatal("recipe", oid, i, d.LiteralDefaults.At(i), v)
			}
		}
		copy := d
		copy.ObjectName = "renamed"
		if copy.LiteralDefaults != d.LiteralDefaults {
			t.Fatal("COW lost default authority")
		}
	}
	if seen != 27 || defaults != 48 {
		t.Fatal("finite inventory", seen, defaults)
	}
	input := []catalog.RoutineLiteral{{SourceType: 16, Text: "true"}}
	recipe := catalog.NewRoutineLiteralDefaults(input...)
	input[0].Text = "false"
	value := recipe.At(0)
	value.Text = "false"
	if recipe.At(0).Text != "true" {
		t.Fatal("mutable recipe backing")
	}
}
