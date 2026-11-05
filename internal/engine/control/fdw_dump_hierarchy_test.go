package control

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestFDWDumpHierarchyCompletion(t *testing.T) {
	for _, attach := range []bool{false, true} {
		name := "partition_of"
		if attach {
			name = "separate_attachment"
		}
		t.Run(name, func(t *testing.T) {
			source, wrapper, server, _ := fdwUnitSource()
			integer, ok := source.CatalogView.LookupType(sqltype.Int4)
			if !ok {
				t.Fatal("missing integer codec")
			}
			parent := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 94002), ObjectNamespace: fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID), ObjectName: "parent", ObjectRevision: 1, Owner: source.Bootstrap, RelationKind: catalog.PartitionedRelation,
				Columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{1}, Index: map[string]int{"id": 0}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1},
				Partition: &catalog.PartitionDefinition{Strategy: parser.PartitionHash, Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: integer.Usage()}}}}
			attribute := int32(1)
			if attach {
				attribute = 3
			}
			child := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 94001), ObjectNamespace: parent.Namespace(), ObjectName: "child", ObjectRevision: 1, Owner: source.Bootstrap, RelationKind: catalog.ForeignRelation,
				Columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{attribute}, Index: map[string]int{"id": 0}, ColumnMeta: catalog.ColumnMetadata{HighWater: attribute},
				Foreign:    &catalog.ForeignTableDefinition{Server: server.Address(), Columns: map[int32][]catalog.ForeignOption{attribute: {{Name: "column_name", Value: "remote_id"}}}},
				Membership: &catalog.PartitionMembership{Parent: parent.Address(), Strategy: parser.PartitionHash, Modulus: 2, Remainder: 0}}
			if attach {
				child.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{
					1: {Number: 1, Dropped: true, Local: true, Facts: integer.AttributeFacts()},
					2: {Number: 2, Dropped: true, Local: true, Facts: integer.AttributeFacts()},
				}
			}
			parent.Partition.Children = []ids.ObjectAddress{child.Address()}
			for _, relation := range []*catalog.Relation{parent, child} {
				source.State.SetDescriptor(relation.Address(), relation)
				source.State.BindName(catalog.DescriptorNameKey(relation), relation.Address())
			}
			capture := &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog, DomainRoles: source.DomainRoles, Graph: make(dump.Graph), TableIndices: make(map[ids.OID]int)}
			collection := &dumpCollection{Capture: capture, source: source}
			ctx := context.Background()
			for _, object := range []*catalog.ForeignObject{wrapper, server} {
				if err := collectForeignDump(ctx, collection, object.Address(), object); err != nil {
					t.Fatal(err)
				}
			}
			place := relationDumpContext{namespace: source.CatalogView.MustNamespace(parent.Namespace()), owner: "postgres"}
			if err := collectTableDump(ctx, collection, parent.Address(), parent, place); err != nil {
				t.Fatal(err)
			}
			if err := collectForeignRelationDump(ctx, collection, child.Address(), child, place); err != nil {
				t.Fatal(err)
			}
			declarations, err := dump.Order(ctx, capture.Graph, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, replay := partition.ConstraintDumpCatalog(Resolver{}, source.CatalogView)
			if err := partition.DumpEvents(ctx, Resolver{}, source.CatalogView, capture.Graph, dump.DeclarationEvents(dump.DeclarationIDs(declarations)), capture.Tables, nil, replay, false); err != nil {
				t.Fatal(err)
			}
			order, err := dump.Order(ctx, capture.Graph, nil)
			if err != nil {
				t.Fatal("hierarchy graph is not closed", err)
			}
			position := make(map[dump.Event]int, len(order))
			for i, event := range order {
				position[event] = i
				if event.OID == child.Address().ObjectID && (event.Phase == dump.Rows || event.Phase == dump.InvalidConstraints) {
					t.Fatal("foreign member acquired a physical data or validation phase")
				}
			}
			childDeclaration := dump.Event{OID: child.Address().ObjectID}
			childCompletion := dump.Event{OID: child.Address().ObjectID, Phase: dump.TableCompletion}
			parentCompletion := dump.Event{OID: parent.Address().ObjectID, Phase: dump.TableCompletion}
			if _, exists := position[childCompletion]; !exists || position[childCompletion] <= position[childDeclaration] || position[parentCompletion] <= position[childCompletion] || position[childDeclaration] <= position[dump.Event{OID: parent.Address().ObjectID}] {
				t.Fatal("parent/member declaration and completion order lost", order)
			}
			statement := capture.ForeignDeclarations[child.Address().ObjectID]
			script := &dumpScript{Capture: capture}
			if err := script.prepareAttributeHistory(); err != nil { t.Fatal(err) }
			statement.SQL, err = script.foreignTableDeclaration(ctx, child.Address().ObjectID)
			if err != nil { t.Fatal(err) }
			if strings.Contains(statement.SQL, " PARTITION OF ") == attach {
				t.Fatal("foreign constructor ignored the shared membership decision", statement.SQL)
			}
			if strings.Count(statement.SQL+statement.FollowingSQL, `OPTIONS ("column_name" E'remote_id')`) != 1 {
				t.Fatal("column options were lost or installed twice")
			}
			if attach {
				attachment := dump.Event{OID: child.Address().ObjectID, Phase: dump.TableAttachment}
				if _, exists := position[attachment]; !exists || position[attachment] <= position[childDeclaration] || position[childCompletion] <= position[attachment] {
					t.Fatal("foreign attachment did not precede hierarchy completion")
				}
				text, err := partition.AttachDumpSQL(ctx, source.CatalogView, child)
				if err != nil || text != "ALTER TABLE \"public\".\"parent\" ATTACH PARTITION \"public\".\"child\" FOR VALUES WITH (MODULUS 2, REMAINDER 0);\n" {
					t.Fatal("foreign attachment lost its captured bound", text, err)
				}
			}
			if len(capture.Tables) != 1 || capture.Tables[0].Descriptor != parent || source.Relations[child.Address()] != nil {
				t.Fatal("foreign declaration became physical capture state")
			}
		})
	}
}

func TestFDWDumpIdentityDependencies(t *testing.T) {
	source, wrapper, server, _ := fdwUnitSource()
	catalogOwner := *source.CatalogView.Owner()
	catalogOwner.Domain = new(ids.Domain)
	source.CatalogView = catalog.NewRead(&catalogOwner, source.State)
	source.OriginalCatalog = source.CatalogView
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	integer, _ := source.CatalogView.LookupType(sqltype.Int4)
	regclass, _ := source.CatalogView.LookupType(sqltype.Regclass)
	boolean, _ := source.CatalogView.LookupType(sqltype.Bool)
	owner := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 95002), ObjectNamespace: fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID), ObjectName: "identity_owner", ObjectRevision: 1, Owner: source.Bootstrap, RelationKind: catalog.ForeignRelation,
		Columns: []catalog.Column{{Name: "id", Usage: integer.Usage()}}, Attributes: []int32{3}, Index: map[string]int{"id": 0}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, Foreign: &catalog.ForeignTableDefinition{Server: server.Address()}}
	owner.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{
		1: {Number: 1, Dropped: true, Local: true, Facts: integer.AttributeFacts()},
		2: {Number: 2, Dropped: true, Local: true, Facts: integer.AttributeFacts()},
	}
	owned := owner.Address()
	owned.SubID = 3
	seq := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 95001), ObjectNamespace: owner.Namespace(), ObjectName: "identity_sequence", ObjectRevision: 1, Owner: owner.Owner, RelationKind: catalog.SequenceRelation,
		Sequence: &catalog.SequenceOptions{SQLType: sqltype.Int4, Increment: 1, Min: 1, Max: 2147483647, Start: 1, Cache: 1, Owned: owned, Identity: true}}
	owner.ColumnMeta.Identities = map[int32]catalog.ColumnIdentity{3: {Mode: parser.IdentityAlways, Sequence: seq.Address()}}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: sqltype.String(regclass, strconv.FormatUint(uint64(seq.Address().ObjectID), 10))}
	consumer := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 95000), ObjectNamespace: owner.Namespace(), ObjectName: "identity_consumer", ObjectRevision: 1, Owner: source.Bootstrap, RelationKind: catalog.ForeignRelation,
		Columns: []catalog.Column{{Name: "guard", Usage: boolean.Usage(), Nullable: true}}, Attributes: []int32{1}, Index: map[string]int{"guard": 0}, Foreign: &catalog.ForeignTableDefinition{Server: server.Address()},
		ColumnMeta: catalog.ColumnMetadata{HighWater: 1, Defaults: map[int32]catalog.ColumnDefault{1: {OID: 95003, Expression: plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: literal, Not: true}}}}}
	for _, relation := range []*catalog.Relation{owner, seq, consumer} {
		source.State.SetDescriptor(relation.Address(), relation)
		source.State.BindName(catalog.DescriptorNameKey(relation), relation.Address())
	}
	source.Sequences = map[ids.ObjectAddress]*storage.SequenceGenerator{seq.Address(): {Last: 23, Called: true}}
	capture := &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog, DomainRoles: source.DomainRoles, Graph: make(dump.Graph)}
	collection := &dumpCollection{Capture: capture, source: source}
	ctx := context.Background()
	for _, object := range []*catalog.ForeignObject{wrapper, server} {
		if err := collectForeignDump(ctx, collection, object.Address(), object); err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range []*catalog.Relation{consumer, owner} {
		if err := collectForeignRelationDump(ctx, collection, relation.Address(), relation, relationDumpContext{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := collectSequenceDump(ctx, collection, seq.Address(), seq, relationDumpContext{namespace: source.CatalogView.MustNamespace(seq.Namespace()), owner: "postgres"}); err != nil {
		t.Fatal(err)
	}
	order, err := dump.Order(ctx, capture.Graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !capture.Graph.DependsOn(dump.Event{OID: consumer.Address().ObjectID}, dump.Event{OID: owner.Address().ObjectID}, make(map[dump.Event]bool)) {
		t.Fatal("identity consumer did not wait for its actual foreign owner", order)
	}
	if _, separate := capture.Graph[dump.Event{OID: seq.Address().ObjectID}]; separate {
		t.Fatal("inline identity sequence acquired a separate declaration event")
	}
	if got, err := (Resolver{}).Predecessor(source.CatalogView, seq.Address(), owner.Address()); err != nil || got != 0 {
		t.Fatal("identity owner's self reference became a cycle", got, err)
	}
	creation, completion := sequence.DumpStatements(capture.Sequences)
	if strings.Contains(strings.Join(creation, ""), "CREATE SEQUENCE") || !strings.Contains(strings.Join(completion, ""), "SELECT setval('\"public\".\"identity_sequence\"',23,true);") {
		t.Fatal("identity sequence creation or saved generator state was lost")
	}
	for _, tc := range []struct {
		name   string
		change func(*catalog.Relation)
	}{
		{"kind", func(r *catalog.Relation) { r.RelationKind = catalog.ViewRelation }},
		{"attribute", func(r *catalog.Relation) { r.Attributes = []int32{1} }},
		{"identity", func(r *catalog.Relation) { r.ColumnMeta.Identities = nil }},
		{"schema", func(r *catalog.Relation) {
			r.ObjectNamespace = fdwUnitAddress(ids.NamespaceClassID, ids.PGCatalogNamespaceOID)
		}},
		{"owner", func(r *catalog.Relation) { r.Owner = source.CatalogView.Owner().Principals.Principal(9999) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(source.State)
			changed := *owner
			tc.change(&changed)
			state.SetDescriptor(changed.Address(), &changed)
			if _, err := (Resolver{}).Predecessor(source.CatalogView.WithState(state), seq.Address(), consumer.Address()); err == nil {
				t.Fatal("invalid reciprocal identity provider accepted")
			}
		})
	}
	if owner.Attributes[0] != 3 || source.Sequences[seq.Address()].Last != 23 || !source.Sequences[seq.Address()].Called {
		t.Fatal("collection mutated stable attributes or generator state")
	}
}
