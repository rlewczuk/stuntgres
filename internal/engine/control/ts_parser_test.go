package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if candidate, err := Collect(ctx, source); !errors.Is(err, context.Canceled) || candidate != nil {
		t.Fatal("canceled collection published prefix", candidate, err)
	}
	invalid := *config
	invalid.Parser.ObjectID++
	source.State.SetDescriptor(invalid.Address(), &invalid)
	if candidate, err := Collect(context.Background(), source); err == nil || candidate != nil {
		t.Fatal("invalid collection published prefix", candidate, err)
	}
}
