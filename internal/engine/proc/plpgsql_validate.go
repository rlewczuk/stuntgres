package proc

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ValidateDefinition checks slot bounds and exact parser/block associations.
// Catalog ownership of each typed Usage remains the procedure provider's job.
func (p *PLProgram) ValidateDefinition(d *Definition) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid checked PL program") }
	if p == nil || d == nil || d.Kind != PLpgSQLBody || d.PL != p || p.Syntax == nil || p.Syntax != d.PLSyntax || p.Root == nil || p.Root.Syntax != nil || len(p.Root.Statements) != 1 || p.Root.Statements[0].Block == nil || p.FormalCount != len(d.Formals) || len(p.Variables) < p.FormalCount || p.Function != d.Name {
		return invalid()
	}
	outer := p.Root.Statements[0]
	if p.Root.Label != p.Function || len(p.Root.Declarations) != 0 || len(p.Root.Handlers) != 0 || p.Root.HandlerScope != nil || outer.Syntax != nil || outer.Query != nil || outer.Target.Slot != 0 || len(outer.Target.Steps) != 0 {
		return invalid()
	}
	bindings := p.FormalCount
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
	var walk func(*PLBlock, *parser.PLBlock, []*PLBlock) error
	walkStatements := func(instructions []PLInstruction, statements []parser.PLStatement, scopes []*PLBlock) error {
		if len(instructions) != len(statements) {
			return invalid()
		}
		for i, instruction := range instructions {
			statement := &statements[i]
			if instruction.Syntax != statement {
				return invalid()
			}
			switch {
			case statement.Block != nil:
				if instruction.Query != nil || statement.Rethrow || statement.Return || statement.Target != nil || statement.Expression != nil {
					return invalid()
				}
				if err := walk(instruction.Block, statement.Block, scopes); err != nil {
					return err
				}
			case statement.Rethrow:
				if statement.Return || statement.Target != nil || statement.Expression != nil || instruction.Block != nil || instruction.Query != nil || instruction.Target.Slot != 0 || len(instruction.Target.Steps) != 0 {
					return invalid()
				}
			default:
				if instruction.Block != nil || (instruction.Query == nil) != (statement.Expression == nil) {
					return invalid()
				}
				if !validPLExpressionQuery(instruction.Query, statement.Expression) {
					return invalid()
				}
				if !statement.Return {
					target, err := p.ResolveTarget(statement.Target, scopes)
					if err != nil || target.Slot < 0 || target.Slot >= len(p.Variables) || target.Slot != instruction.Target.Slot || !slices.EqualFunc(target.Steps, instruction.Target.Steps, samePLTargetStep) || p.Variables[target.Slot].Constant {
						return invalid()
					}
				} else if instruction.Target.Slot != 0 || len(instruction.Target.Steps) != 0 {
					return invalid()
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
			if initializer.Slot != slot || block.Bindings[decl.Name.Text] != slot || variable.Name != decl.Name.Text || variable.Constant != decl.Constant || variable.NotNull != decl.NotNull || (initializer.Expression == nil) != (decl.Default == nil) || (initializer.Query == nil) != (decl.Default == nil) {
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
			if !reflect.DeepEqual(initializer.Expression, decl.Default) || !validPLExpressionQuery(initializer.Query, decl.Default) || initializer.Query != nil && initializer.Scope == nil {
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
		if err := walkStatements(block.Statements, raw.Statements, scopes); err != nil {
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
			if err := walkStatements(handler.Statements, rawHandler.Statements, handlerScopes); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(p.Root.Statements[0].Block, p.Syntax, []*PLBlock{p.Root}); err != nil {
		return err
	}
	if slot != len(p.Variables) {
		return invalid()
	}
	return nil
}

// Compiler-created expression SELECTs own no query clauses beyond their one
// parser-owned target. Compare the whole shape so new SELECT fields cannot
// silently become unchecked executable structure.
func validPLExpressionQuery(query *parser.Select, expression parser.Expr) bool {
	return reflect.DeepEqual(query, plExpressionQuery(expression))
}

func samePLTargetStep(a, b parser.TargetStep) bool {
	if (a.Field == nil) != (b.Field == nil) || a.Subscript != b.Subscript {
		return false
	}
	return a.Field == nil || *a.Field == *b.Field
}
