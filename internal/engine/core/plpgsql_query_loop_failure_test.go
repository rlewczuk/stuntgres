package core_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func queryLoopDemandFailure(t *testing.T) {
	for _, assignment := range []bool{false, true} {
		t.Run(map[bool]string{false: "fetch", true: "assignment"}[assignment], func(t *testing.T) {
			s, d, f, read := loopUnitFrame(t, `DECLARE r record; BEGIN FOR r IN SELECT 1 AS i LOOP END LOOP; RETURN 7; END`)
			outer := d.SQL.PL.Root.Statements[0].Block
			f.Enter(d.SQL.PL.Root)
			defer f.Leave(d.SQL.PL.Root)
			f.Enter(outer)
			defer f.Leave(outer)
			in := outer.Statements[0]
			integer, _ := read.LookupType(sqltype.Int4)
			text, _ := read.LookupType(sqltype.Text)
			value, _ := sqltype.Integer(integer, 1)
			schema := []catalog.Column{{Name: "i", Usage: integer.Usage()}}
			cause := errors.New("reached projection failure")
			projection := queryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, cause })
			if assignment {
				// A private named-row destination forces the real ordinal conversion owner
				// to reject text input before publishing either its target or loop success.
				shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "i", Usage: integer.Usage()}})
				if err != nil {
					t.Fatal(err)
				}
				private := *f.Program
				private.Variables = slices.Clone(f.Program.Variables)
				f.Program = &private
				f.Program.Variables[in.QueryLoop.Target.Slot].Record = false
				f.Program.Variables[in.QueryLoop.Target.Slot].Usage = shape.Usage()
				f.Values[in.QueryLoop.Target.Slot] = sqltype.Null(shape)
				schema[0].Usage = text.Usage()
				value = sqltype.String(text, "bad")
				projection = plan.ColumnExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
			}
			previous := f.Values[in.QueryLoop.Target.Slot]
			found := f.Values[f.Program.FoundSlot]
			rows, source := queryLoopUnitRows(t, schema, [][]sqltype.Value{{value}}, projection)
			owner := &exec.CTEInvocation{Frame: 1}
			owned := &exec.CTEOwnedRows{RowResult: rows, Owner: owner, Source: source}
			_, flow, err := plQueryLoopTestRun(s, d, f, read, owned).Statements(outer.Statements[:1])
			if assignment {
				var state interface{ SQLState() string }
				if !errors.As(err, &state) || state.SQLState() != "22P02" {
					t.Fatal("ordinal conversion failure lost", err)
				}
			} else if !errors.Is(err, cause) {
				t.Fatal("fetch failure lost cause", err)
			}
			if flow.Kind != proc.PLNormal || f.Values[in.QueryLoop.Target.Slot] != previous || f.Values[f.Program.FoundSlot] != found {
				t.Fatal("failed demand published target/FOUND/control", flow)
			}
			if err := owned.Close(); err != nil || !owner.Closed || !rows.closed || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != nil {
				t.Fatal("failed demand retained producer", err)
			}
		})
	}
}
