package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPrimaryKeyAttachmentValidation(t *testing.T) {
	for _, mode := range []string{"valid", "nullable", "unknown", "missing_nn", "mismatched_kind", "null_policy", "second_primary"} {
		t.Run(mode, func(t *testing.T) {
			e, _, parent, idx := primaryKeyUnitFixture(t)
			view := e.CatalogRead()
			state := *view.State()
			state.CloneDescriptors()
			view = view.WithState(&state)
			next := *parent
			next.Columns = slices.Clone(parent.Columns)
			next.Constraints.Unique = slices.Clone(parent.Constraints.Unique)
			backing := *idx
			definition := *idx.SQLIndex
			constraint := *definition.Constraint
			definition.Constraint, backing.SQLIndex = &constraint, &definition
			state.SetDescriptor(next.Address(), &next)
			state.SetDescriptor(backing.Address(), &backing)
			switch mode {
			case "nullable":
				next.Columns[0].Nullable = true
			case "unknown":
				next.Columns[0].NullabilityKnown = false
			case "missing_nn":
				next.Constraints.NotNull = next.Constraints.NotNull[1:]
			case "mismatched_kind":
				constraint.Primary = false
			case "null_policy":
				next.Constraints.Unique[0].NullsNotDistinct = true
				definition.NullsNotDistinct = true
			case "second_primary":
				second := next.Constraints.Unique[0]
				second.OID, second.Name = second.OID+1000, "second"
				other := backing
				other.ObjectAddress.ObjectID += 1000
				other.ObjectName = second.Name
				second.Index = other.Address()
				otherDefinition := definition
				otherDefinition.Constraint = index.UniqueBacking(&second)
				other.SQLIndex = &otherDefinition
				state.SetDescriptor(other.Address(), &other)
				next.Constraints.Unique = append(next.Constraints.Unique, second)
			}
			err := index.ValidateUniques(view, &next, map[string]bool{})
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			if mode == "valid" {
				if err := index.ValidateIndexDefinition(view, &backing); err != nil {
					t.Fatal(err)
				}
			} else if mode == "mismatched_kind" && index.ValidateIndexDefinition(view, &backing) == nil {
				t.Fatal("reciprocal index validation lost Primary")
			}
			if parent.Columns[0].Nullable || !idx.SQLIndex.Constraint.Primary || !parent.Constraints.Unique[0].Primary {
				t.Fatal("validation candidate mutated retained descriptors")
			}
		})
	}
}

func TestPrimaryKeyRealMetadataAndDependencies(t *testing.T) {
	e, _, parent, idx := primaryKeyUnitFixture(t)
	view := e.CatalogRead()
	ref := e.catalog.Domain.Ref(view.Epoch(), idx.Address())
	metadata, names, err := index.ProjectIndexMetadata(context.Background(), view, ref)
	if err != nil || !metadata.IndIsUnique || !metadata.IndIsPrimary || !metadata.IndImmediate || !metadata.IndIsValid || !metadata.IndIsReady || !metadata.IndIsLive || metadata.IndNullsNotDistinct || metadata.IndIsExclusion || metadata.IndRelID != parent.Address().ObjectID || metadata.IndexRelID != idx.Address().ObjectID || metadata.IndNKeyAtts != 2 || metadata.IndNatts != 2 || !reflect.DeepEqual(metadata.IndKey, []int16{1, 2}) {
		t.Fatal(metadata, err)
	}
	if names.SchemaName != "public" || names.TableName != "t" || names.IndexName != "pk" || names.IndexDef != "CREATE UNIQUE INDEX pk ON public.t USING btree (a, b)" {
		t.Fatal(names)
	}
	if rendered, err := (stored.Deparser{View: view}).UniqueConstraintSQL(parent, &parent.Constraints.Unique[0]); err != nil || rendered != "PRIMARY KEY (a, b)" {
		t.Fatal(rendered, err)
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), view, engineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	constraint := ids.ObjectAddress{ClassID: 2606, ObjectID: parent.Constraints.Unique[0].OID}
	want := []pgcatalog.DependEdge{{Dependent: idx.Address(), Referenced: constraint, DepType: 'i'}}
	for _, attribute := range []int32{1, 2} {
		column := parent.Address()
		column.SubID = attribute
		want = append(want, pgcatalog.DependEdge{Dependent: constraint, Referenced: column, DepType: 'a'})
	}
	var got []pgcatalog.DependEdge
	for _, edge := range edges {
		if edge.Dependent == idx.Address() || edge.Dependent == constraint {
			got = append(got, edge)
		}
	}
	if len(got) != len(want) {
		t.Fatal(got, want)
	}
	for _, edge := range want {
		if !slices.Contains(got, edge) {
			t.Fatal("missing real primary attachment edge", edge, got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := index.ProjectIndexMetadata(ctx, view, ref); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPrimaryKeyNotNullDropCandidate(t *testing.T) {
	e, _, parent, _ := primaryKeyUnitFixture(t)
	view := e.CatalogRead()
	for _, action := range []parser.AlterTableAction{
		{Action: parser.DropNotNull, Column: parser.Identifier{Text: "a"}},
		{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: parent.Constraints.NotNull[0].Name}},
	} {
		next, err := NotNullCandidate(nil, view, parent, action, NotNullDirect)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P16", Message: `column "a" is in a primary key`})
		if next != nil {
			t.Fatal("guard returned mutable candidate", next)
		}
	}
	withoutPK, err := NotNullCandidate(nil, view, parent, parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "pk"}}, NotNullDirect)
	if err != nil || len(withoutPK.Constraints.Unique) != 0 || !reflect.DeepEqual(withoutPK.Constraints.NotNull, parent.Constraints.NotNull) {
		t.Fatal(withoutPK, err)
	}
	withoutNN, err := NotNullCandidate(nil, view, withoutPK, parser.AlterTableAction{Action: parser.DropNotNull, Column: parser.Identifier{Text: "a"}}, NotNullDirect)
	if err != nil || !withoutNN.Columns[0].Nullable || len(withoutNN.Constraints.NotNull) != 1 || len(parent.Constraints.Unique) != 1 || parent.Columns[0].Nullable {
		t.Fatal("ordered candidate or retained descriptor contract", withoutNN, err)
	}
}

func TestPrimaryKeyIdentityGuardPrecedence(t *testing.T) {
	e, owner, _, _ := primaryKeyUnitFixture(t)
	ddl := primaryKeyUnitCreate("identity_table", "identity_pkey")
	ddl.Elements[0].Column.Constraints = []parser.ColumnConstraint{
		{Identity: &parser.IdentityDefinition{Mode: parser.IdentityByDefault}},
		{NotNull: true, Name: parser.Identifier{Text: "nn"}},
	}
	withPublicSchemaUnitOwner(t, e, "pk_owner", func() {
		if _, err := e.Execute(owner, ddl, 0, nil); err != nil {
			t.Fatal(err)
		}
	})
	parent := primaryKeyUnitRelation(t, e, "identity_table")
	view := e.CatalogRead()
	for _, tc := range []struct {
		action     parser.AlterTableAction
		diagnostic sqlstate.Error
	}{
		{parser.AlterTableAction{Action: parser.DropNotNull, Column: parser.Identifier{Text: "a"}}, sqlstate.Error{Code: "42601", Message: `column "a" of relation "identity_table" is an identity column`}},
		{parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "nn"}}, sqlstate.Error{Code: "42P16", Message: `column "a" is in a primary key`}},
	} {
		next, err := NotNullCandidate(nil, view, parent, tc.action, NotNullDirect)
		nullCountUnitDiagnostic(t, err, tc.diagnostic)
		if next != nil || e.CatalogRead().State() != view.State() || parent.Columns[0].Nullable || !parent.Constraints.Unique[0].Primary {
			t.Fatal("identity/PK route guard changed state", next)
		}
	}
}
