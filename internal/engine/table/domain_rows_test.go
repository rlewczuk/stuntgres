package table

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestValidateSelectedDomainRows(t *testing.T) {
	integer := builtinType(t, sqltype.Int4)
	one, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	null := sqltype.Null(integer)
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100}, ObjectNamespace: namespace, ObjectName: "t", Columns: []catalog.Column{{Name: "d"}}, Attributes: []int32{1}}
	state := catalog.NewState(1)
	state.SetDescriptor(namespace, &catalog.Namespace{ObjectAddress: namespace, ObjectName: "public"})
	state.SetDescriptor(relation.Address(), relation)
	view := catalog.NewRead(nil, state)
	row := func(id ids.RowID, version uint64, born ids.CommitSeq, value sqltype.Value) storage.RowVersion {
		return storage.RowVersion{Row: id, Version: ids.VersionID(version), Born: born, Values: []sqltype.Value{value}}
	}
	// Row 2 ended, 3 is replaced by an own write and 4 was born after the
	// frontier, so only row 1 of the committed rows is checked.
	committed := []storage.RowVersion{row(1, 1, 1, one), row(2, 2, 1, null), row(3, 3, 1, null), row(4, 4, 3, null)}
	use := typedef.DomainValidationUse{Relation: relation, Columns: []int{0}, Versions: committed,
		Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{2: {Version: 2, End: 1}}}}
	replacement := row(3, 5, 0, one)
	replacement.Base = 3
	evaluation := func(ctx context.Context) *exec.Context {
		return &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	}
	validate := func(ctx context.Context, writes ...storage.RowVersion) error {
		return ValidateSelectedDomainRows(evaluation(ctx), view, &catalog.Type{}, use, 2, writes, nil, typedef.DomainValidateNulls)
	}
	if err := validate(context.Background(), replacement); err != nil {
		t.Fatalf("live rows hold no NULL: %v", err)
	}
	var diagnostic *sqlstate.Error
	if err := validate(context.Background(), replacement, row(6, 6, 0, null)); !errors.As(err, &diagnostic) || diagnostic.Code != "23502" || diagnostic.Table != "t" || diagnostic.Column != "d" {
		t.Fatalf("private NULL write: %v", err)
	}
	if err := validate(context.Background()); !errors.As(err, &diagnostic) || diagnostic.Code != "23502" {
		t.Fatalf("unreplaced committed NULL: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validate(canceled, replacement); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scan: %v", err)
	}
}
