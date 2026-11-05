package control

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
)

func TestDblinkConnectionReplayAttributes(t *testing.T) {
	key := providerKey{schema: "dbc_moved", name: "connect_owner", signature: "25"}
	for _, definer := range []bool{false, true} {
		p := providerReplay{original: key, current: key, arguments: "text", securityChanged: true, securityDefiner: definer, configuration: catalog.NewRoutineConfiguration([]string{"application_name=restored"})}
		if unchangedProviders([]providerReplay{p}) {
			t.Fatal("security/configuration replay demand lost")
		}
		mode := "INVOKER"
		if definer {
			mode = "DEFINER"
		}
		got, err := planProviderReplay([]providerReplay{p})
		want := []string{
			"ALTER FUNCTION \"dbc_moved\".\"connect_owner\"(text) SECURITY " + mode + ";\n",
			"ALTER FUNCTION \"dbc_moved\".\"connect_owner\"(text) SET \"application_name\" TO E'restored';\n",
		}
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("security replay got %q, %v; want %q", got, err, want)
		}
		p.configuration = nil
		if unchangedProviders([]providerReplay{p}) {
			t.Fatal("security-only replay demand lost")
		}
	}
}
