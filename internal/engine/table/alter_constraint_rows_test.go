package table

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestValidateAddedConstraintRows(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	one, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	null := sqltype.Null(integer)
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectName: "t", Columns: []catalog.Column{{Name: "v", Usage: integer.Usage()}}, Attributes: []int32{1}, Index: map[string]int{"v": 0}}
	validator := NewAlterRowValidator(relation, "public")
	row := func(id ids.RowID, version uint64, born ids.CommitSeq, value sqltype.Value) storage.RowVersion {
		return storage.RowVersion{Row: id, Version: ids.VersionID(version), Born: born, Values: []sqltype.Value{value}}
	}
	// Only row 1 is a live committed head: 2 ended, 3 is replaced by an own
	// write, 4 is privately deleted and 5 was born after the frontier.
	committed := []storage.RowVersion{row(1, 1, 1, one), row(2, 2, 1, null), row(3, 3, 1, null), row(4, 4, 1, null), row(5, 5, 3, null)}
	ends := &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{2: {Version: 2, End: 1}}}
	replacement := row(3, 6, 0, one)
	replacement.Base = 3
	writes := []storage.RowVersion{replacement}
	deletes := &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{4: {Row: 4, Version: 4}}}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	if err := ValidateAddedConstraintRows(evaluation, validator, committed, ends, 2, writes, deletes); err != nil {
		t.Fatal("dead or replaced row validated", err)
	}
	withNull := append(writes, row(7, 7, 0, null))
	err = ValidateAddedConstraintRows(evaluation, validator, committed, ends, 2, withNull, deletes)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23502" || diagnostic.Column != "v" || diagnostic.Table != "t" || diagnostic.Schema != "public" {
		t.Fatal("own write not validated", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evaluation.Context = ctx
	if err := ValidateAddedConstraintRows(evaluation, validator, committed, ends, 2, withNull, deletes); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not observed before validation", err)
	}
}
