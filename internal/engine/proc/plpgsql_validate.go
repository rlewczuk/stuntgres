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
)

// ValidateDefinition checks slot bounds and exact parser/block associations.
// Catalog ownership of each typed Usage remains the procedure provider's job.
func (p *PLProgram) ValidateDefinition(d *Definition) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid checked PL program") }
	if p == nil || d == nil || d.Kind != PLpgSQLBody || d.PL != p || p.Syntax == nil || p.Syntax != d.PLSyntax || p.Root == nil || p.Root.Syntax != nil || len(p.Root.Statements) != 1 || p.Root.Statements[0].Block == nil || p.FormalCount != len(d.Formals) || len(p.Variables) < p.FormalCount || p.Function != d.Name || p.SearchCount < 0 {
		return invalid()
	}
	outer := p.Root.Statements[0]
	if p.Root.Label != p.Function || len(p.Root.Declarations) != 0 || len(p.Root.Handlers) != 0 || p.Root.HandlerScope != nil || outer.Syntax != nil || outer.Query != nil || outer.Target.Slot != 0 || len(outer.Target.Steps) != 0 {
		return invalid()
	}
	bindings := p.FormalCount
	if p.Trigger != d.Trigger || d.Trigger && (p.Procedure || len(d.Formals) != 0 || d.Result.Type == nil || d.Result.Type.ID() != 2279 || len(p.Variables) < TriggerSlotCount) {
		return invalid()
	}
	if d.Trigger {
		bindings = TriggerSlotCount
		for i, name := range triggerNames {
			v := p.Variables[i]
			if v.Name != name || p.Root.Bindings[name] != i || v.Record != (i < 2) || v.Constant || v.NotNull || v.InheritInputCollation {
				return invalid()
			}
			if i >= 2 {
				if v.Usage.Type == nil || uint32(v.Usage.Type.ID()) != triggerTypes[i] || v.Usage != v.Usage.Type.Usage() {
					return invalid()
				}
			}
		}
	}
	for i, formal := range d.Formals {
		variable := p.Variables[i]
		positional := "$" + strconv.Itoa(i+1)
		bound, exists := p.Root.Bindings[positional]
		if !exists || variable.Name != formal.Name || variable.Usage != formal.Usage || variable.Record || variable.Constant || variable.NotNull || variable.InheritInputCollation != (formal.Usage.Collation.ID != 0) || bound != i {
			return invalid()
		}
		if formal.Name != "" {
			bindings++
			named, exists := p.Root.Bindings[formal.Name]
			if !exists || named != i || formal.Name == positional {
				return invalid()
			}
		}
	}
	if len(p.Root.Bindings) != bindings {
		return invalid()
	}
	slot := p.FormalCount
	var diagnostics plDiagnosticDatums
	if d.Trigger {
		slot = TriggerSlotCount
	}
	var walk func(*PLBlock, *parser.PLBlock, []*PLBlock) error
	searches := 0
	queries := make(map[*parser.Select]bool)
	query := func(checked *parser.Select, raw parser.Expr) bool {
		if raw == nil {
			return checked == nil
		}
		if checked == nil || queries[checked] || !plQueryShape(checked, raw.SourcePosition()) || !reflect.DeepEqual(checked.Targets[0], raw) || !plPrivateReferences(checked, "") {
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
			if checked.SearchName != diagnostics.finishCase(slot) {
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
			if err := diagnostics.target(p, d.Source, raw, scopes); err != nil {
				return invalid()
			}
			if err := diagnostics.expression(p, d.Source, raw.ExpressionStart, raw.ExpressionEnd, scopes); err != nil {
				return invalid()
			}
			variants := 0
			for _, present := range []bool{raw.Block != nil, raw.If != nil, raw.Case != nil, raw.Return, raw.Rethrow, raw.Target != nil} {
				if present {
					variants++
				}
			}
			if variants != 1 {
				return invalid()
			}
			switch {
			case raw.Block != nil:
				if instruction.Query != nil || instruction.If != nil || instruction.Case != nil || walk(instruction.Block, raw.Block, scopes) != nil {
					return invalid()
				}
			case raw.If != nil || raw.Case != nil:
				if instruction.Query != nil || instruction.Block != nil {
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
				if raw.Return || raw.Target != nil || raw.Expression != nil || instruction.Block != nil || instruction.If != nil || instruction.Case != nil || instruction.Query != nil || instruction.Target.Slot != 0 || len(instruction.Target.Steps) != 0 {
					return invalid()
				}
			default:
				if instruction.Block != nil || instruction.If != nil || instruction.Case != nil || !query(instruction.Query, raw.Expression) {
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
					if raw.Expression != nil && policy.Code != "" || raw.Expression == nil && !p.Procedure && !d.Void() && len(d.OutputFormals) == 0 {
						return invalid()
					}
				}
			}
		}
		return nil
	}
	walk = func(block *PLBlock, raw *parser.PLBlock, scopes []*PLBlock) error {
		if block == nil || raw == nil || block.Syntax != raw || block.Label != raw.Label.Text || len(block.Statements) != len(raw.Statements) {
			return invalid()
		}
		for _, bound := range block.Bindings {
			if bound < 0 || bound >= len(p.Variables) {
				return invalid()
			}
		}
		initial := 0
		visible := &PLBlock{Label: block.Label, Bindings: make(map[string]int)}
		scopes = append(scopes, visible)
		for _, decl := range raw.Declarations {
			if decl.Alias != nil {
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
			variable := p.Variables[slot]
			if initializer.Slot != slot || initializer.Start != decl.DefaultStart || initializer.End != decl.DefaultEnd || initializer.Delimiter != decl.DefaultDelimiter || block.Bindings[decl.Name.Text] != slot || variable.Name != decl.Name.Text || variable.Constant != decl.Constant || variable.NotNull != decl.NotNull || !reflect.DeepEqual(initializer.Expression, decl.Default) || !query(initializer.Query, decl.Default) {
				return invalid()
			}
			if !plSourceSpan(d.Source, decl.Default, decl.DefaultStart, decl.DefaultEnd, decl.DefaultDelimiter) {
				return invalid()
			}
			inherit := !variable.Record && variable.Usage.Collation.ID != 0 && len(decl.Type.Copy.Parts) == 0
			if len(decl.Type.Copy.Parts) > 0 && !decl.Type.Row {
				if source, found := p.Lookup(plNames(decl.Type.Copy), scopes); found {
					inherit = p.Variables[source].InheritInputCollation
				}
			}
			if variable.InheritInputCollation != inherit {
				return invalid()
			}
			if initializer.Query != nil && (initializer.Scope == nil || initializer.Scope.Label != visible.Label || !maps.Equal(initializer.Scope.Bindings, visible.Bindings)) {
				return invalid()
			}
			if err := diagnostics.expression(p, d.Source, decl.DefaultStart, decl.DefaultEnd, scopes); err != nil {
				return invalid()
			}
			visible.Bindings[decl.Name.Text] = slot
			initial++
			slot++
		}
		if initial != len(block.Declarations) {
			return invalid()
		}
		if !maps.Equal(visible.Bindings, block.Bindings) {
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
		if scope == nil || scope.Syntax != nil || scope.Label != "" || len(scope.Bindings) != 2 || len(scope.Declarations) != 0 || len(scope.Statements) != 0 || len(scope.Handlers) != 0 || scope.HandlerScope != nil {
			return invalid()
		}
		for _, name := range []string{"sqlstate", "sqlerrm"} {
			if slot >= len(p.Variables) {
				return invalid()
			}
			v := p.Variables[slot]
			bound, found := scope.Bindings[name]
			if !found || bound != slot || v.Name != name || !v.Constant || v.Record || v.NotNull || v.InheritInputCollation || !v.Usage.Valid() || v.Usage.Type.ID() != sqltype.Text {
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

func plQueryShape(query *parser.Select, pos parser.Position) bool {
	if query == nil || len(query.Targets) != 1 {
		return false
	}
	expected := parser.Select{Targets: query.Targets, Pos: pos}
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
	if start.Character <= 0 || end.Character < start.Character || delimiter.Character < end.Character || start.Offset < 0 || start.Offset >= end.Offset || end.Offset > delimiter.Offset || delimiter.Offset > len(source) {
		return false
	}
	position := raw.SourcePosition()
	return position.Offset >= start.Offset && position.Offset < end.Offset
}

func samePLTargetStep(a, b parser.TargetStep) bool {
	if (a.Field == nil) != (b.Field == nil) || a.Subscript != b.Subscript {
		return false
	}
	return a.Field == nil || *a.Field == *b.Field
}
