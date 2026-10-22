package dml

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Successful physical validation needs only the current command. Any accidental
// use of another host service fails rather than supplying a fake public result.
type rlsProposalValidationHost struct {
	Host
	command                        Command
	reported                       bool
	reportedGeneration, reportedID sqltype.Value
}

func (h *rlsProposalValidationHost) Command() *Command { return &h.command }
func (h *rlsProposalValidationHost) RowDetail(b *analyze.Binding, row []sqltype.Value, _ DetailPurpose) (string, error) {
	h.reported = true
	h.reportedGeneration = h.command.PartitionInsert.NamedRow[0]
	h.reportedID = h.command.PartitionInsert.NamedRow[1]
	return exec.FailingRowDetail(h.command.PartitionInsert.NamedRow, sqltype.OutputEnvironment{}, nil, nil, b.Relation)
}

func TestRLSRoutedProposalLayout(t *testing.T) {
	boolean, _ := builtinType(sqltype.Bool)
	integer, _ := builtinType(sqltype.Int4)
	id, err := sqltype.Integer(integer, 9)
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []bool{false, true} {
		for _, violation := range []bool{false, true} {
			name := "no_program"
			if program {
				name = "new_program"
			}
			if violation {
				name += "/constraint_failure"
			}
			t.Run(name, func(t *testing.T) {
				target := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 81001}, Columns: []catalog.Column{{Name: "allowed", Usage: boolean.Usage(), Nullable: true}, {Name: "id", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{2, 5}, Index: map[string]int{"allowed": 0, "id": 1}}
				physical := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 81002}, Columns: []catalog.Column{target.Columns[1], target.Columns[0]}, Attributes: []int32{3, 7}, Index: map[string]int{"id": 0, "allowed": 1}}
				generated := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
				physical.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{7: {Kind: catalog.GenerationStored, Planned: generated}}
				layout, err := catalog.InheritanceQueryLayout(physical, target)
				if err != nil {
					t.Fatal(err)
				}
				segment := &exec.InheritanceQuerySegment{Layout: layout}
				namedRow := []sqltype.Value{sqltype.Boolean(boolean, false), id}
				state := &partition.InsertState{NamedRow: namedRow, Layouts: map[ids.ObjectAddress]*exec.InheritanceQuerySegment{physical.Address(): segment}}
				h := &rlsProposalValidationHost{}
				h.command.PartitionInsert = state
				if program {
					column := plan.ColumnExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
					h.command.RLS.New = &exec.RLSProgram{Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{Expression: column}}}}}
				}
				row := []sqltype.Value{id, sqltype.Null(boolean)}
				if violation {
					physical.Columns[0].Nullable = false
					row[0] = sqltype.Null(integer)
				}
				// The primitive stored recipe is admitted once, as it is in a prepared
				// command; validation must still evaluate its finalized value before RLS.
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
				evaluation.PreparedGenerations = map[*catalog.Relation][]expr.Expr{physical: nil}
				err = validatePhysicalInsertRow(h, analyze.Binding{Relation: physical}, &analyze.Binding{Relation: target}, "public", row, &evaluation)
				if violation {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "23502" || !h.reported {
						t.Fatal("missing real constraint diagnostic", err)
					}
					if !sqltype.SameDatum(h.reportedGeneration, generated.Value) || !h.reportedID.IsNull() {
						t.Fatal("diagnostic observed stale named row")
					}
				} else if err != nil || h.reported {
					t.Fatal(err)
				}
				if !sqltype.SameDatum(row[1], generated.Value) {
					t.Fatal("physical generation was not finalized")
				}
				want := sqltype.Boolean(boolean, program || violation)
				wantID := id
				if violation {
					wantID = sqltype.Null(integer)
				}
				if !sqltype.SameDatum(namedRow[0], want) || !sqltype.SameDatum(namedRow[1], wantID) {
					t.Fatal("no-program projection or lost indexed finalized NEW/diagnostic")
				}
				if &state.NamedRow[0] != &namedRow[0] || state.Layouts[physical.Address()] != segment || &segment.Layout[0] != &layout[0] {
					t.Fatal("replaced admitted layout/private proposal storage")
				}
			})
		}
	}
}
