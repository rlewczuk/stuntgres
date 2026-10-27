package proc

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// ValidateDefinition validates the complete immutable checked layout. Typed
// descriptor ownership is checked separately by the catalog procedure provider.
func (p *PLProgram) ValidateDefinition(d *Definition) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid checked PL program") }
	if p == nil || d == nil || d.Kind != PLpgSQLBody || d.PL != p || p.Syntax == nil || p.Syntax != d.PLSyntax || p.Root == nil || !p.Root.FunctionScope || p.Root.Syntax != nil || len(p.Root.Statements) != 1 || p.Root.Statements[0].Block == nil || p.FormalCount != len(d.Formals) || p.Function != d.Name || p.Source != d.Source || p.SearchCount < 0 {
		return invalid()
	}
	if p.Root.Label != p.Function || len(p.Root.Declarations) != 0 || len(p.Root.Handlers) != 0 || p.Root.HandlerScope != nil || p.Root.Statements[0].Syntax != nil || !emptyPLInstructionPayload(p.Root.Statements[0]) {
		return invalid()
	}
	if p.Root.Control == nil || p.Root.Control.ID != 1 || p.Root.Control.Loop || p.Syntax.IsLoop {
		return invalid()
	}
	nextControl := 2
	controls := map[*PLControl]bool{p.Root.Control: true}
	bindings := p.FormalCount
	formalBindings := make(map[string]int)
	if p.Invocation != d.Invocation || d.Invocation > EventTriggerInvocation {
		return invalid()
	}
	if d.Invocation != OrdinaryInvocation && (p.Procedure || len(d.Formals) != 0 || d.Result.Type == nil) {
		return invalid()
	}
	if d.Invocation == EventTriggerInvocation {
		if d.Result.Type.ID() != 3838 || len(p.Variables) < len(eventTriggerNames) {
			return invalid()
		}
		bindings = len(eventTriggerNames)
		for i, name := range eventTriggerNames {
			v := p.Variables[i]
			if v.Name != name || v.Record || v.Constant || v.NotNull || v.InheritInputCollation || v.Role != PLOrdinarySlot || v.Cursor != nil || v.Usage.Type == nil || v.Usage.Type.ID() != 25 || v.Usage != v.Usage.Type.Usage() {
				return invalid()
			}
			formalBindings[name] = i
		}
	}
	if d.Invocation == DMLTriggerInvocation {
		if d.Result.Type.ID() != 2279 || len(p.Variables) < TriggerSlotCount {
			return invalid()
		}
		bindings = TriggerSlotCount
		for i, name := range triggerNames {
			v := p.Variables[i]
			if v.Name != name || v.Record != (i < 2) || v.Constant || v.NotNull || v.InheritInputCollation || v.Role != PLOrdinarySlot || v.Cursor != nil {
				return invalid()
			}
			formalBindings[name] = i
			if i >= 2 {
				if v.Usage.Type == nil || uint32(v.Usage.Type.ID()) != triggerTypes[i] || v.Usage != v.Usage.Type.Usage() {
					return invalid()
				}
			}
		}
	}
	if len(d.OutputFormals) == 0 && (d.PolymorphicResult || sqltype.PolymorphicFamily(d.Result.Type.ID()) != 0) {
		if len(p.Variables) <= bindings {
			return invalid()
		}
		v := p.Variables[bindings]
		if v.Name != "$0" || v.Usage != d.Result || v.Record || v.Constant || v.NotNull || !v.InheritInputCollation || v.Role != PLOrdinarySlot || v.Cursor != nil {
			return invalid()
		}
		formalBindings["$0"] = bindings
		bindings++
	}
	if p.FoundSlot != bindings || len(p.Variables) <= bindings {
		return invalid()
	}
	for i, formal := range d.Formals {
		v := p.Variables[i]
		if v.Name != formal.Name || v.Usage != formal.Usage || v.Record != (formal.Usage.Type.ID() == 2249) || v.Constant || v.NotNull || v.Role != PLOrdinarySlot || v.Cursor != nil || v.InheritInputCollation != (formal.Usage.Collation.ID != 0 || sqltype.PolymorphicFamily(formal.Usage.Type.ID()) != 0) {
			return invalid()
		}
		positional := "$" + strconv.Itoa(i+1)
		if _, exists := formalBindings[positional]; exists {
			return invalid()
		}
		formalBindings[positional] = i
		if formal.Name != "" {
			if _, exists := formalBindings[formal.Name]; exists {
				return invalid()
			}
			formalBindings[formal.Name] = i
		}
	}
	if !maps.Equal(formalBindings, p.Root.Bindings) || p.Root.Label != p.Function || len(p.Root.Declarations) != 0 {
		return invalid()
	}
	var diagnostics plDiagnosticDatums
	found := p.Variables[p.FoundSlot]
	if found.Name != "found" || found.Role != PLFoundSlot || found.Record || found.Constant || found.NotNull || found.Cursor != nil || found.InheritInputCollation || !found.Usage.Valid() || found.Usage.Type.ID() != sqltype.Bool {
		return invalid()
	}
	slot := bindings + 1
	var walk func(*PLBlock, *parser.PLBlock, []*PLBlock, ...map[string]int) error
	searches := 0
	queries := make(map[*parser.Select]bool)
	query := func(checked *parser.Select, raw parser.Expr, alias ...parser.Identifier) bool {
		if raw == nil {
			return checked == nil && (len(alias) == 0 || alias[0] == (parser.Identifier{}))
		}
		if checked == nil || queries[checked] || !plQueryShape(checked, raw.SourcePosition(), alias...) || !reflect.DeepEqual(checked.Targets[0], raw) || !plPrivateReferences(checked, "") {
			return false
		}
		queries[checked] = true
		return true
	}
	var walkList func([]PLInstruction, []parser.PLStatement, []*PLBlock) error
	var walkConditional func(*PLConditional, *parser.PLConditional, bool, []*PLBlock) error
	walkConditional = func(checked *PLConditional, raw *parser.PLConditional, simple bool, scopes []*PLBlock) error {
		if checked == nil || len(raw.Arms) == 0 || len(checked.Arms) != len(raw.Arms) || checked.HasElse != raw.HasElse || !raw.HasElse && len(raw.Else) != 0 || !plSourceSpan(d.Source, raw.Search.Expression, raw.Search.Start, raw.Search.End, raw.Search.Delimiter) || !query(checked.Search, raw.Search.Expression) {
			return invalid()
		}
		if err := diagnostics.expression(p, d.Source, raw.Search.Start, raw.Search.End, scopes); err != nil {
			return invalid()
		}
		if simple {
			if checked.SearchSlot != searches {
				return invalid()
			}
			searches++
		} else if checked.SearchSlot != -1 || checked.SearchName != "" {
			return invalid()
		}
		for i, arm := range raw.Arms {
			branch := checked.Arms[i]
			if len(arm.Conditions) == 0 || !simple && len(arm.Conditions) != 1 {
				return invalid()
			}
			for _, item := range arm.Conditions {
				if !plSourceSpan(d.Source, item.Expression, item.Start, item.End, item.Delimiter) {
					return invalid()
				}
				if err := diagnostics.expression(p, d.Source, item.Start, item.End, scopes); err != nil {
					return invalid()
				}
			}
			first := arm.Conditions[0].Expression
			if first == nil {
				return invalid()
			}
			if simple {
				if branch.Query == nil || queries[branch.Query] || !plQueryShape(branch.Query, first.SourcePosition()) || !plPrivateReferences(branch.Query, PLSearchName(checked.SearchSlot)) {
					return invalid()
				}
				in, ok := branch.Query.Targets[0].(parser.InListExpr)
				pos := first.SourcePosition()
				left := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: PLSearchName(checked.SearchSlot), Pos: pos}}}, Pos: pos}
				if !ok || in.Negate || len(in.Items) != len(arm.Conditions) || !reflect.DeepEqual(in.Left, left) || in.Pos != PLSearchOperatorPosition(d.Source, checked.SearchSlot) || branch.OperatorPosition != in.Pos {
					return invalid()
				}
				for j, item := range arm.Conditions {
					if item.Expression == nil || !reflect.DeepEqual(in.Items[j], item.Expression) {
						return invalid()
					}
				}
				queries[branch.Query] = true
			} else if !query(branch.Query, first) || branch.DiagnosticQuery != "" || branch.OperatorPosition != (parser.Position{}) || branch.DiagnosticOperator != 0 || branch.DiagnosticItems != 0 {
				return invalid()
			}
			if err := walkList(branch.Statements, arm.Statements, scopes); err != nil {
				return err
			}
		}
		if err := walkList(checked.Else, raw.Else, scopes); err != nil {
			return err
		}
		if simple {
			if checked.SearchName != diagnostics.finishCase(slot-1) {
				return invalid()
			}
			prefix := "\"" + checked.SearchName + "\" IN ("
			for i, arm := range raw.Arms {
				branch := checked.Arms[i]
				start := arm.Conditions[0].Start.Offset
				end := arm.Conditions[len(arm.Conditions)-1].End.Offset
				if branch.DiagnosticQuery != prefix+d.Source[start:end]+")" || branch.DiagnosticOperator != len(checked.SearchName)+4 || branch.DiagnosticItems != len(prefix)+1 {
					return invalid()
				}
			}
		}
		return nil
	}
	walkList = func(instructions []PLInstruction, statements []parser.PLStatement, scopes []*PLBlock) error {
		if len(instructions) != len(statements) {
			return invalid()
		}
		for i, instruction := range instructions {
			raw := &statements[i]
			if instruction.Syntax != raw || raw.Pos.Offset < 0 || raw.Pos.Offset >= len(d.Source) || raw.Pos.Character <= 0 || !plSourceSpan(d.Source, raw.Expression, raw.ExpressionStart, raw.ExpressionEnd, raw.ExpressionDelimiter) {
				return invalid()
			}
			if (instruction.Dynamic != nil) != (raw.Dynamic != nil) {
				return invalid()
			}
			if (instruction.SQL != nil) != (raw.SQL != nil) {
				return invalid()
			}
			if (instruction.Into != nil) != (raw.Into != nil) {
				return invalid()
			}
			if (instruction.IntegerFor != nil) != (raw.IntegerFor != nil) {
				return invalid()
			}
			if (instruction.While != nil) != (raw.While != nil) {
				return invalid()
			}
			if (instruction.Foreach != nil) != (raw.Foreach != nil) || (instruction.QueryLoop != nil) != (raw.QueryLoop != nil) || (instruction.Loop != nil) != (raw.Loop != nil) || (instruction.Transfer != nil) != (raw.Transfer != nil) {
				return invalid()
			}
			if (instruction.Raise != nil) != (raw.Raise != nil) || (instruction.Assert != nil) != (raw.Assert != nil) {
				return invalid()
			}
			if err := diagnostics.target(p, d.Source, raw, scopes); err != nil {
				return invalid()
			}
			if err := diagnostics.expression(p, d.Source, raw.ExpressionStart, raw.ExpressionEnd, scopes); err != nil {
				return invalid()
			}
			variants := 0
			for _, present := range []bool{raw.Dynamic != nil, raw.SQL != nil, raw.Into != nil, raw.IntegerFor != nil, raw.Foreach != nil, raw.Raise != nil, raw.Assert != nil, raw.QueryLoop != nil, raw.Block != nil, raw.Loop != nil, raw.While != nil, raw.Transfer != nil, raw.If != nil, raw.Case != nil, raw.Cursor != nil, raw.Return, raw.Rethrow, raw.Target != nil} {
				if present {
					variants++
				}
			}
			if variants != 1 {
				return invalid()
			}
			switch {
			case raw.Dynamic != nil:
				empty := instruction
				empty.Dynamic = nil
				if instruction.Block != nil || raw.Expression != nil || !emptyPLInstructionPayload(empty) || !p.validDynamic(d, raw, instruction.Dynamic, scopes, &diagnostics) {
					return invalid()
				}
			case raw.SQL != nil:
				q, checked := raw.SQL, instruction.SQL
				empty := instruction
				empty.SQL = nil
				if checked == nil || checked.Query != q.Query || checked.Kind != q.Kind || instruction.Block != nil || raw.Expression != nil || !emptyPLInstructionPayload(empty) || !validPLSQLSource(d.Source, raw.Pos, q) {
					return invalid()
				}
				if diagnostics.expression(p, d.Source, q.Start, q.End, scopes) != nil {
					return invalid()
				}
			case raw.Into != nil:
				q, checked := raw.Into, instruction.Into
				empty := instruction
				empty.Into = nil
				position, supported := plQueryLoopPosition(q.Query)
				if !supported || checked == nil || checked.Query != q.Query || checked.Strict != q.Strict || instruction.Block != nil || !emptyPLInstructionPayload(empty) || raw.Expression != nil || !validPLIntoSource(d.Source, q, position) {
					return invalid()
				}
				expected, err := p.compileInto(q, scopes)
				if err != nil || len(expected.Targets) != len(checked.Targets) {
					return invalid()
				}
				for j, target := range expected.Targets {
					if !reflect.DeepEqual(target, checked.Targets[j]) {
						return invalid()
					}
				}
				if diagnostics.into(p, d.Source, q, checked, scopes) != nil {
					return invalid()
				}
			case raw.Raise != nil || raw.Assert != nil:
				empty := instruction
				empty.Raise, empty.Assert = nil, nil
				if instruction.Block != nil || raw.Expression != nil || !emptyPLInstructionPayload(empty) {
					return invalid()
				}
				operand := func(value parser.PLExpression, checked *parser.Select, required bool) bool {
					return (!required || value.Expression != nil) &&
						value.Alias == (parser.Identifier{}) &&
						plSourceSpan(d.Source, value.Expression, value.Start, value.End, value.Delimiter) &&
						query(checked, value.Expression) &&
						diagnostics.expression(p, d.Source, value.Start, value.End, scopes) == nil
				}
				if raw.Raise != nil {
					if !p.validRaise(raw.Raise, instruction.Raise, raw.Pos) {
						return invalid()
					}
					for i, value := range raw.Raise.Arguments {
						if !operand(value, instruction.Raise.Arguments[i], true) {
							return invalid()
						}
					}
					for i, option := range raw.Raise.Options {
						if !operand(option.Value, instruction.Raise.Options[i].Query, true) {
							return invalid()
						}
					}
				} else if !operand(raw.Assert.Condition, instruction.Assert.Condition, true) || !operand(raw.Assert.Message, instruction.Assert.Message, false) {
					return invalid()
				}
			case raw.IntegerFor != nil:
				q, checked := raw.IntegerFor, instruction.IntegerFor
				empty := instruction
				empty.IntegerFor = nil
				if checked == nil || instruction.Block != nil || !emptyPLInstructionPayload(empty) || raw.Expression != nil || len(q.Targets) != 1 || q.Targets[0] == nil || len(q.TargetCommas) != 0 || q.Body == nil || !q.Body.IsLoop || checked.Reverse != q.Reverse || slot >= len(p.Variables) || checked.Counter != slot {
					return invalid()
				}
				name, err := p.integerForName(q.Targets[0], q.Lower.Start, scopes)
				if err != nil || checked.Name != name || diagnostics.integerFor(p, d.Source, q, scopes) != nil {
					return invalid()
				}
				for i, operand := range []parser.PLExpression{q.Lower, q.Upper, q.Step} {
					checkedQuery := []*parser.Select{checked.Lower, checked.Upper, checked.Step}[i]
					if i < 2 && operand.Expression == nil || !plSourceSpan(d.Source, operand.Expression, operand.Start, operand.End, operand.Delimiter) || !query(checkedQuery, operand.Expression, operand.Alias) {
						return invalid()
					}
				}
				if !plIntegerForDelimiters(d.Source, q) {
					return invalid()
				}
				v := p.Variables[slot]
				if v.Name != name || v.Role != PLIntegerForSlot || v.Record || v.Constant || v.NotNull || v.Cursor != nil || v.InheritInputCollation || !v.Usage.Valid() || v.Usage.Type.ID() != sqltype.Int4 || v.Usage != v.Usage.Type.Usage() {
					return invalid()
				}
				counter := slot
				slot++
				if walk(checked.Body, q.Body, scopes, map[string]int{name: counter}) != nil {
					return invalid()
				}
			case raw.Foreach != nil:
				q, checked := raw.Foreach, instruction.Foreach
				empty := instruction
				empty.Foreach = nil
				if checked == nil || instruction.Block != nil || !emptyPLInstructionPayload(empty) || raw.Expression != nil ||
					len(q.Targets) == 0 || q.Source.Expression == nil || q.Source.Alias != (parser.Identifier{}) ||
					!plSourceSpan(d.Source, q.Source.Expression, q.Source.Start, q.Source.End, q.Source.Delimiter) ||
					q.Source.Delimiter.Offset+4 > len(d.Source) || !strings.EqualFold(d.Source[q.Source.Delimiter.Offset:q.Source.Delimiter.Offset+4], "loop") ||
					q.Body == nil || !q.Body.IsLoop || checked.Slice != q.Slice || checked.SliceSpecified != q.SliceSpecified || checked.SlicePos != q.SlicePos ||
					!q.SliceSpecified && (q.Slice != 0 || q.SlicePos != (parser.Position{})) || !query(checked.Query, q.Source.Expression) {
					return invalid()
				}
				targets, err := p.foreachTargets(q, scopes)
				if err != nil || !reflect.DeepEqual(targets, checked.Targets) || diagnostics.foreach(p, q, scopes) != nil || walk(checked.Body, q.Body, scopes) != nil {
					return invalid()
				}
			case raw.QueryLoop != nil:
				q := raw.QueryLoop
				checked := instruction.QueryLoop
				position, supported := plQueryLoopPosition(q.Query)
				if q.Target == nil || checked == nil || !supported || !plSourceRange(d.Source, position, q.Start, q.End, q.Delimiter) || q.Delimiter.Offset+4 > len(d.Source) || !strings.EqualFold(d.Source[q.Delimiter.Offset:q.Delimiter.Offset+4], "loop") || q.Body == nil || !q.Body.IsLoop || checked.Query != q.Query || instruction.Query != nil || instruction.Block != nil || instruction.Cursor != nil || instruction.If != nil || instruction.Case != nil || instruction.Target.Slot != 0 || len(instruction.Target.Steps) != 0 || raw.Expression != nil {
					return invalid()
				}
				target, err := p.queryLoopTarget(q.Target, scopes)
				if err != nil || checked.Target.Slot != target.Slot || len(checked.Target.Steps) != 0 {
					return invalid()
				}
				if diagnostics.expression(p, d.Source, q.Target.SourcePosition(), q.Start, scopes) != nil || diagnostics.expression(p, d.Source, q.Start, q.End, scopes) != nil || walk(checked.Body, q.Body, scopes) != nil {
					return invalid()
				}
			case raw.While != nil:
				q, checked := raw.While, instruction.While
				empty := instruction
				empty.While = nil
				condition := q.Condition
				if checked == nil || instruction.Block != nil || !emptyPLInstructionPayload(empty) || raw.Expression != nil ||
					condition.Expression == nil || !plSourceSpan(d.Source, condition.Expression, condition.Start, condition.End, condition.Delimiter) ||
					condition.Delimiter.Offset+4 > len(d.Source) || !strings.EqualFold(d.Source[condition.Delimiter.Offset:condition.Delimiter.Offset+4], "loop") ||
					q.Body == nil || !q.Body.IsLoop || q.Body.Pos != condition.Delimiter ||
					!query(checked.Condition, condition.Expression, condition.Alias) {
					return invalid()
				}
				if diagnostics.expression(p, d.Source, condition.Start, condition.End, scopes) != nil || walk(checked.Body, q.Body, scopes) != nil {
					return invalid()
				}
			case raw.Loop != nil:
				if !raw.Loop.IsLoop || instruction.Block != nil || instruction.Query != nil || instruction.If != nil || instruction.Case != nil || instruction.Cursor != nil || instruction.Target.Slot != 0 || len(instruction.Target.Steps) != 0 || raw.Expression != nil || walk(instruction.Loop, raw.Loop, scopes) != nil {
					return invalid()
				}
			case raw.Transfer != nil:
				target, err := resolvePLControl(raw.Transfer, raw.Pos, scopes)
				if err != nil || target != instruction.Transfer || instruction.Block != nil || instruction.If != nil || instruction.Case != nil || instruction.Cursor != nil || instruction.Target.Slot != 0 || len(instruction.Target.Steps) != 0 || !query(instruction.Query, raw.Expression, raw.Transfer.Alias) {
					return invalid()
				}
			case raw.Cursor != nil:
				if instruction.Block != nil || instruction.If != nil || instruction.Case != nil || instruction.Query != nil || instruction.Cursor == nil || instruction.Target.Slot != 0 || len(instruction.Target.Steps) != 0 || raw.Expression != nil {
					return invalid()
				}
				if !p.validCursorInstruction(raw.Cursor, instruction.Cursor, scopes) {
					return invalid()
				}
				if err := diagnostics.cursor(p, d.Source, raw.Cursor, instruction.Cursor, scopes); err != nil {
					return invalid()
				}
				if query := instruction.Cursor.ArgumentQuery; query != nil {
					if queries[query] {
						return invalid()
					}
					queries[query] = true
				}
				if raw.Cursor.Loop != nil {
					if !raw.Cursor.Loop.IsLoop || instruction.Cursor.Loop == nil || instruction.Cursor.Loop.Control != nil {
						return invalid()
					}
					loop := instruction.Cursor.Loop
					if slot >= len(p.Variables) || instruction.Cursor.LoopSlot != slot || loop == nil || loop.FunctionScope || loop.Syntax != nil || loop.Label != "" || len(loop.Declarations) != 0 || len(loop.Statements) != 1 || loop.Statements[0].Syntax != nil || !emptyPLInstructionPayload(loop.Statements[0]) || len(loop.Bindings) != 1 || loop.Bindings[raw.Cursor.LoopName.Text] != slot || loop.HandlerScope != nil || len(loop.Handlers) != 0 {
						return invalid()
					}
					v := p.Variables[slot]
					if v.Name != raw.Cursor.LoopName.Text || !v.Record || v.Role != PLLoopRecordSlot || v.Usage != (sqltype.Usage{}) || v.Constant || v.NotNull || v.Cursor != nil || v.InheritInputCollation {
						return invalid()
					}
					slot++
					if err := walk(loop.Statements[0].Block, raw.Cursor.Loop, append(scopes, loop)); err != nil {
						return err
					}
				} else if instruction.Cursor.Loop != nil || instruction.Cursor.LoopSlot != -1 {
					return invalid()
				}
			case raw.Block != nil:
				if raw.Block.IsLoop || !emptyPLInstructionPayload(instruction) || walk(instruction.Block, raw.Block, scopes) != nil {
					return invalid()
				}
			case raw.If != nil || raw.Case != nil:
				if instruction.Query != nil || instruction.Block != nil || instruction.Cursor != nil || instruction.Target.Slot != 0 || len(instruction.Target.Steps) != 0 || raw.Expression != nil {
					return invalid()
				}
				checked, conditional := instruction.If, raw.If
				if raw.Case != nil {
					if instruction.If != nil {
						return invalid()
					}
					checked, conditional = instruction.Case, raw.Case
				} else if instruction.Case != nil || conditional.Search.Expression != nil {
					return invalid()
				}
				if err := walkConditional(checked, conditional, conditional.Search.Expression != nil, scopes); err != nil {
					return err
				}
			case raw.Rethrow:
				if raw.Expression != nil || instruction.Block != nil || !emptyPLInstructionPayload(instruction) {
					return invalid()
				}
			default:
				if instruction.Block != nil || instruction.If != nil || instruction.Case != nil || instruction.Cursor != nil || !query(instruction.Query, raw.Expression) {
					return invalid()
				}
				if !raw.Return {
					target, err := p.ResolveTarget(raw.Target, scopes)
					if err != nil || target.Slot < 0 || target.Slot >= len(p.Variables) || target.Slot != instruction.Target.Slot || !slices.EqualFunc(target.Steps, instruction.Target.Steps, samePLTargetStep) || p.Variables[target.Slot].Constant {
						return invalid()
					}
				}
				if raw.Return {
					if instruction.Target.Slot != 0 || len(instruction.Target.Steps) != 0 {
						return invalid()
					}
					policy := PLReturnPolicy(d.Result, len(d.OutputFormals), p.Procedure)
					if raw.Expression != nil && (policy.Code != "" || d.ReturnsSet) || raw.Expression == nil && !d.ReturnsSet && !p.Procedure && !d.Void() && d.Invocation != EventTriggerInvocation && len(d.OutputFormals) == 0 {
						return invalid()
					}
				}
			}
		}
		return nil
	}
	walk = func(block *PLBlock, raw *parser.PLBlock, scopes []*PLBlock, initialBindings ...map[string]int) error {
		if block == nil || raw == nil || block.FunctionScope || block.Syntax != raw || block.Label != raw.Label.Text || len(block.Statements) != len(raw.Statements) {
			return invalid()
		}
		if block.Control == nil || controls[block.Control] || block.Control.ID != nextControl || block.Control.Loop != raw.IsLoop || raw.EndLabel.Text != "" && raw.EndLabel.Text != raw.Label.Text || raw.IsLoop && (len(raw.Declarations) != 0 || len(raw.Handlers) != 0) {
			return invalid()
		}
		controls[block.Control] = true
		nextControl++
		visible := &PLBlock{Label: block.Label, Bindings: make(map[string]int), Control: block.Control}
		if len(initialBindings) != 0 {
			maps.Copy(visible.Bindings, initialBindings[0])
		}
		scopes = append(scopes, visible)
		initial := 0
		for _, decl := range raw.Declarations {
			if _, exists := visible.Bindings[decl.Name.Text]; exists {
				return invalid()
			}
			if decl.Alias != nil {
				if decl.Collation != nil {
					return invalid()
				}
				target, err := p.ResolveTarget(decl.Alias, scopes)
				if err != nil || len(target.Steps) != 0 || block.Bindings[decl.Name.Text] != target.Slot {
					return invalid()
				}
				visible.Bindings[decl.Name.Text] = target.Slot
				continue
			}
			if initial >= len(block.Declarations) || slot >= len(p.Variables) {
				return invalid()
			}
			initializer := block.Declarations[initial]
			v := p.Variables[slot]
			if initializer.Slot != slot || initializer.Start != decl.DefaultStart || initializer.End != decl.DefaultEnd || initializer.Delimiter != decl.DefaultDelimiter || block.Bindings[decl.Name.Text] != slot || v.Name != decl.Name.Text || v.Role != PLOrdinarySlot || v.Constant != decl.Constant || v.NotNull != decl.NotNull || !reflect.DeepEqual(initializer.Expression, decl.Default) || !query(initializer.Query, decl.Default) {
				return invalid()
			}
			if !plSourceSpan(d.Source, decl.Default, decl.DefaultStart, decl.DefaultEnd, decl.DefaultDelimiter) {
				return invalid()
			}
			if decl.Default == nil {
				if initializer.Scope != nil {
					return invalid()
				}
			} else if !samePLScope(initializer.Scope, visible) {
				return invalid()
			}
			if err := diagnostics.expression(p, d.Source, decl.DefaultStart, decl.DefaultEnd, scopes); err != nil {
				return invalid()
			}
			if decl.Cursor != nil {
				recipe := v.Cursor
				if decl.Collation != nil || recipe == nil || recipe.Syntax != decl.Cursor || recipe.Slot != slot || v.Record || v.Usage.Type == nil || v.Usage.Type.ID() != sqltype.Refcursor || v.InheritInputCollation || len(recipe.Arguments) != len(decl.Cursor.Arguments) || len(recipe.Scopes) != len(scopes)+1 {
					return invalid()
				}
				if !plQuerySpan(d.Source, decl.Cursor.Query, decl.Cursor.QueryStart, decl.Cursor.QueryEnd, decl.Cursor.QueryDelimiter) {
					return invalid()
				}
				for i, scope := range scopes {
					if !samePLScope(recipe.Scopes[i], scope) {
						return invalid()
					}
				}
				argumentScope := recipe.Scopes[len(scopes)]
				if argumentScope == nil || argumentScope.Label != decl.Name.Text || len(argumentScope.Bindings) != len(recipe.Arguments) {
					return invalid()
				}
				cursorSlot := slot
				slot++
				argumentVisible := &PLBlock{Label: decl.Name.Text, Bindings: make(map[string]int)}
				argumentScopes := append(scopes, argumentVisible)
				for i, arg := range decl.Cursor.Arguments {
					if slot >= len(p.Variables) || recipe.Arguments[i] != slot {
						return invalid()
					}
					argument := p.Variables[slot]
					bound, ok := argumentScope.Bindings[arg.Name.Text]
					inherit := argument.Usage.Collation.ID != 0 && len(arg.Type.Copy.Parts) == 0
					if len(arg.Type.Copy.Parts) > 0 && !arg.Type.Row {
						if source, found := p.Lookup(plNames(arg.Type.Copy), argumentScopes); found {
							inherit = p.Variables[source].InheritInputCollation
						}
					}
					if arg.Collation != nil || !ok || bound != slot || argument.Name != arg.Name.Text || argument.Role != PLCursorArgumentSlot || argument.Record || argument.Constant || argument.NotNull || argument.Cursor != nil || argument.InheritInputCollation != inherit {
						return invalid()
					}
					if _, exists := argumentVisible.Bindings[arg.Name.Text]; exists {
						return invalid()
					}
					argumentVisible.Bindings[arg.Name.Text] = slot
					slot++
				}
				if !samePLScope(argumentScope, argumentVisible) {
					return invalid()
				}
				if err := diagnostics.cursorDeclaration(p, d.Source, decl.Cursor, argumentScopes); err != nil {
					return invalid()
				}
				visible.Bindings[decl.Name.Text] = cursorSlot
			} else {
				if v.Cursor != nil || v.Record && v.Usage != (sqltype.Usage{}) && (v.Usage.Type == nil || v.Usage.Type.ID() != 2249) {
					return invalid()
				}
				inherit := !v.Record && v.Usage.Collation.ID != 0 && len(decl.Type.Copy.Parts) == 0
				if len(decl.Type.Copy.Parts) > 0 && !decl.Type.Row {
					if source, found := p.Lookup(plNames(decl.Type.Copy), scopes); found {
						inherit = p.Variables[source].InheritInputCollation
					}
				}
				if decl.Collation != nil {
					if v.Record || !v.Usage.Valid() || v.Usage.Type.Usage().Collation.ID == 0 || v.Usage.Collation.ID == 0 || !v.Usage.Collation.Valid() {
						return invalid()
					}
					inherit = false
				}
				if v.InheritInputCollation != inherit {
					return invalid()
				}
				visible.Bindings[decl.Name.Text] = slot
				slot++
			}
			initial++
		}
		if initial != len(block.Declarations) || !maps.Equal(visible.Bindings, block.Bindings) {
			return invalid()
		}
		if err := walkList(block.Statements, raw.Statements, scopes); err != nil {
			return err
		}
		if len(block.Handlers) != len(raw.Handlers) {
			return invalid()
		}
		if len(raw.Handlers) == 0 {
			if block.HandlerScope != nil {
				return invalid()
			}
			return nil
		}
		scope := block.HandlerScope
		if scope == nil || scope.Control != nil || scope.FunctionScope || scope.Syntax != nil || scope.Label != "" || len(scope.Bindings) != 2 || len(scope.Declarations) != 0 || len(scope.Statements) != 0 || len(scope.Handlers) != 0 || scope.HandlerScope != nil {
			return invalid()
		}
		for _, name := range []string{"sqlstate", "sqlerrm"} {
			if slot >= len(p.Variables) {
				return invalid()
			}
			v := p.Variables[slot]
			bound, found := scope.Bindings[name]
			if !found || bound != slot || v.Name != name || !v.Constant || v.Record || v.NotNull || v.InheritInputCollation || v.Role != PLOrdinarySlot || v.Cursor != nil || !v.Usage.Valid() || v.Usage.Type.ID() != sqltype.Text {
				return invalid()
			}
			slot++
		}
		handlerScopes := append(scopes, scope)
		for i, handler := range block.Handlers {
			rawHandler := &raw.Handlers[i]
			if handler.Syntax != rawHandler || len(handler.Conditions) != len(rawHandler.Conditions) || len(handler.Conditions) == 0 {
				return invalid()
			}
			for j, condition := range handler.Conditions {
				want, err := compilePLCondition(rawHandler.Conditions[j])
				if err != nil || condition.Others != want.Others || !slices.Equal(condition.Codes, want.Codes) {
					return invalid()
				}
			}
			if err := walkList(handler.Statements, rawHandler.Statements, handlerScopes); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(p.Root.Statements[0].Block, p.Syntax, []*PLBlock{p.Root}); err != nil {
		return err
	}
	if slot != len(p.Variables) || searches != p.SearchCount {
		return invalid()
	}
	return nil
}

func plQueryShape(query *parser.Select, pos parser.Position, alias ...parser.Identifier) bool {
	if query == nil || len(query.Targets) != 1 {
		return false
	}
	expected := parser.Select{Targets: query.Targets, Pos: pos}
	if len(alias) != 0 && alias[0].Text != "" {
		expected.Labels = alias
	}
	return reflect.DeepEqual(query, &expected)
}

func plPrivateReferences(query *parser.Select, allowed string) bool {
	count := 0
	err := parser.VisitQueryExpressions(query, func(raw parser.Expr) error {
		column, ok := raw.(parser.ColumnExpr)
		if !ok {
			return nil
		}
		for _, part := range column.Name.Parts {
			if strings.ContainsRune(part.Text, 0) {
				if allowed == "" || len(column.Name.Parts) != 1 || part.Text != allowed {
					return fmt.Errorf("stuntgres: foreign PL search reference")
				}
				count++
			}
		}
		return nil
	})
	if allowed != "" {
		return err == nil && count == 1
	}
	return err == nil && count == 0
}

func plSourceSpan(source string, raw parser.Expr, start, end, delimiter parser.Position) bool {
	if raw == nil {
		return start == (parser.Position{}) && end == (parser.Position{}) && delimiter == (parser.Position{})
	}
	return plSourceRange(source, raw.SourcePosition(), start, end, delimiter)
}

func plSourceRange(source string, position, start, end, delimiter parser.Position) bool {
	if start.Character <= 0 || end.Character < start.Character || delimiter.Character < end.Character || start.Offset < 0 || start.Offset >= end.Offset || end.Offset > delimiter.Offset || delimiter.Offset > len(source) {
		return false
	}
	if position.Offset < start.Offset || position.Offset >= end.Offset {
		return false
	}
	// Check local character deltas without recounting the preceding body.
	// The positioned raw node anchors the immutable grammar-owned span.
	return position.Character == start.Character+utf8.RuneCountInString(source[start.Offset:position.Offset]) &&
		end.Character == position.Character+utf8.RuneCountInString(source[position.Offset:end.Offset]) &&
		delimiter.Character == end.Character+utf8.RuneCountInString(source[end.Offset:delimiter.Offset])
}

func plQuerySpan(source string, raw *parser.Select, start, end, delimiter parser.Position) bool {
	if raw == nil {
		return plSourceSpan(source, nil, start, end, delimiter)
	}
	return plSourceRange(source, raw.Pos, start, end, delimiter)
}

func samePLTargetStep(a, b parser.TargetStep) bool {
	if (a.Field == nil) != (b.Field == nil) || a.Subscript != b.Subscript {
		return false
	}
	return a.Field == nil || *a.Field == *b.Field
}

func samePLScope(a, b *PLBlock) bool {
	return a != nil && b != nil && a.Control == nil && a.Label == b.Label && a.FunctionScope == b.FunctionScope && a.Syntax == nil && len(a.Declarations) == 0 && len(a.Statements) == 0 && a.HandlerScope == nil && len(a.Handlers) == 0 && maps.Equal(a.Bindings, b.Bindings)
}

// SameLayout excludes typed Usage, which the caller compares through its mapping
// authority; every cursor recipe, target and implicit namespace remains semantic.
func (p *PLProgram) SameLayout(other *PLProgram) bool {
	if p == nil || other == nil {
		return p == other
	}
	if p.FormalCount != other.FormalCount || p.FoundSlot != other.FoundSlot || p.SearchCount != other.SearchCount || p.Invocation != other.Invocation || p.Function != other.Function || p.Procedure != other.Procedure || p.Source != other.Source || len(p.Variables) != len(other.Variables) || !reflect.DeepEqual(p.Root, other.Root) {
		return false
	}
	for i, v := range p.Variables {
		if v.Role != other.Variables[i].Role || !reflect.DeepEqual(v.Cursor, other.Variables[i].Cursor) {
			return false
		}
	}
	return true
}

func (p *PLProgram) validCursorInstruction(raw *parser.PLCursorStatement, in *PLCursorInstruction, scopes []*PLBlock) bool {
	if (raw.Action == parser.PLForCursor) != (raw.Loop != nil) {
		return false
	}
	if !plQuerySpan(p.Source, raw.Query, raw.QueryStart, raw.QueryEnd, raw.QueryDelimiter) ||
		!plSourceSpan(p.Source, raw.Command, raw.CommandStart, raw.CommandEnd, raw.CommandDelimiter) ||
		!plSourceSpan(p.Source, raw.Motion.Count, raw.Motion.CountStart, raw.Motion.CountEnd, raw.Motion.CountDelimiter) {
		return false
	}
	for _, value := range raw.Using {
		if value.Expression == nil || !plSourceSpan(p.Source, value.Expression, value.Start, value.End, value.Delimiter) {
			return false
		}
	}
	for _, actual := range raw.Arguments {
		if actual.Expression == nil || !plSourceSpan(p.Source, actual.Expression, actual.Start, actual.End, actual.Delimiter) {
			return false
		}
	}
	var targetSource parser.Expr
	if len(raw.Targets) != 0 {
		targetSource = raw.Targets[0]
	}
	if !plSourceSpan(p.Source, targetSource, raw.TargetsStart, raw.TargetsEnd, raw.TargetsDelimiter) {
		return false
	}
	target, err := p.ResolveTarget(raw.Variable, scopes)
	if err != nil || len(target.Steps) != 0 || target.Slot != in.Slot || in.Slot < 0 || in.Slot >= len(p.Variables) {
		return false
	}
	v := p.Variables[in.Slot]
	if v.Record || !v.Usage.Valid() || v.Usage.Type.ID() != sqltype.Refcursor || raw.Action < parser.PLOpenCursor || raw.Action > parser.PLForCursor {
		return false
	}
	if raw.Action == parser.PLFetchCursor && raw.Motion.Counted {
		return false
	}
	if len(raw.Targets) != len(in.Targets) {
		return false
	}
	for i, source := range raw.Targets {
		target, err := p.ResolveTarget(source, scopes)
		if err != nil || !reflect.DeepEqual(target, in.Targets[i]) || p.Variables[target.Slot].Constant {
			return false
		}
	}
	if raw.Action != parser.PLOpenCursor && raw.Action != parser.PLForCursor {
		return len(in.Arguments) == 0 && in.ArgumentQuery == nil
	}
	if v.Cursor == nil {
		return raw.Action != parser.PLForCursor && !raw.HasArguments && len(in.Arguments) == 0 && in.ArgumentQuery == nil && (raw.Query != nil || raw.Command != nil)
	}
	recipe := v.Cursor
	if raw.Query != nil || raw.Command != nil || len(in.Arguments) != len(recipe.Arguments) || len(raw.Arguments) != len(recipe.Arguments) || raw.HasArguments != (len(recipe.Arguments) > 0) {
		return false
	}
	for j, slot := range recipe.Arguments {
		found := false
		for i, actual := range raw.Arguments {
			matches := actual.Name.Text == p.Variables[slot].Name
			if actual.Name.Text == "" {
				matches = i == j
			}
			if !matches {
				continue
			}
			if found || in.Arguments[j].Slot != slot || in.Arguments[j].Scope != nil || in.Arguments[j].Query != nil || in.Arguments[j].Start != actual.Start || in.Arguments[j].End != actual.End || in.Arguments[j].Delimiter != actual.Delimiter || !reflect.DeepEqual(in.Arguments[j].Expression, actual.Expression) {
				return false
			}
			found = true
		}
		if !found {
			return false
		}
	}
	return reflect.DeepEqual(in.ArgumentQuery, p.cursorArgumentQuery(in.Arguments, raw.Arguments)) && (in.ArgumentQuery == nil || plPrivateReferences(in.ArgumentQuery, ""))
}

func emptyPLInstructionPayload(in PLInstruction) bool {
	return in.Dynamic == nil && in.SQL == nil && in.Into == nil && in.IntegerFor == nil && in.Foreach == nil && in.Raise == nil && in.Assert == nil && in.QueryLoop == nil && in.Query == nil && in.Cursor == nil && in.If == nil && in.Case == nil && in.Loop == nil && in.While == nil && in.Transfer == nil && in.Target.Slot == 0 && len(in.Target.Steps) == 0
}
