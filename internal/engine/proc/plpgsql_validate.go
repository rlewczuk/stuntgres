package proc

import (
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
)

// ValidateDefinition checks slot bounds and exact parser/block associations.
// Catalog ownership of each typed Usage remains the procedure provider's job.
func (p *PLProgram) ValidateDefinition(d *Definition) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid checked PL program") }
	if p == nil || d == nil || d.Kind != PLpgSQLBody || d.PL != p || p.Syntax == nil || p.Syntax != d.PLSyntax || p.Root == nil || p.Root.Syntax != nil || len(p.Root.Statements) != 1 || p.Root.Statements[0].Block == nil || p.FormalCount != len(d.Formals) || len(p.Variables) < p.FormalCount || p.Function != d.Name {
		return invalid()
	}
	for i, formal := range d.Formals {
		variable := p.Variables[i]
		positional := "$" + strconv.Itoa(i+1)
		bound, exists := p.Root.Bindings[positional]
		if !exists || variable.Name != formal.Name || variable.Usage != formal.Usage || variable.Record || variable.Constant || variable.NotNull || variable.InheritInputCollation != (formal.Usage.Collation.ID != 0) || bound != i {
			return invalid()
		}
		if formal.Name != "" {
			named, exists := p.Root.Bindings[formal.Name]
			if !exists || named != i || formal.Name == positional {
				return invalid()
			}
		}
	}
	slot := p.FormalCount
	var walk func(*PLBlock, *parser.PLBlock, []*PLBlock) error
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
				if err != nil || len(target.Fields) != 0 || block.Bindings[decl.Name.Text] != target.Slot {
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
			if initializer.Query != nil && (len(initializer.Query.Targets) != 1 || initializer.Query.Pos != decl.Default.SourcePosition() || initializer.Scope == nil) {
				return invalid()
			}
			visible.Bindings[decl.Name.Text] = slot
			initial++
			slot++
		}
		if initial != len(block.Declarations) {
			return invalid()
		}
		for i, instruction := range block.Statements {
			statement := &raw.Statements[i]
			if instruction.Syntax != statement {
				return invalid()
			}
			if statement.Block != nil {
				if instruction.Query != nil {
					return invalid()
				}
				if err := walk(instruction.Block, statement.Block, scopes); err != nil {
					return err
				}
			} else {
				if instruction.Block != nil || (instruction.Query == nil) != (statement.Expression == nil) {
					return invalid()
				}
				if instruction.Query != nil && (len(instruction.Query.Targets) != 1 || instruction.Query.Pos != statement.Expression.SourcePosition()) {
					return invalid()
				}
				if !statement.Return && (instruction.Target.Slot < 0 || instruction.Target.Slot >= len(p.Variables) || p.Variables[instruction.Target.Slot].Constant) {
					return invalid()
				}
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
