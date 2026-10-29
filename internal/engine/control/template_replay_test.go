package control

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestTemplateProviderReplayCycle(t *testing.T) {
	seed := providerReplay{family: templateProvider, original: providerKey{"pg_catalog", "simple", ""}, current: providerKey{"pg_catalog", "unaccent", ""}}
	member := providerReplay{family: templateProvider, objectKind: "template", original: providerKey{"pg_catalog", "unaccent", ""}, current: providerKey{"pg_catalog", "simple", ""}}
	reserved := providerReplay{family: templateProvider, original: providerKey{"pg_catalog", "__stuntgres_template_1", ""}, current: providerKey{"pg_catalog", "__stuntgres_template_1", ""}}
	want := []string{
		"ALTER TEXT SEARCH TEMPLATE \"pg_catalog\".\"simple\" RENAME TO \"__stuntgres_template_2\";\n",
		"ALTER TEXT SEARCH TEMPLATE \"pg_catalog\".\"unaccent\" RENAME TO \"simple\";\n",
		"ALTER TEXT SEARCH TEMPLATE \"pg_catalog\".\"__stuntgres_template_2\" RENAME TO \"unaccent\";\n",
	}
	for _, providers := range [][]providerReplay{{seed, member, reserved}, {reserved, member, seed}} {
		before := slices.Clone(providers)
		got, err := planProviderReplay(providers)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("template cycle: got %q, want %q: %v", got, want, err)
		}
		if !slices.Equal(providers, before) {
			t.Fatal("template collision planner mutated retained input")
		}
		if strings.Contains(strings.Join(got, ""), "OWNER TO") {
			t.Fatal("ownerless template acquired an owner directive")
		}
	}
}

func TestTemplateProviderReplaySeedIdentity(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	address := fdwUnitAddress(ids.TSTemplateClassID, 3727)
	seed, ok := source.CatalogView.TSTemplate(address)
	if !ok {
		t.Fatal("missing genuine initialized template")
	}
	current := *seed
	current.ObjectName = "renamed_simple"
	current.ObjectNamespace = fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID)
	source.State.UnbindName(catalog.DescriptorNameKey(seed))
	source.State.SetDescriptor(address, &current)
	source.State.BindName(catalog.DescriptorNameKey(&current), address)
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView}}
	providers, err := script.templateProviderReplays()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ALTER TEXT SEARCH TEMPLATE \"pg_catalog\".\"simple\" RENAME TO \"renamed_simple\";\n",
		"ALTER TEXT SEARCH TEMPLATE \"pg_catalog\".\"renamed_simple\" SET SCHEMA \"public\";\n",
	}
	got, err := planProviderReplay(providers)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("seed identity: got %q, want %q: %v", got, want, err)
	}
	if seed.Name() != "simple" || seed.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
		t.Fatal("replay mutated retained initialized descriptor")
	}
	forged := current
	forged.Origin = &catalog.InstalledOrigin{Installation: catalog.NewInstallation(fdwUnitAddress(ids.ExtensionClassID, 90099), "unaccent", "1.1", extension.StockUnaccent, []ids.ObjectAddress{address}), Declaration: 0}
	source.State.SetDescriptor(address, &forged)
	if _, err := script.templateProviderReplays(); err == nil {
		t.Fatal("forged installed origin authenticated as initialized template")
	}
}

func TestTemplateMembershipOrdinaryReplay(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	source.CatalogView.Owner().Extensions = map[string]catalog.ExtensionImplementation{"fuzzystrmatch": extension.StockFuzzystrmatch}
	recipe := replayCohort(t, source, 50000, "", true, false)
	ext := source.CatalogView.MustExtension(recipe.Cohort)
	seed, ok := source.CatalogView.TSTemplate(fdwUnitAddress(ids.TSTemplateClassID, 3727))
	if !ok {
		t.Fatal("missing initialized callbacks")
	}
	template := *seed
	template.ObjectAddress = fdwUnitAddress(ids.TSTemplateClassID, 70000)
	template.ObjectNamespace = ext.TargetNamespace
	template.ObjectName = "ordinary_member_template"
	template.Extension = ext.Address()
	source.State.SetDescriptor(template.Address(), &template)
	source.State.BindName(catalog.DescriptorNameKey(&template), template.Address())
	next := *ext
	next.Members = append(slices.Clone(ext.Members), template.Address())
	source.State.SetDescriptor(next.Address(), &next)
	capture := &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog, DomainRoles: source.DomainRoles, OriginalRoles: source.OriginalRoles, Graph: make(dump.Graph)}
	collection := &dumpCollection{Capture: capture, source: source}
	if err := collectTextSearchDump(context.Background(), collection, template.Address(), &template); err != nil {
		t.Fatal(err)
	}
	if len(capture.Templates) != 1 || capture.Templates[0] != &template {
		t.Fatal("current membership suppressed ordinary template creator")
	}
	predecessor, err := (Resolver{}).Predecessor(source.CatalogView, template.Address(), fdwUnitAddress(ids.TSDictionaryClassID, 70001))
	if err != nil || predecessor != template.Address().ObjectID {
		t.Fatal("dictionary consumer lost actual template creator barrier", predecessor, err)
	}
	statement, err := extension.TemplateDumpStatement(source.CatalogView, &template)
	if err != nil {
		t.Fatal(err)
	}
	if !statement.PrivilegedCreation || statement.FollowingSQL != "" || strings.Count(statement.SQL, "CREATE TEXT SEARCH TEMPLATE") != 1 {
		t.Fatal("ordinary template lost privileged ownerless creator", statement)
	}
	script := &dumpScript{Capture: capture}
	if err := script.prepareInstallationReplay(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(script.installationStatements, ""), "ordinary_member_template") {
		t.Fatal("canonical cohort duplicated ordinary template identity")
	}
	if !strings.Contains(strings.Join(script.membershipStatements, ""), `ADD TEXT SEARCH TEMPLATE "public"."ordinary_member_template"`) {
		t.Fatal("ordinary template current membership missing", script.membershipStatements)
	}
	if template.Extension != ext.Address() || source.State.Get(template.Address()) != &template {
		t.Fatal("replay changed captured template authority")
	}
}
