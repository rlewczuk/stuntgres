package control

import (
	"bytes"
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestFDWDumpDeferredDefaults(t *testing.T) {
	for _, alreadyDeferred := range []bool{false, true} {
		for _, mode := range []string{"regclass", "opaque-bool", "shared-provider"} {
			inline, shared := mode == "opaque-bool", mode == "shared-provider"
			name := mode
			if alreadyDeferred {
				name += "/ordinary-deferred"
			}
			t.Run(name, func(t *testing.T) {
				source, _, server, _ := fdwUnitSource()
				owner := *source.CatalogView.Owner()
				owner.Domain = new(ids.Domain)
				source.CatalogView = catalog.NewRead(&owner, source.State)
				source.OriginalCatalog = source.CatalogView
				var regclass, boolean, integer *sqltype.Descriptor
				var types []*sqltype.Descriptor
				for _, typ := range sqltype.Builtins() {
					switch typ.ID() {
					case sqltype.Regclass:
						regclass = typ
					case sqltype.Bool:
						boolean = typ
					case sqltype.Int4:
						integer = typ
					}
					element := typ
					if typ.IsArray() {
						element = typ.Element()
					}
					switch element.ID() {
					case sqltype.Regclass, sqltype.Bool, sqltype.Int4:
						types = append(types, typ)
					}
				}
				// Keep this a partial catalog, but include the genuine associated
				// arrays required by each selected scalar's provider contract.
				catalog.InstallBuiltinTypes(source.State, types, source.Bootstrap)
				if regclass == nil || boolean == nil || integer == nil {
					t.Fatal("missing genuine builtin codec")
				}
				provider := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 94002), ObjectNamespace: fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID), ObjectRevision: 1, ObjectName: "late", Owner: source.Bootstrap, RelationKind: catalog.ForeignRelation, Columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{1}, Index: map[string]int{"id": 0}, Foreign: &catalog.ForeignTableDefinition{Server: server.Address()}}
				literal := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: sqltype.String(regclass, strconv.FormatUint(uint64(provider.Address().ObjectID), 10))}
				var expression expr.Expr = literal
				usage := regclass.Usage()
				if inline {
					usage = boolean.Usage()
					expression = plan.NullTestExpr{Info: expr.Info{Usage: usage}, Child: literal, Not: true}
				}
				definition := catalog.ColumnDefault{OID: 94003, Expression: expression}
				if table.DefaultReplayDeferrable(definition) == inline {
					t.Fatal("fixture does not exercise the intended actual default eligibility")
				}
				consumer := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 94001), ObjectNamespace: provider.Namespace(), ObjectRevision: 1, ObjectName: "early", Owner: source.Bootstrap, RelationKind: catalog.ForeignRelation, Columns: []catalog.Column{{Name: "ref", Usage: usage, Nullable: true}}, Attributes: []int32{1}, Index: map[string]int{"ref": 0}, ColumnMeta: catalog.ColumnMetadata{Defaults: map[int32]catalog.ColumnDefault{1: definition}}, Foreign: &catalog.ForeignTableDefinition{Server: server.Address()}}
				if shared {
					consumer.Columns = append(consumer.Columns, catalog.Column{Name: "guard", Usage: boolean.Usage(), Nullable: true})
					consumer.Attributes = append(consumer.Attributes, 3)
					consumer.Index["guard"] = 1
					consumer.ColumnMeta.Defaults[3] = catalog.ColumnDefault{OID: 94004, Expression: plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: literal, Not: true}}
				}
				for _, relation := range []*catalog.Relation{consumer, provider} {
					source.State.SetDescriptor(relation.Address(), relation)
					source.State.BindName(catalog.DescriptorNameKey(relation), relation.Address())
				}
				capture := &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog, DomainRoles: source.DomainRoles, Graph: make(dump.Graph), DeferDefaults: alreadyDeferred}
				collection := &dumpCollection{Capture: capture, source: source}
				if err := collectForeignRelationDump(context.Background(), collection, consumer.Address(), consumer, relationDumpContext{}); err != nil {
					t.Fatal(err)
				}
				if capture.DeferDefaults != alreadyDeferred {
					t.Fatal("foreign collection changed ordinary default deferral")
				}
				if inline || shared {
					found := false
					for _, predecessor := range capture.Graph[dump.Event{OID: consumer.Address().ObjectID}] {
						if predecessor == (dump.Event{OID: provider.Address().ObjectID}) {
							found = true
						}
					}
					if !found {
						t.Fatal("inline opaque REGCLASS payload lost its later-provider graph edge")
					}
				}
				if !inline {
					inlineDefaults := 0
					if shared {
						inlineDefaults = 1
					}
					if strings.Count(capture.ForeignDeclarations[consumer.Address().ObjectID].SQL, " DEFAULT ") != inlineDefaults {
						t.Fatal("eligible omission changed an inline default or left an eligible default inline")
					}
					var output bytes.Buffer
					if err := fdwUnitDump(source, &output); err != nil {
						t.Fatal(err)
					}
					providerAt := strings.Index(output.String(), `CREATE FOREIGN TABLE "public"."late"`)
					repairAt := strings.Index(output.String(), `ALTER FOREIGN TABLE "public"."early" ALTER COLUMN "ref" SET DEFAULT`)
					if providerAt < 0 || repairAt <= providerAt {
						t.Fatal("foreign default repair did not follow the actual later-provider declaration")
					}
					if strings.LastIndex(output.String(), "SET SESSION AUTHORIZATION DEFAULT;") < repairAt {
						t.Fatal("foreign default repair left bootstrap authority active")
					}
					var repeated bytes.Buffer
					if err := fdwUnitDump(source, &repeated); err != nil || !bytes.Equal(output.Bytes(), repeated.Bytes()) {
						t.Fatal("foreign default replay rendering was not deterministic", err)
					}
					// Replay rendering failure must not leak a successful prefix.
					consumer.ColumnMeta.Defaults[1] = catalog.ColumnDefault{OID: 94003, Expression: plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: sqltype.String(regclass, "not-an-oid")}}
					output.Reset()
					if err := fdwUnitDump(source, &output); err == nil || output.Len() != 0 {
						t.Fatal("unrenderable stored foreign reference emitted a partial dump", err, output.Len())
					}
					consumer.ColumnMeta.Defaults[1] = definition
				}
				if !reflect.DeepEqual(consumer.ColumnMeta.Defaults[1].Expression, expression) || consumer.ColumnMeta.Defaults[1].OID != 94003 {
					t.Fatal("default collection mutated the captured definition")
				}
			})
		}
	}
}

func TestFDWDumpTypeAssociations(t *testing.T) {
	for _, mismatched := range []bool{false, true} {
		name := "missing-associated-array"
		if mismatched {
			name = "mismatched-associated-array"
		}
		t.Run(name, func(t *testing.T) {
			source, _, _, _ := fdwUnitSource()
			var boolean *sqltype.Descriptor
			for _, typ := range sqltype.Builtins() {
				if typ.ID() == sqltype.Bool {
					boolean = typ
					break
				}
			}
			if boolean == nil {
				t.Fatal("missing genuine builtin codec")
			}
			catalog.InstallBuiltinTypes(source.State, []*sqltype.Descriptor{boolean}, source.Bootstrap)
			if mismatched {
				descriptor, ok := source.CatalogView.Descriptor(fdwUnitAddress(ids.TypeClassID, ids.OID(sqltype.Bool)))
				if !ok {
					t.Fatal("missing installed boolean")
				}
				// ACLITEM's genuine array exists, but cannot be BOOL's companion.
				descriptor.(*catalog.Type).Array = sqltype.ACLItemArray
			}
			var output bytes.Buffer
			err := fdwUnitDump(source, &output)
			if err == nil || err.Error() != "stuntgres: invalid element array association" || output.Len() != 0 {
				t.Fatalf("invalid builtin association must reject before output: err=%v bytes=%d", err, output.Len())
			}
		})
	}
}
