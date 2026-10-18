package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPrimaryKeyAttachmentValidation(t *testing.T) {
	for _, mode := range []string{"valid", "nullable", "unknown", "missing_nn", "mismatched_kind", "null_policy", "second_primary"} {
		t.Run(mode, func(t *testing.T) {
			e, _, parent, index := primaryKeyUnitFixture(t)
			view := e.CatalogRead()
			state := *view.state
			state.descriptors = maps.Clone(view.state.descriptors)
			view.state = &state
			next := *parent
			next.columns = slices.Clone(parent.columns)
			next.uniqueConstraints = slices.Clone(parent.uniqueConstraints)
			backing := *index
			definition := *index.sqlIndex
			constraint := *definition.constraint
			definition.constraint, backing.sqlIndex = &constraint, &definition
			state.setDescriptor(next.address, &next)
			state.setDescriptor(backing.address, &backing)
			switch mode {
			case "nullable":
				next.columns[0].Nullable = true
			case "unknown":
				next.columns[0].NullabilityKnown = false
			case "missing_nn":
				next.notNullConstraints = next.notNullConstraints[1:]
			case "mismatched_kind":
				constraint.Primary = false
			case "null_policy":
				next.uniqueConstraints[0].NullsNotDistinct = true
				definition.nullsNotDistinct = true
			case "second_primary":
				second := next.uniqueConstraints[0]
				second.OID, second.Name = second.OID+1000, "second"
				other := backing
				other.address.ObjectID += 1000
				other.name = second.Name
				second.Index = other.address
				otherDefinition := definition
				otherDefinition.constraint = uniqueBacking(&second)
				other.sqlIndex = &otherDefinition
				state.setDescriptor(other.address, &other)
				next.uniqueConstraints = append(next.uniqueConstraints, second)
			}
			err := validateUniques(view, &next, map[string]bool{})
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			if mode == "valid" {
				if err := validateIndexDefinition(view, &backing); err != nil {
					t.Fatal(err)
				}
			} else if mode == "mismatched_kind" && validateIndexDefinition(view, &backing) == nil {
				t.Fatal("reciprocal index validation lost Primary")
			}
			if parent.columns[0].Nullable || !index.sqlIndex.constraint.Primary || !parent.uniqueConstraints[0].Primary {
				t.Fatal("validation candidate mutated retained descriptors")
			}
		})
	}
}

func TestPrimaryKeyRealMetadataAndDependencies(t *testing.T) {
	e, _, parent, index := primaryKeyUnitFixture(t)
	view := e.CatalogRead()
	ref := e.catalog.domain.Ref(view.Epoch(), index.address)
	metadata, names, err := projectIndexMetadata(context.Background(), view, ref)
	if err != nil || !metadata.IndIsUnique || !metadata.IndIsPrimary || !metadata.IndImmediate || !metadata.IndIsValid || !metadata.IndIsReady || !metadata.IndIsLive || metadata.IndNullsNotDistinct || metadata.IndIsExclusion || metadata.IndRelID != parent.address.ObjectID || metadata.IndexRelID != index.address.ObjectID || metadata.IndNKeyAtts != 2 || metadata.IndNatts != 2 || !reflect.DeepEqual(metadata.IndKey, []int16{1, 2}) {
		t.Fatal(metadata, err)
	}
	if names.SchemaName != "public" || names.TableName != "t" || names.IndexName != "pk" || names.IndexDef != "CREATE UNIQUE INDEX pk ON public.t USING btree (a, b)" {
		t.Fatal(names)
	}
	if rendered, err := (pgRenderer{view: view}).uniqueConstraintSQL(parent, &parent.uniqueConstraints[0]); err != nil || rendered != "PRIMARY KEY (a, b)" {
		t.Fatal(rendered, err)
	}
	edges, err := collectPGDepend(context.Background(), view)
	if err != nil {
		t.Fatal(err)
	}
	constraint := ids.ObjectAddress{ClassID: 2606, ObjectID: parent.uniqueConstraints[0].OID}
	want := []pgDependEdge{{dependent: index.address, referenced: constraint, deptype: 'i'}}
	for _, attribute := range []int32{1, 2} {
		column := parent.address
		column.SubID = attribute
		want = append(want, pgDependEdge{dependent: constraint, referenced: column, deptype: 'a'})
	}
	var got []pgDependEdge
	for _, edge := range edges {
		if edge.dependent == index.address || edge.dependent == constraint {
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
	if _, _, err := projectIndexMetadata(ctx, view, ref); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPrimaryKeyNotNullDropCandidate(t *testing.T) {
	e, _, parent, _ := primaryKeyUnitFixture(t)
	view := e.CatalogRead()
	for _, action := range []parser.AlterTableAction{
		{Action: parser.DropNotNull, Column: parser.Identifier{Text: "a"}},
		{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: parent.notNullConstraints[0].Name}},
	} {
		next, err := notNullCandidate(nil, view, parent, action, notNullDirect)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42P16", Message: `column "a" is in a primary key`})
		if next != nil {
			t.Fatal("guard returned mutable candidate", next)
		}
	}
	withoutPK, err := notNullCandidate(nil, view, parent, parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "pk"}}, notNullDirect)
	if err != nil || len(withoutPK.uniqueConstraints) != 0 || !reflect.DeepEqual(withoutPK.notNullConstraints, parent.notNullConstraints) {
		t.Fatal(withoutPK, err)
	}
	withoutNN, err := notNullCandidate(nil, view, withoutPK, parser.AlterTableAction{Action: parser.DropNotNull, Column: parser.Identifier{Text: "a"}}, notNullDirect)
	if err != nil || !withoutNN.columns[0].Nullable || len(withoutNN.notNullConstraints) != 1 || len(parent.uniqueConstraints) != 1 || parent.columns[0].Nullable {
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
		next, err := notNullCandidate(nil, view, parent, tc.action, notNullDirect)
		nullCountUnitDiagnostic(t, err, tc.diagnostic)
		if next != nil || e.CatalogRead().state != view.state || parent.columns[0].Nullable || !parent.uniqueConstraints[0].Primary {
			t.Fatal("identity/PK route guard changed state", next)
		}
	}
}
