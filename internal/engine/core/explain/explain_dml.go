package explain

import "fmt"

// ResolveCommands links modifying-CTE presentation to the already captured
// accepted child command, including children started lazily after parent
// capture. The existing family slice is the sole lookup owner. A successful
// modifying family must have finished its mandatory auxiliaries; missing or
// cyclic captures are errors, never static-source or zero-actual fallbacks.
func ResolveCommands(renderers []*Renderer) error {
	for _, renderer := range renderers {
		if err := renderer.resolveCommands(nil); err != nil {
			return err
		}
	}
	return nil
}

func (r *Renderer) resolveCommands(ancestors []*Renderer) error {
	if r.commandsResolved {
		return nil
	}
	for _, ancestor := range ancestors {
		if ancestor == r {
			return fmt.Errorf("stuntgres: cyclic EXPLAIN CTE command capture")
		}
	}
	ancestors = append(ancestors, r)
	var visit func(*Tree) error
	visit = func(tree *Tree) error {
		if tree.cteDefinition != nil {
			if r.Commands != nil {
				for _, child := range *r.Commands {
					if child != r && child.CTEOwner == tree.cteOwner && child.CTEDefinition == tree.cteDefinition {
						tree.commandRenderer = child
						break
					}
				}
			}
			if tree.commandRenderer == nil || tree.commandRenderer.CommandTree() == nil {
				return fmt.Errorf("stuntgres: missing accepted EXPLAIN CTE command capture")
			}
			if err := tree.commandRenderer.resolveCommands(ancestors); err != nil {
				return err
			}
			// Retire the stale static child graph; only the real captured
			// renderer owns effective provider and execution presentation.
			*tree = Tree{commandRenderer: tree.commandRenderer}
			return nil
		}
		for _, child := range tree.children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, tree := range r.trees {
		if err := visit(tree); err != nil {
			return err
		}
	}
	r.commandsResolved = true
	return nil
}
