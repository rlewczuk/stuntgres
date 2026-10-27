package proc

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func diagnosticsUnitDefinition(t *testing.T) *Definition {
	t.Helper()
	d := &Definition{Kind: PLpgSQLBody, Name: "diagnostics_unit", Result: plUnitInteger(t).Usage(), Source: "DECLARE x integer; y integer; r record; BEGIN GET DIAGNOSTICS x=ROW_COUNT,y=PG_ROUTINE_OID; IF false THEN GET DIAGNOSTICS r.a=ROW_COUNT; END IF; CASE 1 WHEN 1 THEN x:=7; END CASE; BEGIN RAISE EXCEPTION 'outer'; EXCEPTION WHEN OTHERS THEN GET STACKED DIAGNOSTICS r.b=MESSAGE_TEXT; BEGIN RAISE; EXCEPTION WHEN OTHERS THEN GET STACKED DIAGNOSTICS r.c=PG_EXCEPTION_CONTEXT; END; END; RETURN x; END"}
	var err error
	d.PLSyntax, err = parser.ParsePLpgSQL(d.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	resolve := plUnitResolver(t, d.Result)
	d.PL, err = CompilePL(d, d.PLSyntax, false, plUnitCollation(t), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		switch raw.Type.Name {
		case "record":
			return sqltype.Usage{}, true, nil
		case "text":
			return plUnitBuiltin(t, sqltype.Text).Usage(), false, nil
		}
		return resolve(raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.PL.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPLpgSQLDiagnosticsProgramValidation(t *testing.T) {
	original := diagnosticsUnitDefinition(t)
	variables := append([]PLVariable(nil), original.PL.Variables...)
	root := original.PL.Root
	for _, name := range []string{"absent", "empty", "area", "item", "target", "span", "extra_variant", "raw_item", "raw_area", "raw_target", "raw_end", "raw_delimiter", "raw_text", "coordinated_item", "coordinated_area", "coordinated_target", "coordinated_span", "separately_admitted", "separately_admitted_same_length", "separately_admitted_delimiter", "handler", "unreachable", "ledger"} {
		t.Run(name, func(t *testing.T) {
			d := diagnosticsUnitDefinition(t)
			body := d.PL.Root.Statements[0].Block
			in := &body.Statements[0]
			checked, raw := in.Diagnostics, in.Syntax.Diagnostics
			if checked == nil || !parser.ValidPLDiagnostics(raw) || len(checked.Assignments) != 2 {
				t.Fatal("missing real compile admission")
			}
			switch name {
			case "absent":
				in.Diagnostics = nil
			case "empty":
				checked.Assignments = nil
			case "area":
				checked.Stacked = true
			case "item":
				checked.Assignments[0].Item = parser.PLContext
			case "target":
				checked.Assignments[0].Target = checked.Assignments[1].Target
			case "span":
				in.Syntax.Pos.Character++
			case "extra_variant":
				in.Query = &parser.Select{}
			case "raw_item":
				raw.Assignments[0].Item = parser.PLContext
			case "raw_area":
				raw.Stacked = true
			case "raw_target":
				raw.Assignments[0].Target = raw.Assignments[1].Target
			case "raw_end":
				raw.End.Offset++
			case "raw_delimiter":
				raw.Delimiter.Character++
			case "raw_text":
				raw.Text = strings.ToLower(raw.Text)
			case "coordinated_item":
				raw.Assignments[0].Item = parser.PLContext
				checked.Assignments[0].Item = parser.PLContext
			case "coordinated_area":
				raw.Stacked, checked.Stacked = true, true
				raw.Assignments[0].Item, checked.Assignments[0].Item = parser.PLMessageText, parser.PLMessageText
			case "coordinated_target":
				raw.Assignments[0].Target = raw.Assignments[1].Target
				checked.Assignments[0].Target = checked.Assignments[1].Target
			case "coordinated_span":
				raw.Assignments[0].ItemPos.Character++
			case "separately_admitted", "separately_admitted_same_length", "separately_admitted_delimiter":
				source := d.Source
				prior := raw
				donorSource := strings.Replace(source, "y=PG_ROUTINE_OID", "y=PG_CONTEXT", 1)
				switch name {
				case "separately_admitted_same_length":
					donorSource = strings.Replace(source, "ROW_COUNT", "row_count", 1)
				case "separately_admitted_delimiter":
					donorSource = strings.Replace(source, "PG_ROUTINE_OID;", "PG_ROUTINE_OID ;", 1)
				}
				donor, err := parser.ParsePLpgSQL(donorSource, parser.PLReturnPolicy{})
				if err != nil {
					t.Fatal(err)
				}
				replacement := donor.Statements[0].Diagnostics
				if replacement == prior || !parser.ValidPLDiagnostics(replacement) || replacement.Pos != prior.Pos || replacement.Assignments[0].ItemPos != prior.Assignments[0].ItemPos || replacement.Assignments[1].ItemPos != prior.Assignments[1].ItemPos {
					t.Fatal("donor is not a separately admitted coordinate-preserving payload")
				}
				in.Syntax.Diagnostics = replacement
				if name == "separately_admitted" {
					checked.Assignments[1].Item = parser.PLContext
				}
				if name == "separately_admitted_same_length" && (replacement.End != prior.End || len(replacement.Text) != len(prior.Text) || replacement.Text == prior.Text) {
					t.Fatal("same-length substitution does not distinguish admitted text from span bounds")
				}
				if name == "separately_admitted_delimiter" && replacement.End == prior.End {
					t.Fatal("delimiter substitution did not change the admitted end")
				}
				if d.Source != source || d.PLSyntax.Statements[0].Diagnostics != replacement || prior.Assignments[1].Item != parser.PLRoutineOID || !parser.ValidPLDiagnostics(prior) {
					t.Fatal("substitution changed source or prior parser backing")
				}
			case "handler":
				body.Statements[3].Block.Handlers[0].Statements[0].Diagnostics.Assignments[0].Item = parser.PLRowCount
			case "unreachable":
				body.Statements[1].If.Arms[0].Statements[0].Diagnostics.Assignments[0].Target.Slot = len(d.PL.Variables)
			case "ledger":
				body.Statements[2].Case.SearchName = "__Case__Variable_1__"
			}
			if err := d.PL.ValidateDefinition(d); err == nil {
				t.Fatal("accepted forged diagnostics", name)
			}
			if original.PL.Root != root || !reflect.DeepEqual(original.PL.Variables, variables) || original.PL.ValidateDefinition(original) != nil {
				t.Fatal("candidate modified original immutable backing")
			}
		})
	}
	t.Run("identical_source_admission", func(t *testing.T) {
		d := diagnosticsUnitDefinition(t)
		donor, err := parser.ParsePLpgSQL(d.Source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		in := &d.PL.Root.Statements[0].Block.Statements[0]
		prior := in.Syntax.Diagnostics
		replacement := donor.Statements[0].Diagnostics
		if prior == replacement || !parser.ValidPLDiagnostics(replacement) {
			t.Fatal("identical-source parse did not create separate genuine admission")
		}
		in.Syntax.Diagnostics = replacement
		if err := d.PL.ValidateDefinition(d); err != nil {
			t.Fatal("rejected genuine admission for the exact source occurrence", err)
		}
		if !parser.ValidPLDiagnostics(prior) || prior.Assignments[0].Item != parser.PLRowCount {
			t.Fatal("identical-source substitution mutated prior backing")
		}
	})
	copyProgram := *original.PL
	copyProgram.Variables = append([]PLVariable(nil), original.PL.Variables...)
	if !original.PL.SameLayout(&copyProgram) {
		t.Fatal("copying Usages changed layout")
	}
	changed := conditionalUnitDefinition(t, "DECLARE x integer; BEGIN GET DIAGNOSTICS x=PG_ROUTINE_OID; RETURN x; END")
	if original.PL.SameLayout(changed.PL) {
		t.Fatal("different diagnostics layout accepted")
	}
}

func TestPLpgSQLDiagnosticsFrameOwnership(t *testing.T) {
	d := diagnosticsUnitDefinition(t)
	p := d.PL
	variables := append([]PLVariable(nil), p.Variables...)
	input := NewFrame(nil)
	defer input.Close()
	first := NewPLFrame(p, d, input, sqltype.CollationRef{})
	second := NewPLFrame(p, d, input, sqltype.CollationRef{})
	defer second.Close()
	found := func(f *PLFrame) bool {
		v, err := f.Values[p.FoundSlot].Boolean()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if first == second || first.RowCount() != 0 || second.RowCount() != 0 || found(first) || found(second) || first.HandledError() != nil || second.HandledError() != nil {
		t.Fatal("fresh invocation inherited mutable state")
	}
	first.SetRowCount(3)
	first.SetFound(true)
	second.SetRowCount(11)
	if first.RowCount() != 3 || second.RowCount() != 11 || !found(first) || found(second) {
		t.Fatal("frames share count/FOUND")
	}
	block := p.Root.Statements[0].Block.Statements[3].Block
	nested := block.Handlers[0].Statements[1].Block
	outer := &sqlstate.Error{Code: "22012", Message: "outer", Detail: "owned detail", Hint: "owned hint", Context: "original caught context"}
	inner := &sqlstate.Error{Code: "22P02", Message: "inner", Context: "inner origin"}
	previous := first.EnterHandler(block.HandlerScope, outer)
	if previous != nil || first.HandledError() != outer || second.HandledError() != nil {
		t.Fatal("handler ownership escaped frame")
	}
	owned := first.Values[block.HandlerScope.Bindings["sqlerrm"]]
	restored := first.EnterHandler(nested.HandlerScope, inner)
	if restored != outer || first.HandledError() != inner {
		t.Fatal("nested handler failed to replace active error")
	}
	first.LeaveHandler(nested.HandlerScope, restored)
	if first.HandledError() != outer || first.RowCount() != 3 || !found(first) {
		t.Fatal("nested handler failed exact restoration")
	}
	first.LeaveHandler(block.HandlerScope, previous)
	if first.HandledError() != nil || len(first.Scopes) != 0 || outer.Context != "original caught context" {
		t.Fatal("handler exit retained or mutated caught record")
	}
	// Closing with live handler state must erase private values and references,
	// without erasing an already exported immutable text datum or another frame.
	first.EnterHandler(block.HandlerScope, outer)
	values := first.Values
	first.Close()
	first.Close()
	if first.RowCount() != 0 || first.Values != nil || first.Scopes != nil || first.Searches != nil || first.HandledError() != nil || first.InputCollation != (sqltype.CollationRef{}) {
		t.Fatal("Close retained invocation state")
	}
	for _, value := range values {
		if value.Type() != nil {
			t.Fatal("Close left datum in original backing")
		}
	}
	if text, err := owned.Text(); err != nil || text != "outer" {
		t.Fatal("returned text borrowed cleared frame", text, err)
	}
	if second.RowCount() != 11 || found(second) || !reflect.DeepEqual(variables, p.Variables) || p.ValidateDefinition(d) != nil {
		t.Fatal("Close mutated sibling/program")
	}
}
