package stored

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// DefinitionSQL presents a checked SQL-standard body without executable
// re-admission. The immutable owner's manifest remains authoritative even after
// a callee implementation is replaced; labels come from this statement's read.
func (p *RoutineProgram) DefinitionSQL(ctx context.Context, view catalog.Read, d *catalog.Procedure, presentation Presentation) (string, error) {
	if p == nil || d == nil || view.State() == nil || view.Get(d.Address()) != d || d.SQLBody != p { return "", ids.ErrCatalogReplaced }
	if err := p.ValidateDefinition(d); err != nil { return "", err }
	if err := p.ValidateMetadata(view); err != nil { return "", err }
	presentation.Deparse = true
	presentation.Pretty = false
	presentation.RoutineDeclaration = true
	presentation.RoutineInputNames = d.SQL.Names
	presentation.Indent = 0
	if !d.SQL.StandardReturn {
		// Atomic statements have one leading space; carry that offset into
		// continuation clauses through the existing query layout policy.
		presentation.Indent = 1
	}
	if d.SQL.StandardReturn && p.Len() != 1 { return "", fmt.Errorf("stuntgres: invalid SQL-standard RETURN body") }
	var out strings.Builder
	if !d.SQL.StandardReturn { out.WriteString("BEGIN ATOMIC\n") }
	for i, action := range p.actions {
		if err := ctx.Err(); err != nil { return "", err }
		render := ruleRendering{RuleAction: action, policy: presentation, temporaryOwner: presentation.TemporaryOwner}
		var text string
		var err error
		if raw, ok := d.SQL.Statements[i].(*parser.Select); ok && raw.RoutineReturn {
			text, err = routineReturnSQL(ctx, view, render)
		} else {
			if d.SQL.StandardReturn { return "", fmt.Errorf("stuntgres: missing SQL-standard RETURN statement") }
			text, err = ruleActionSQL(ctx, view, render)
			text = strings.TrimLeft(text, " ")
		}
		if err != nil { return "", err }
		if !d.SQL.StandardReturn { out.WriteByte(' ') }
		out.WriteString(text)
		if !d.SQL.StandardReturn { out.WriteByte(';') }
		out.WriteByte('\n')
	}
	if !d.SQL.StandardReturn { out.WriteString("END\n") }
	return out.String(), nil
}

func routineReturnSQL(ctx context.Context, view catalog.Read, render ruleRendering) (string, error) {
	action := render.RuleAction
	selecting, ok := action.syntax.(*parser.Select)
	if !ok || len(selecting.Targets) != 1 || selecting.From != nil {
		return "", fmt.Errorf("stuntgres: invalid retained RETURN syntax")
	}
	// Like ruleActionSQL's SELECT path, render the normalized captured root,
	// not the original command spelling (which carries no occurrence authority).
	selecting = action.presentation.syntax
	if selecting == nil || len(selecting.Targets) != 1 || selecting.From != nil {
		return "", fmt.Errorf("stuntgres: invalid frozen RETURN projection")
	}
	r := render.renderer(action.presentation)
	r.Frame = action.presentation.query.Root.(*plan.ProjectNode).Frame
	text, err := r.ExpressionSQL(ctx, view, selecting.Targets[0], analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
	if err != nil { return "", err }
	return "RETURN " + text, nil
}
