package core

import (
	"fmt"
	"maps"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (r *explainRenderer) sourceDetails(tree *explainTree, source *plan.SourceNode, relation *catalog.Relation) error {
	var leaf exec.Leaf
	if source.Occurrence >= 0 && source.Occurrence < len(r.leaves) && r.leaves[source.Occurrence].Ref.Address == source.Ref.Address {
		leaf = r.leaves[source.Occurrence]
	}
	principal := r.auth.State.CurrentUser
	if leaf.Principal != (authorization.PrincipalRef{}) {
		principal = leaf.Principal
	}
	if relation.RelationKind == catalog.ViewRelation {
		if r.viewActive[relation.Address()] {
			return fmt.Errorf("stuntgres: recursive stored EXPLAIN source")
		}
		definition := stored.ViewOf(relation)
		if definition == nil {
			return fmt.Errorf("stuntgres: missing stored EXPLAIN source")
		}
		auth, err := r.scope.Engine().authorizeViewSourceLocked(r.scope, source.Ref, definition, principal, leaf.Mark)
		if err != nil {
			return err
		}
		auth.State.CurrentUser = principal
		if !definition.Invoker() {
			auth.State.CurrentUser = relation.Owner
		}
		r.viewActive[relation.Address()] = true
		frames, occurrences, priorAuth, leaves := r.frames, r.occurrences, r.auth, r.leaves
		priorInherited := r.inheritedMark
		priorForeign := r.foreign
		priorOwners := r.foreignOwners
		r.foreignOwners = nil
		// A stored producer resets all three flags. An ordinary derived query
		// preserves Outer/Invocation but recomputes identity demand.
		r.foreign = explainForeignContext{}
		inherited := exec.MergeQueryRowMark(r.inheritedMark, leaf.Mark)
		r.inheritedMark = plan.RowMark{}
		r.frames, r.occurrences, r.auth = maps.Clone(frames), maps.Clone(occurrences), auth
		r.leaves = make([]exec.Leaf, len(definition.Sources()))
		for i, leaf := range definition.Sources() {
			r.leaves[i] = exec.Leaf{Ref: leaf.Ref, Only: leaf.Only, Mark: stored.ViewLeafRowMark(definition, i, inherited)}
			if relation, ok := auth.Catalog.Relation(leaf.Ref.Address); ok {
				r.leaves[i].Relation = relation
				r.leaves[i].Uses = make([]int, 0, len(leaf.Reads))
				for _, attribute := range leaf.Reads {
					slot := relation.IndexForAttribute(attribute)
					if slot < 0 {
						return fmt.Errorf("stuntgres: invalid stored EXPLAIN attribute")
					}
					r.leaves[i].Uses = append(r.leaves[i].Uses, slot)
				}
			}
		}
		child, err := r.query(definition.Query())
		r.frames, r.occurrences, r.auth, r.leaves = frames, occurrences, priorAuth, leaves
		r.inheritedMark = priorInherited
		r.foreign = priorForeign
		r.foreignOwners = priorOwners
		delete(r.viewActive, relation.Address())
		if err != nil {
			return err
		}
		tree.children = append(tree.children, child)
	}
	if r.options.verbose {
		outputs := make([]string, len(source.Schema()))
		for i := range outputs {
			text, _, err := r.slot(source, i)
			if err != nil {
				return err
			}
			outputs[i] = text
		}
		tree.details = append(tree.details, "Output: "+strings.Join(outputs, ", "))
		r.property(tree, "Output", outputs)
	}
	command, reads := catalog.PolicySelect, false
	if leaf.Mark.Mode != 0 {
		command, reads = catalog.PolicyUpdate, true
	}
	if source.Occurrence < 0 && relation.Address() == r.policyTarget {
		command, reads, principal = r.policyCommand, r.policyReads, r.policyPrincipal
	}
	condition, err := r.rowSecurity(relation, command, false, reads, principal)
	if err != nil {
		return err
	}
	if condition != nil {
		frames := r.frames
		r.frames = maps.Clone(frames)
		r.frames[1] = source
		text, err := r.expression(source, condition)
		r.frames = frames
		if err != nil {
			return err
		}
		tree.details = append(tree.details, "Row Security Filter: "+text)
		r.property(tree, "Row Security Filter", text)
		tree.restriction = condition
	}
	return nil
}

// Import the selected immutable policy recipes without runtime preflight or
// materialization. AND/OR reproduce the existing RLS selection's actual groups.
func (r *explainRenderer) rowSecurity(relation *catalog.Relation, command catalog.PolicyCommand, proposed, reads bool, principal authorization.PrincipalRef) (expr.Expr, error) {
	if !relation.Security.RLSEnabled {
		return nil, nil
	}
	auth := r.auth
	auth.State.CurrentUser = principal
	selection, err := access.SelectRowSecurity(auth.Roles, relation, principal, command, proposed, reads)
	if err != nil || selection.Count == 0 {
		return nil, err
	}
	if selection.Denied() {
		boolean, _ := r.auth.Catalog.LookupType(sqltype.Bool)
		return plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}, nil
	}
	combine := func(left, right expr.Expr, operator parser.LogicalOperator) expr.Expr {
		if left == nil {
			return right
		}
		if right == nil {
			return left
		}
		return plan.LogicalExpr{Info: expr.InfoOf(left), Op: operator, Left: left, Right: right}
	}
	var result expr.Expr
	for _, group := range selection.Groups[:selection.Count] {
		var permissive, restrictive expr.Expr
		for i, clauses := range [][]access.RLSClause{group.Permissive, group.Restrictive} {
			for _, clause := range clauses {
				policy := stored.PolicyOf(clause.Payload)
				if policy == nil {
					return nil, fmt.Errorf("stuntgres: missing EXPLAIN policy")
				}
				if err := view.CheckPolicyAuthority(auth.View, auth, relation, policy); err != nil {
					return nil, err
				}
				imported, err := view.ImportPolicyClause(auth.Catalog, r.scope.Engine().Catalog.Domain, relation, policy, principal)
				if err != nil {
					return nil, err
				}
				if i == 0 {
					permissive = combine(permissive, imported.Node, parser.LogicalOr)
				} else {
					restrictive = combine(restrictive, imported.Node, parser.LogicalAnd)
				}
			}
		}
		result = combine(result, combine(permissive, restrictive, parser.LogicalAnd), parser.LogicalAnd)
	}
	return result, nil
}

func (r *explainRenderer) sortKeys(root plan.Node, keys []plan.SortKey) (string, error) {
	parts, err := r.sortKeyParts(root, keys)
	return strings.Join(parts, ", "), err
}

func (r *explainRenderer) sortKeyParts(root plan.Node, keys []plan.SortKey) ([]string, error) {
	parts := make([]string, len(keys))
	for i, key := range keys {
		text, err := r.expression(root, key.Expression)
		if err != nil {
			return nil, err
		}
		if key.Desc {
			text += " DESC"
		} else {
			text += " ASC"
		}
		if key.NullsFirst {
			text += " NULLS FIRST"
		} else {
			text += " NULLS LAST"
		}
		parts[i] = text
	}
	return parts, nil
}

func (r *explainRenderer) windowCall(node *plan.WindowNode, index int) (string, error) {
	if index < 0 || index >= len(node.Calls) || index >= len(node.SpecIndices) {
		return "", fmt.Errorf("stuntgres: missing EXPLAIN window specification")
	}
	specIndex := node.SpecIndices[index]
	if specIndex < 0 || specIndex >= len(node.Specs) {
		return "", fmt.Errorf("stuntgres: invalid EXPLAIN window specification")
	}
	call := node.Calls[index]
	call.Spec = node.Specs[specIndex]
	root := node.Child
	if call.Procedure == nil {
		return "", fmt.Errorf("stuntgres: missing EXPLAIN window declaration")
	}
	arguments := call.Arguments
	arguments = arguments[:len(arguments):len(arguments)]
	if len(arguments) == 0 && call.Argument != nil {
		arguments = append(arguments, call.Argument)
	}
	if call.Ordinal != nil {
		arguments = append(arguments, call.Ordinal)
	}
	if call.DefaultValue != nil {
		arguments = append(arguments, call.DefaultValue)
	}
	args, err := r.expressions(root, arguments)
	if err != nil {
		return "", err
	}
	if args == "" && call.Operation == plan.WindowCount {
		args = "*"
	}
	var spec []string
	if len(call.Spec.Partition) != 0 {
		partition, err := r.expressions(root, call.Spec.Partition)
		if err != nil {
			return "", err
		}
		spec = append(spec, "PARTITION BY "+partition)
	}
	if len(call.Spec.Order) != 0 {
		order, err := r.sortKeys(root, call.Spec.Order)
		if err != nil {
			return "", err
		}
		spec = append(spec, "ORDER BY "+order)
	}
	frame := call.Spec.Frame
	unit := "RANGE"
	if frame.Unit == parser.FrameRows {
		unit = "ROWS"
	} else if frame.Unit == parser.FrameGroups {
		unit = "GROUPS"
	}
	bound := func(bound plan.WindowBound) (string, error) {
		switch bound.Kind {
		case parser.FrameUnboundedPreceding:
			return "UNBOUNDED PRECEDING", nil
		case parser.FrameCurrentRow:
			return "CURRENT ROW", nil
		case parser.FrameUnboundedFollowing:
			return "UNBOUNDED FOLLOWING", nil
		}
		text, err := r.expression(root, bound.Offset)
		if bound.Kind == parser.FrameOffsetPreceding {
			text += " PRECEDING"
		} else {
			text += " FOLLOWING"
		}
		return text, err
	}
	start, err := bound(frame.Start)
	if err != nil {
		return "", err
	}
	end, err := bound(frame.End)
	if err != nil {
		return "", err
	}
	frameText := unit + " BETWEEN " + start + " AND " + end
	switch frame.Exclusion {
	case parser.ExcludeCurrentRow:
		frameText += " EXCLUDE CURRENT ROW"
	case parser.ExcludeGroup:
		frameText += " EXCLUDE GROUP"
	case parser.ExcludeTies:
		frameText += " EXCLUDE TIES"
	}
	spec = append(spec, frameText)
	return r.objectName(call.Procedure.Namespace(), call.Procedure.Name()) + "(" + args + ") OVER (" + strings.Join(spec, " ") + ")", nil
}
