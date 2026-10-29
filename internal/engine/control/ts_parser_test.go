package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestTSParserDump(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	catalog.InstallBuiltinTSParsers(source.State)
	seed := source.CatalogView.MustTSParser(fdwUnitAddress(ids.TSParserClassID, 3722))
	parser := *seed
	parser.ObjectAddress.ObjectID = 91001
	parser.ObjectNamespace = fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID)
	parser.ObjectName = "actual_parser"
	parser.ObjectRevision = 2
	parser.Headline = 0
	source.State.SetDescriptor(parser.Address(), &parser)
	source.State.BindName(catalog.DescriptorNameKey(&parser), parser.Address())
	statement, err := parserDumpStatement(source.CatalogView, &parser)
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE TEXT SEARCH PARSER \"public\".\"actual_parser\" (START = \"pg_catalog\".\"prsd_start\", GETTOKEN = \"pg_catalog\".\"prsd_nexttoken\", END = \"pg_catalog\".\"prsd_end\", LEXTYPES = \"pg_catalog\".\"prsd_lextype\");\n"
	if statement.SQL != want || statement.FollowingSQL != "" || !statement.PrivilegedCreation || strings.Contains(statement.SQL, "OWNER") || strings.Contains(statement.SQL, "HEADLINE") {
		t.Fatal("real callback serialization", statement)
	}
	repeated, err := parserDumpStatement(source.CatalogView, &parser)
	if err != nil || !reflect.DeepEqual(repeated, statement) {
		t.Fatal("nondeterministic parser declaration", err)
	}
	moved := *seed
	moved.ObjectName = "moved_default"
	moved.ObjectNamespace = parser.Namespace()
	moved.ObjectRevision = 3
	source.State.UnbindName(catalog.DescriptorNameKey(seed))
	source.State.SetDescriptor(moved.Address(), &moved)
	source.State.BindName(catalog.DescriptorNameKey(&moved), moved.Address())
	alter, err := parserDumpStatement(source.CatalogView, &moved)
	if err != nil {
		t.Fatal(err)
	}
	if alter.SQL != "ALTER TEXT SEARCH PARSER \"pg_catalog\".\"default\" RENAME TO \"moved_default\";\nALTER TEXT SEARCH PARSER \"pg_catalog\".\"moved_default\" SET SCHEMA \"public\";\n" {
		t.Fatal("seed ALTER contract", alter.SQL)
	}
	capture := &Capture{CatalogView: source.CatalogView, Graph: make(dump.Graph)}
	collection := &dumpCollection{Capture: capture, source: source}
	if err := collectParserDump(context.Background(), collection, parser.Address(), &parser); err != nil {
		t.Fatal(err)
	}
	if len(capture.Parsers) != 1 || !reflect.DeepEqual(capture.Graph[dump.Event{OID: parser.Address().ObjectID}], []dump.Event{{OID: 3722}}) {
		t.Fatal("modified seed predecessor", capture.Graph)
	}
	config := &catalog.TSConfiguration{ObjectAddress: fdwUnitAddress(ids.TSConfigurationClassID, 91002), ObjectNamespace: parser.Namespace(), ObjectRevision: 4, ObjectName: "parser_config", Owner: source.Bootstrap, Parser: parser.Address()}
	source.State.SetDescriptor(config.Address(), config)
	source.State.BindName(catalog.DescriptorNameKey(config), config.Address())
	if err := collectConfigurationDump(context.Background(), collection, config.Address(), config); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(capture.Graph[dump.Event{OID: config.Address().ObjectID}], []dump.Event{{OID: parser.Address().ObjectID}}) {
		t.Fatal("parser-before-config graph", capture.Graph)
	}
	// Keep full-collector admission on the genuine minimal fixture, separate
	// from the declaration-only cohort fixture below (which has no domain CHECK
	// replay preparation). Snapshot descriptors before their membership changes.
	probe := source
	probe.State = catalog.CloneState(source.State)
	probeParser, probeSeed := parser, moved
	probe.State.SetDescriptor(parser.Address(), &probeParser)
	probe.State.SetDescriptor(moved.Address(), &probeSeed)
	probe.CatalogView = source.CatalogView.WithState(probe.State)
	probe.OriginalCatalog = probe.CatalogView
	if baseline, err := Collect(context.Background(), probe); err != nil || baseline == nil || len(baseline.Configurations) != 1 || len(baseline.Parsers) != 2 {
		t.Fatal("valid parser collection baseline", baseline, err)
	}
	// Current e membership does not replace an ordinary parser declaration or
	// its dependency event with the extension event. The initialized parser
	// remains an ALTER replay even when it also acquires lawful membership.
	source.CatalogView.Owner().Extensions = map[string]catalog.ExtensionImplementation{"fuzzystrmatch": extension.StockFuzzystrmatch, "uuid-ossp": extension.StockUUIDOssp}
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	recipe := replayCohort(t, source, 50000, "", true, false)
	parent := *source.CatalogView.MustExtension(recipe.Cohort)
	parser.Extension, moved.Extension = parent.Address(), parent.Address()
	parent.Members = append(parent.Members[:len(parent.Members):len(parent.Members)], parser.Address(), moved.Address())
	source.State.SetDescriptor(parent.Address(), &parent)
	if attached, err := parserDumpStatement(source.CatalogView, &parser); err != nil || attached.SQL != want {
		t.Fatal("membership suppressed ordinary CREATE", attached, err)
	}
	if seeded, err := parserDumpStatement(source.CatalogView, &moved); err != nil || seeded.SQL != alter.SQL || strings.Contains(seeded.SQL, "CREATE") {
		t.Fatal("seed membership manufactured CREATE", seeded, err)
	}
	if oid, err := predecessor(source.CatalogView, parser.Address(), config.Address()); err != nil || oid != parser.Address().ObjectID {
		t.Fatal("configuration lost ordinary parser event", oid, err)
	}
	capture.OriginalCatalog, capture.DomainRoles, capture.OriginalRoles = source.OriginalCatalog, source.DomainRoles, source.OriginalRoles
	script := &dumpScript{Capture: capture}
	if err := script.prepareInstallationReplay(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(script.installationStatements, ""), "CREATE TEXT SEARCH PARSER") {
		t.Fatal("installed prefix duplicated ordinary parser")
	}
	membership := strings.Join(script.membershipStatements, "")
	for _, name := range []string{"actual_parser", "moved_default"} {
		if !strings.Contains(membership, `ADD TEXT SEARCH PARSER "public"."`+name+`"`) {
			t.Fatal("final parser membership missing", name, membership)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if candidate, err := Collect(ctx, probe); !errors.Is(err, context.Canceled) || candidate != nil {
		t.Fatal("canceled collection published prefix", candidate, err)
	}
	invalid := *config
	invalid.Parser.ObjectID++
	probe.State.SetDescriptor(invalid.Address(), &invalid)
	if candidate, err := Collect(context.Background(), probe); err == nil || err.Error() != "stuntgres: invalid text search configuration descriptor authority" || candidate != nil {
		t.Fatal("invalid parser relationship published prefix or failed for unrelated authority", candidate, err)
	}
}
