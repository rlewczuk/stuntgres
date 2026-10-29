package control

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
)

func TestInsertUsernameReplayAttributes(t *testing.T) {
	key := providerKey{schema: "iu_ext", name: "iu_real"}
	p := providerReplay{original: key, current: key, securityDefiner: true, configuration: catalog.NewRoutineConfiguration([]string{`search_path=pg_catalog, "odd, schema"`, "application_name=O'Reilly"}), ownerChanged: true, owner: "iu_owner"}
	if unchangedProviders([]providerReplay{p}) { t.Fatal("execution attributes omitted from replay demand") }
	statements, err := planProviderReplay([]providerReplay{p})
	want := []string{
		"ALTER FUNCTION \"iu_ext\".\"iu_real\"() SECURITY DEFINER;\n",
		"ALTER FUNCTION \"iu_ext\".\"iu_real\"() SET \"search_path\" TO E'pg_catalog', E'odd, schema';\n",
		"ALTER FUNCTION \"iu_ext\".\"iu_real\"() SET \"application_name\" TO E'O''Reilly';\n",
		"ALTER FUNCTION \"iu_ext\".\"iu_real\"() OWNER TO \"iu_owner\";\n",
	}
	if err != nil || !slices.Equal(statements, want) { t.Fatalf("attribute replay %q, %v; want %q", statements, err, want) }
	if p.configuration.At(0) != `search_path=pg_catalog, "odd, schema"` || !p.securityDefiner { t.Fatal("planning mutated immutable configuration") }
	p.securityDefiner, p.ownerChanged = false, false
	p.configuration = catalog.NewRoutineConfiguration([]string{"application_name=only"})
	if unchangedProviders([]providerReplay{p}) { t.Fatal("configuration-only change lost") }
	p.configuration = nil
	p.securityDefiner = true
	if unchangedProviders([]providerReplay{p}) { t.Fatal("definer-only change lost") }
	if _, err := providerConfigurationSQL("invalid"); err == nil { t.Fatal("malformed configuration accepted") }
}
