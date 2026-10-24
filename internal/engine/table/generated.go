package table

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func GenerationRecipeExpression(definition catalog.ColumnGeneration) expr.Expr {
	if definition.Expression != nil {
		return definition.Expression
	}
	// Standalone internal provider fixtures may supply only an executable.
	// Published SQL generation descriptors require the declaration expression.
	return definition.Planned
}

// One execution owner uses one fixed generation program for this descriptor.
// ALTER borrows its existing selected-slot vector; ordinary writes demand all
// stored slots. Neither path admits unused recipes or checks rights per row.
func PrepareGenerationProgram(c *exec.Context, d *catalog.Relation, selected []int, consumer *catalog.Relation) ([]expr.Expr, error) {
	program, prepared := c.PreparedGenerations[d]
	if prepared {
		return program, nil
	}
	stored := false
	count := len(d.Attributes)
	if selected != nil {
		count = len(selected)
	}
	for i := range count {
		slot := i
		if selected != nil {
			slot = selected[i]
		}
		definition, exists := d.ColumnMeta.Generations[d.Attributes[slot]]
		if !exists || definition.Kind != catalog.GenerationStored {
			continue
		}
		stored = true
		recipe := *c
		recipe.RoutineRecipe, recipe.RoutineGraft = true, false
		recipe.RoutineChecks = nil
		recipe.RoutineReferences = definition.Dependencies
		recipe.RoutineConsumer = consumer
		node, changed, err := exec.PreflightCoercionsWithPolicy(&recipe, GenerationRecipeExpression(definition), true)
		c.PreparedDomainChecks = recipe.PreparedDomainChecks
		if err != nil {
			return nil, err
		}
		if err := exec.AdmitExpression(&recipe, node, recipe.Catalog); err != nil {
			return nil, err
		}
		if changed {
			if program == nil {
				program = make([]expr.Expr, len(d.Columns))
				for i, attribute := range d.Attributes {
					program[i] = GenerationRecipeExpression(d.ColumnMeta.Generations[attribute])
				}
			}
			program[slot] = node
		}
	}
	if stored {
		if c.PreparedGenerations == nil {
			c.PreparedGenerations = make(map[*catalog.Relation][]expr.Expr)
		}
		c.PreparedGenerations[d] = program
	}
	return program, nil
}

func EvaluateGenerations(ctx context.Context, d *catalog.Relation, row []sqltype.Value, c *exec.Context) error {
	if len(d.ColumnMeta.Generations) == 0 {
		return ctx.Err()
	}
	c.Context = ctx
	c.Scope = 1
	c.Frame = 1
	c.Row = row
	program, err := PrepareGenerationProgram(c, d, nil, d)
	if err != nil {
		return err
	}
	for slot, attribute := range d.Attributes {
		definition, ok := d.ColumnMeta.Generations[attribute]
		if !ok {
			continue
		}
		if definition.Kind == catalog.GenerationVirtual {
			row[slot] = sqltype.Null(d.Columns[slot].Usage.Type)
			continue
		}
		expression := GenerationRecipeExpression(definition)
		if program != nil {
			expression = program[slot]
		}
		value, err := expression.Eval(c)
		if err != nil {
			return err
		}
		row[slot] = value
	}
	return ctx.Err()
}

func ValidateGenerationProviders(view catalog.Read, d *catalog.Relation) error {
	for _, definition := range d.ColumnMeta.Generations {
		for _, ref := range definition.Dependencies {
			address := ref.Address
			address.SubID = 0
			if !stored.ReferenceEpochValid(view, d, ref) {
				return fmt.Errorf("stuntgres: missing generation provider")
			}
			if address == d.Address() && ref.Address.SubID != 0 {
				if _, generated := d.ColumnMeta.Generations[ref.Address.SubID]; generated {
					return fmt.Errorf("stuntgres: generated dependency on generated attribute")
				}
			}
			if address == d.Address() {
				if ref.Address.SubID != 0 && !d.HasAttribute(ref.Address.SubID) {
					return fmt.Errorf("stuntgres: missing generation source attribute")
				}
				continue
			}
			provider, ok := view.Descriptor(address)
			relation, relationProvider := provider.(*catalog.Relation)
			validRelation := relationProvider && relation.Address().ClassID == ids.RelationClassID
			if validRelation && ref.Address.SubID != 0 {
				rowType, exists := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
				validRelation = exists && stored.CompositeAttributeProvider(rowType, relation, ref.Address.SubID)
			}
			if !ok || provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.OperatorObject && provider.Kind() != ids.NamespaceObject && provider.Kind() != ids.TSDictionaryObject && provider.Kind() != ids.ProcedureObject && !validRelation || provider.Kind() == ids.NamespaceObject && ref.Address.SubID != 0 {
				return fmt.Errorf("stuntgres: invalid generation provider")
			}
			if provider.Kind() == ids.ProcedureObject {
				// Analyzed dependencies retain installed identity even when a
				// branch is pruned. Surviving-tree immutability is checked
				// separately; validate the actual procedure manifest here.
				procedure, ok := provider.(*catalog.Procedure)
				if !ok || ref.Address.SubID != 0 {
					return fmt.Errorf("stuntgres: invalid generation procedure")
				}
				if err := (stored.ProcedureProvider{}).Validate(view, procedure); err != nil {
					return err
				}
			}
			if provider.Kind() == ids.CollationObject {
				if err := stored.ValidateCollationDependency(view, ref, definition.Expression); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// The analyzed tree supplies provider-signature validation. The physical tree
// independently validates its concrete usages and row/owner identities.
func ValidateGenerationPlan(view catalog.Read, d *catalog.Relation, node expr.Expr) error {
	if node == nil {
		return nil
	}
	usage := node.ResultUsage()
	if usage.Type == nil {
		return fmt.Errorf("stuntgres: missing generation plan type")
	}
	if !stored.ValidExpressionUsage(view, usage) {
		return fmt.Errorf("stuntgres: invalid generation plan type")
	}
	if err := stored.ValidateUsageCollation(view, usage); err != nil {
		return err
	}
	child := func(n expr.Expr) error { return ValidateGenerationPlan(view, d, n) }
	pair := func(a, b expr.Expr) error {
		if err := child(a); err != nil {
			return err
		}
		return child(b)
	}
	args := func(nodes []expr.Expr) error {
		for _, n := range nodes {
			if err := child(n); err != nil {
				return err
			}
		}
		return nil
	}
	switch n := node.(type) {
	case plan.RowExpr:
		shape := n.Usage.Type.BaseType()
		if !shape.IsComposite() || n.FieldProjection == nil && len(n.Fields) != shape.CompositeLen() || n.FieldProjection != nil && len(n.FieldProjection) != len(n.Fields) || n.PresenceSlot < 0 || n.WholeNames == nil && (n.PresenceSlot != 0 || n.PresenceFrame != 0) || n.WholeNames != nil && (len(n.WholeNames) != len(n.Fields) || n.PresenceFrame == 0) {
			return fmt.Errorf("stuntgres: invalid composite row plan")
		}
		if n.FieldProjection == nil {
			for i, field := range n.Fields {
				if field == nil || !sqltype.SameRecordUsage(field.ResultUsage(), shape.CompositeMember(i).Usage) {
					return fmt.Errorf("stuntgres: incompatible composite row member")
				}
			}
		} else {
			if n.Raw == nil {
				return fmt.Errorf("stuntgres: retained row projection has no constructor")
			}
			seen := make(map[int32]bool, len(n.FieldProjection))
			live := 0
			for i, projection := range n.FieldProjection {
				if n.Fields[i] == nil || projection.Attribute <= 0 || seen[projection.Attribute] {
					return fmt.Errorf("stuntgres: invalid retained row attribute")
				}
				seen[projection.Attribute] = true
				slot, present := shape.CompositeAttributeIndex(projection.Attribute)
				if !present {
					if projection.Slot != -1 {
						return fmt.Errorf("stuntgres: invalid retired row slot")
					}
					continue
				}
				if slot != projection.Slot || !sqltype.SameRecordUsage(n.Fields[i].ResultUsage(), shape.CompositeMember(slot).Usage) {
					return fmt.Errorf("stuntgres: incompatible retained row member")
				}
				live++
			}
			if live != shape.CompositeLen() {
				return fmt.Errorf("stuntgres: incomplete retained row shape")
			}
		}
		return args(n.Fields)
	case plan.FieldExpr:
		if n.Base == nil || n.Base.ResultUsage().Type == nil || !n.Base.ResultUsage().Type.BaseType().IsComposite() {
			return fmt.Errorf("stuntgres: invalid composite field base")
		}
		shape := n.Base.ResultUsage().Type.BaseType()
		index, ok := shape.CompositeAttributeIndex(n.Attribute)
		source := index
		if row, constructor := n.Base.(plan.RowExpr); constructor {
			source = row.FieldSourceIndex(index)
		}
		if !ok || source != n.Index || !sqltype.SameRecordUsage(n.Usage, shape.CompositeMember(index).Usage) {
			return fmt.Errorf("stuntgres: invalid composite field plan")
		}
		return child(n.Base)
	case plan.ArrayExpr:
		return args(n.Elements)
	case plan.SubscriptExpr:
		var storage [13]expr.Expr
		return args(plan.SubscriptChildren(n, &storage))
	case plan.ColumnExpr:
		if _, generated := d.ColumnMeta.Generations[n.Origin.Address.SubID]; generated || n.Virtual != nil {
			return fmt.Errorf("stuntgres: generated reference in generation plan")
		}
		if !stored.ReferenceEpochValid(view, d, n.Origin) || n.Origin.Address.ClassID != d.Address().ClassID || n.Origin.Address.ObjectID != d.Address().ObjectID || n.Scope != 1 || n.Frame != 1 || n.Slot < 0 || n.Slot >= len(d.Columns) || d.Attributes[n.Slot] != n.Origin.Address.SubID || usage != d.Columns[n.Slot].Usage {
			return fmt.Errorf("stuntgres: invalid generation plan row")
		}
		return nil
	case plan.TableOIDExpr:
		if !stored.ReferenceEpochValid(view, d, n.Owner) || n.Owner.Address != d.Address() || usage.Type.ID() != sqltype.Oid {
			return fmt.Errorf("stuntgres: invalid generation plan owner")
		}
		return nil
	case plan.LiteralExpr:
		if n.Value.Type() != usage.Type {
			return fmt.Errorf("stuntgres: invalid generation plan literal")
		}
		return nil
	case plan.CollationExpr:
		return child(n.Child)
	case plan.CoercionExpr:
		return child(n.Child)
	case plan.SelectedOperatorExpr:
		return child(n.Provider)
	case plan.ArithmeticExpr:
		return pair(n.Left, n.Right)
	case plan.BitwiseExpr:
		return pair(n.Left, n.Right)
	case plan.RowComparisonExpr:
		for _, comparison := range n.Comparisons {
			if err := pair(comparison.Left, comparison.Right); err != nil {
				return err
			}
		}
		return nil
	case plan.ScalarArrayComparisonExpr:
		if err := child(n.Left); err != nil {
			return err
		}
		if n.Array != nil {
			if err := child(n.Array); err != nil {
				return err
			}
		}
		return args(n.Items)
	case plan.ComparisonExpr:
		return pair(n.Left, n.Right)
	case plan.LogicalExpr:
		return pair(n.Left, n.Right)
	case plan.DistinctExpr:
		return pair(n.Left, n.Right)
	case plan.NullTestExpr:
		return child(n.Child)
	case plan.TruthTestExpr:
		return child(n.Child)
	case plan.LikeExpr:
		return pair(n.Left, n.Right)
	case plan.ConditionalExpr:
		return args(n.Args)
	case plan.CaseExpr:
		if err := pair(n.Test, n.Otherwise); err != nil {
			return err
		}
		for _, arm := range n.Arms {
			if err := pair(arm.Condition, arm.Result); err != nil {
				return err
			}
		}
		return nil
	case plan.ExtensionPathsExpr:
		if err := n.Validate(); err != nil {
			return err
		}
		return ValidateGenerationPlan(view, d, n.Arg)
	case plan.InstalledEarthExpr:
		if err := stored.ValidateInstalledEarth(n, view); err != nil {
			return err
		}
		return args(n.Args)
	case plan.InstalledCubeExpr:
		if err := stored.ValidateInstalledCube(n, view); err != nil {
			return err
		}
		return args(n.Args)
	case plan.InstalledSoundexExpr:
		return args(n.Args)
	case plan.InstalledMetaphoneExpr:
		return args(n.Args)
	case plan.InstalledLevenshteinExpr:
		return args(n.Args)
	case plan.InstalledUUIDOsspExpr:
		if err := stored.ValidateInstalledUUIDOssp(n, view); err != nil {
			return err
		}
		return args(n.Args)
	case plan.InstalledCryptoExpr:
		return args(n.Args)
	case plan.InstalledEarthPointExpr:
		if !stored.ReferenceEpochValid(view, d, n.Ref) {
			return fmt.Errorf("stuntgres: invalid generated earth point reference")
		}
		if err := stored.ValidateInstalledEarthPoint(n, view); err != nil {
			return err
		}
		return args(n.Args[:])
	case plan.InstalledCubeSpatialExpr:
		if !stored.ReferenceEpochValid(view, d, n.Ref) {
			return fmt.Errorf("stuntgres: invalid generated cube spatial reference")
		}
		if err := stored.ValidateInstalledCubeSpatial(n, view); err != nil {
			return err
		}
		return args(n.Args[:])
	case plan.InstalledTextExpr:
		return args([]expr.Expr{n.Arg})
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence.
		stored, ok := n.(expr.StoredProvider)
		if !ok {
			return fmt.Errorf("stuntgres: unsupported generation plan provider %T", node)
		}
		if err := stored.ValidateStoredProvider(); err != nil {
			return err
		}
		return args(n.Children())
	default:
		return fmt.Errorf("stuntgres: unsupported generation plan provider %T", node)
	}
}

func GenerationDepends(d *catalog.Relation, target ids.ObjectAddress) bool {
	for _, definition := range d.ColumnMeta.Generations {
		for _, dependency := range definition.Dependencies {
			address := dependency.Address
			if address.ClassID == target.ClassID && address.ObjectID == target.ObjectID && (target.SubID == 0 || address.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}

// GenerationCandidate is old with SET or DROP EXPRESSION of action. The
// relation admission owner has already checked ownership and acquired AX.
// Only detached generation maps enter the shared ALTER candidate pipeline.
func GenerationCandidate(h AlterHost, auth ddl.Authority, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, int32, error) {
	name := action.Column.Text
	if catalog.IsSystemColumn(name) {
		return nil, 0, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, name))
	}
	slot, exists := old.Index[name]
	if !exists {
		return nil, 0, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, old.Name()))
	}
	attribute := old.Attributes[slot]
	definition, generated := old.ColumnMeta.Generations[attribute]
	if !generated {
		if action.Action == parser.DropExpression && action.IfExists {
			h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`column "%s" of relation "%s" is not a generated column, skipping`, name, old.Name())})
			return old, 0, nil
		}
		return nil, 0, sqlError("55000", fmt.Sprintf(`column "%s" of relation "%s" is not a generated column`, name, old.Name()))
	}
	if definition.Kind == catalog.GenerationVirtual {
		message := ""
		if action.Action == parser.DropExpression {
			message = "ALTER TABLE / DROP EXPRESSION is not supported for virtual generated columns"
		} else if len(old.Constraints.Check) != 0 {
			// PostgreSQL18.0 rejects even an unrelated CHECK; newer releases differ.
			message = "ALTER TABLE / SET EXPRESSION is not supported for virtual generated columns in tables with check constraints"
		}
		if message != "" {
			diagnostic := sqlError("0A000", message).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf(`Column "%s" of relation "%s" is a virtual generated column.`, name, old.Name())
			return nil, 0, diagnostic
		}
	}
	next := *old
	next.ColumnMeta.Generations = maps.Clone(old.ColumnMeta.Generations)
	if action.Action == parser.DropExpression {
		delete(next.ColumnMeta.Generations, attribute)
		return &next, 0, nil
	}
	if action.Generation == nil {
		return nil, 0, fmt.Errorf("stuntgres: missing replacement generation expression")
	}
	replacement, err := h.BindGeneration(auth, &next, slot, action.Generation.Expression)
	if err != nil {
		// ALTER utility cooking has no semantic caret, including unsimplified
		// virtual security checks. CREATE and lexer syntax positions are unchanged.
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
		return nil, 0, err
	}
	next.ColumnMeta.Generations[attribute] = replacement
	return &next, attribute, nil
}
