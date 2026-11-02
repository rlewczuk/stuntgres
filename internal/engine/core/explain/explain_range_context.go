package explain

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Scope is a binding coordinate, not a nesting depth. Reused stored graphs get
// independent environments; child queries inherit only their lexical ancestors.
type rangeCoordinate struct {
	scope, frame uint32
}

type rangeDemandContext struct {
	analysisOnly   bool
	executableRead bool
	markValidation bool
}

// A declaration retains its actual authority, sources and lexical environment,
// not those of whichever reader first happens to encounter its producer.
type rangeDeclaration struct {
	scope       *rangeScope
	environment *rangeEnvironment
}

func (c *rangeCapture) registerFrame(s *rangeScope, env *rangeEnvironment, frame uint32) {
	if frame == 0 {
		return
	}
	env.frames[rangeCoordinate{1, frame}] = s
	for _, definition := range env.localDefinitions {
		if definition.Owner != frame {
			continue
		}
		if declaration := env.declarations[definition]; declaration.environment == nil {
			env.declarations[definition] = rangeDeclaration{scope: s, environment: env}
		}
	}
}

// Do not cross a query or opaque producer boundary while discovering local
// frames. Registration precedes expression traversal, including child queries.
func (c *rangeCapture) registerFrames(s *rangeScope, env *rangeEnvironment, node plan.Node) {
	switch n := node.(type) {
	case nil, *plan.SourceNode, *plan.DerivedNode, *plan.WorktableScanNode:
		return
	case *plan.ProjectNode:
		c.registerFrame(s, env, n.Frame)
	case *plan.JoinNode:
		c.registerFrame(s, env, n.Frame)
	case *plan.FunctionFromNode:
		c.registerFrame(s, env, n.Frame)
	case *plan.SetProjectNode:
		c.registerFrame(s, env, n.Frame)
	}
	left, right := plan.QueryInputs(node)
	c.registerFrames(s, env, left)
	c.registerFrames(s, env, right)
}

func (c *rangeCapture) queryNodes(s *rangeScope, inherited *rangeEnvironment, root plan.Node) error {
	local := *inherited
	local.frames = maps.Clone(inherited.frames)
	c.registerFrames(s, &local, root)
	if err := c.node(s, &local, root); err != nil {
		return err
	}
	// Unreferenced definitions still belong to their declaration, and must be
	// captured while that declaration's lexical/authority context is available.
	for _, definition := range local.localDefinitions {
		if declaration := local.declarations[definition]; declaration.scope == s {
			if _, err := c.producer(&local, definition); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *rangeCapture) outerCoordinate(s *rangeScope, env *rangeEnvironment, scope, frame uint32) error {
	// Generated-owner recipes have no query coordinate.
	if scope == 0 && frame == 0 {
		return nil
	}
	owner := env.frames[rangeCoordinate{scope, frame}]
	if owner == nil {
		return fmt.Errorf("stuntgres: missing EXPLAIN lexical range owner (%d,%d)", scope, frame)
	}
	if owner == s {
		return nil
	}
	// Publish detached, unique IDs in declaration order. No child scope's
	// captures are copied to its parent, and no structural ancestry is inferred.
	owners, _ := s.value["Outer Scope IDs"].([]any)
	for i, previous := range owners {
		id := previous.(int)
		if id == owner.id {
			return nil
		}
		if owner.id < id {
			owners = append(owners, nil)
			copy(owners[i+1:], owners[i:])
			owners[i] = owner.id
			s.value["Outer Scope IDs"] = owners
			return nil
		}
	}
	s.value["Outer Scope IDs"] = append(owners, owner.id)
	return nil
}

func (c *rangeCapture) outerFrame(s *rangeScope, env *rangeEnvironment, frame uint32) error {
	if frame == 0 {
		return nil
	} // Optional frame carriers have no owner at zero.
	return c.outerCoordinate(s, env, 1, frame)
}

func (c *rangeCapture) expressionOwner(s *rangeScope, env *rangeEnvironment, expression expr.Expr) error {
	switch e := expression.(type) {
	case plan.ColumnExpr:
		return c.outerCoordinate(s, env, e.Scope, e.Frame)
	case plan.SystemAttributeExpr:
		return c.outerCoordinate(s, env, e.Scope, e.Frame)
	case plan.MergeActionExpr:
		return c.outerCoordinate(s, env, e.Scope, e.Frame)
	case plan.ReturningWholeRowExpr:
		return c.outerCoordinate(s, env, e.Scope, e.Frame)
	case plan.RowExpr:
		return c.outerFrame(s, env, e.PresenceFrame)
	case plan.GroupExpr:
		return c.outerFrame(s, env, e.Frame)
	case plan.AggregateExpr:
		return c.outerFrame(s, env, e.Frame)
	case *plan.WindowExpr:
		return c.outerFrame(s, env, e.Frame)
	}
	return nil
}
