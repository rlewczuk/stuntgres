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

func TestConfigurationMembershipOrdinaryReplay(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	source.CatalogView.Owner().Extensions = map[string]catalog.ExtensionImplementation{"fuzzystrmatch": extension.StockFuzzystrmatch, "uuid-ossp": extension.StockUUIDOssp}
	recipe := replayCohort(t, source, 50000, "", true, false)
	ext := source.CatalogView.MustExtension(recipe.Cohort)
	catalog.InstallBuiltinTSParsers(source.State)
	defaultParser := source.CatalogView.MustTSParser(ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: 3722})
	configuration := &catalog.TSConfiguration{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 70000}, ObjectNamespace: ext.TargetNamespace, ObjectRevision: 1, ObjectName: "member_configuration", Owner: source.DomainRoles.LookupRef("pg_read_all_stats"), Parser: defaultParser.Address(), Extension: ext.Address()}
	source.State.SetDescriptor(configuration.Address(), configuration)
	source.State.BindName(catalog.DescriptorNameKey(configuration), configuration.Address())
	next := *ext
	next.Members = append(slices.Clone(ext.Members), configuration.Address())
	source.State.SetDescriptor(next.Address(), &next)
	capture := &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog, DomainRoles: source.DomainRoles, OriginalRoles: source.OriginalRoles, Graph: make(dump.Graph)}
	collection := &dumpCollection{Capture: capture, source: source}
	if err := collectConfigurationDump(context.Background(), collection, configuration.Address(), configuration); err != nil {
		t.Fatal(err)
	}
	if len(capture.Configurations) != 1 || capture.Configurations[0] != configuration {
		t.Fatal("membership suppressed ordinary creator")
	}
	if _, ok := capture.Graph[dump.Event{OID: configuration.Address().ObjectID}]; !ok {
		t.Fatal("configuration lost its own dependency event")
	}
	statement, err := configurationDumpStatement(source.CatalogView, source.DomainRoles, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if !statement.PrivilegedCreation || statement.Owner != "pg_read_all_stats" || strings.Count(statement.SQL, "CREATE TEXT SEARCH CONFIGURATION") != 1 || !strings.Contains(statement.FollowingSQL, `OWNER TO "pg_read_all_stats"`) {
		t.Fatal("ordinary creation/owner authority changed", statement)
	}
	script := &dumpScript{Capture: capture}
	if err := script.prepareInstallationReplay(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(script.installationStatements, ""), "CREATE TEXT SEARCH CONFIGURATION") {
		t.Fatal("installed prefix duplicated ordinary configuration")
	}
	if !strings.Contains(strings.Join(script.membershipStatements, ""), `ADD TEXT SEARCH CONFIGURATION "public"."member_configuration"`) {
		t.Fatal("final symbolic configuration membership omitted", script.membershipStatements)
	}
	if configuration.Extension != ext.Address() || source.State.Get(configuration.Address()) != configuration {
		t.Fatal("rendering mutated captured membership")
	}
}
