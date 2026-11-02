package analyze

import "stuntgres/internal/parser"

// Explicit REL_18_6 nested syntax selections. Source/capability/diagnostic
// bookkeeping has no released selected field. Keep this closed inventory
// paired with parser/analyze owner coverage, not exported-field reflection.
func (j *StatementJumble) utilitySyntaxFields(node any, anchor *int) bool {
	switch n := node.(type) {
	// These parser leaves deliberately retain deferred rejection, not executable
	// operands. Classify them without hashing diagnostics or inventing children;
	// the existing analyzer remains the error authority and failed frames publish
	// no statistics. Unknown executable families still hit the closed-inventory
	// rejection rather than silently receiving this treatment.
	case *parser.IndexSubqueryExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RejectedIndexSubqueryExpr")
		j.utilityPosition(n.Pos, anchor)
	case parser.IndexSubqueryExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ForbiddenGenerationExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RejectedGenerationExpr")
		j.utilityPosition(n.Pos, anchor)
	case parser.ForbiddenGenerationExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.Identifier:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Identifier")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Text)
	case parser.Identifier:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ObjectName:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ObjectName")
		utilitySlice(j, n.Parts, anchor)
	case parser.ObjectName:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ExplainOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ExplainOption")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.utilitySyntax(n.Value, anchor)
	case parser.ExplainOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.DatabaseOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DatabaseOption")
		j.text(n.Name)
		j.text(n.Value)
		j.flag(n.Default)
		j.flag(n.Numeric)
	case parser.DatabaseOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.VacuumOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("VacuumOption")
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Value, anchor)
		j.flag(n.HasValue)
	case parser.VacuumOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.VacuumTarget:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("VacuumTarget")
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.Only)
		utilitySlice(j, n.Columns, anchor)
	case parser.VacuumTarget:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CopyOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CopyOption")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.number(uint64(n.Argument))
		j.text(n.Value)
		j.flag(n.HasValue)
		j.flag(n.Numeric)
		utilitySlice(j, n.Columns, anchor)
	case parser.CopyOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ExtensionOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ExtensionOption")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.utilitySyntax(&n.Value, anchor)
	case parser.ExtensionOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ExtensionMemberTarget:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ExtensionMemberTarget")
		j.text(n.Kind)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Routine, anchor)
		j.utilitySyntax(&n.Type, anchor)
		j.utilitySyntax(&n.TargetType, anchor)
		j.utilitySyntax(&n.Operator, anchor)
		j.utilitySyntax(&n.Method, anchor)
	case parser.ExtensionMemberTarget:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.EnumLabel:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("EnumLabel")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Text)
	case parser.EnumLabel:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.RangeOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RangeOption")
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Value, anchor)
	case parser.RangeOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TypeModifier:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TypeModifier")
		j.utilityPosition(n.Pos, anchor)
		if n.Integer == nil {
			j.number(0)
		} else {
			j.number(1)
			j.number(uint64(*n.Integer))
		}
		j.text(n.Raw)
		j.flag(n.UnaryPlus)
		j.flag(n.NonSimple)
	case parser.TypeModifier:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.Column:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Column")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.utilitySyntax(&n.TypeName, anchor)
		utilitySlice(j, n.Constraints, anchor)
		j.utilitySyntax(n.Collation, anchor)
	case parser.Column:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.IdentityDefinition:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("IdentityDefinition")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Mode))
		utilitySlice(j, n.Options, anchor)
	case parser.IdentityDefinition:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.GenerationDefinition:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("GenerationDefinition")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Expression, anchor)
		j.number(uint64(n.Mode))
		j.text(n.Kind)
	case parser.GenerationDefinition:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ColumnConstraint:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ColumnConstraint")
		j.utilityPosition(n.Pos, anchor)
		j.flag(n.NotNull)
		j.utilitySyntax(n.Default, anchor)
		j.utilitySyntax(n.Check, anchor)
		j.utilitySyntax(n.Unique, anchor)
		j.utilitySyntax(n.ForeignKey, anchor)
		j.utilitySyntax(n.Identity, anchor)
		j.utilitySyntax(n.Generation, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.NotEnforced)
		j.flag(n.Deferrable)
		j.flag(n.InitiallyDeferred)
	case parser.ColumnConstraint:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.NotNullConstraint:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("NotNullConstraint")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Column, anchor)
		j.utilitySyntax(&n.Name, anchor)
	case parser.NotNullConstraint:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CheckConstraint:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CheckConstraint")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(n.Expression, anchor)
		j.flag(n.NoInherit)
		j.flag(n.NotValid)
		j.flag(n.NotEnforced)
	case parser.CheckConstraint:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.UniqueConstraint:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("UniqueConstraint")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Columns, anchor)
		j.flag(n.Primary)
		j.flag(n.NullsNotDistinct)
		j.flag(n.Deferrable)
		j.flag(n.InitiallyDeferred)
	case parser.UniqueConstraint:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ForeignKeyConstraint:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ForeignKeyConstraint")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Columns, anchor)
		j.utilitySyntax(&n.Target, anchor)
		utilitySlice(j, n.Referenced, anchor)
		j.flag(n.Full)
		j.flag(n.NotValid)
		j.flag(n.NotEnforced)
		j.flag(n.Deferrable)
		j.flag(n.InitiallyDeferred)
		j.number(uint64(n.UpdateAction))
		j.number(uint64(n.DeleteAction))
		utilitySlice(j, n.DeleteColumns, anchor)
	case parser.ForeignKeyConstraint:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.PartitionKey:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("PartitionKey")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Strategy))
		utilitySlice(j, n.Keys, anchor)
	case parser.PartitionKey:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.PartitionDatum:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("PartitionDatum")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Expression, anchor)
		j.number(uint64(n.Kind))
	case parser.PartitionDatum:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.PartitionBound:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("PartitionBound")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Strategy))
		j.number(uint64(n.Modulus))
		j.number(uint64(n.Remainder))
		j.flag(n.Default)
		utilitySlice(j, n.Lower, anchor)
		utilitySlice(j, n.Upper, anchor)
		utilitySlice(j, n.Values, anchor)
	case parser.PartitionBound:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.PartitionParent:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("PartitionParent")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Bound, anchor)
	case parser.PartitionParent:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.IndexKey:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("IndexKey")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Column, anchor)
		j.utilitySyntax(n.Expression, anchor)
		j.utilitySyntax(n.Collation, anchor)
		j.utilitySyntax(n.Opclass, anchor)
		utilitySlice(j, n.Options, anchor)
		j.flag(n.Desc)
		j.flag(n.DirectionExplicit)
		j.number(uint64(n.Nulls))
	case parser.IndexKey:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.IndexOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("IndexOption")
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Namespace)
		j.utilitySyntax(&n.Value, anchor)
		j.flag(n.HasValue)
	case parser.IndexOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ViewOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ViewOption")
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Value)
		j.flag(n.HasValue)
	case parser.ViewOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.SequenceOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SequenceOption")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.number(uint64(n.Value))
		j.utilitySyntax(&n.Type, anchor)
		j.utilitySyntax(&n.Owned, anchor)
	case parser.SequenceOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TableElement:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TableElement")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Column, anchor)
		j.utilitySyntax(n.Like, anchor)
		j.utilitySyntax(n.NotNull, anchor)
		j.utilitySyntax(n.Check, anchor)
		j.utilitySyntax(n.Unique, anchor)
		j.utilitySyntax(n.ForeignKey, anchor)
		j.utilitySyntax(n.Exclusion, anchor)
	case parser.TableElement:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TableLike:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TableLike")
		j.utilitySyntax(&n.Name, anchor)
		j.utilityLikeOptions(n.Options)
	case parser.TableLike:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.LikeOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("LikeOption")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.flag(n.Including)
	case parser.LikeOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.AlterTableAction:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableAction")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Column, anchor)
		j.utilitySyntax(&n.Constraint, anchor)
		j.utilitySyntax(&n.Rule, anchor)
		j.utilitySyntax(&n.Trigger, anchor)
		j.number(uint64(n.TriggerSelector))
		j.utilitySyntax(&n.Tablespace, anchor)
		j.utilitySyntax(&n.Property, anchor)
		j.utilitySyntax(&n.Method, anchor)
		j.utilitySyntax(&n.ClusterIndex, anchor)
		j.flag(n.MethodDefault)
		j.utilitySyntax(&n.Parent, anchor)
		j.number(uint64(n.Action))
		j.number(uint64(n.ReplicaIdentity))
		j.utilitySyntax(&n.ReplicaIndex, anchor)
		j.number(uint64(n.StatisticsTarget))
		j.flag(n.IfExists)
		j.flag(n.IfNotExists)
		j.utilitySyntax(n.Definition, anchor)
		j.number(uint64(n.Mode))
		j.utilitySyntax(n.Identity, anchor)
		j.utilitySyntax(&n.TypeName, anchor)
		j.utilitySyntax(n.Collation, anchor)
		j.utilitySyntax(n.Using, anchor)
		j.utilitySyntax(n.Exclusion, anchor)
		j.utilitySyntax(n.UsingIndex, anchor)
		j.utilitySyntax(n.Generation, anchor)
		j.utilitySyntax(n.Check, anchor)
		j.utilitySyntax(n.Unique, anchor)
		j.utilitySyntax(n.ForeignKey, anchor)
		utilitySlice(j, n.Options, anchor)
		j.flag(n.NotValid)
		j.flag(n.NotEnforced)
		j.flag(n.NoInherit)
		j.flag(n.Deferrable)
		j.flag(n.InitiallyDeferred)
		j.flag(n.InheritabilitySet)
		j.flag(n.DeferrabilitySet)
		j.flag(n.InitiallySet)
		j.flag(n.EnforcementSet)
	case parser.AlterTableAction:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.UsingIndexConstraint:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("UsingIndexConstraint")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Index, anchor)
		j.flag(n.Primary)
		j.flag(n.Deferrable)
		j.flag(n.InitiallyDeferred)
	case parser.UsingIndexConstraint:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ExclusionConstraint:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ExclusionConstraint")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Index, anchor)
		utilitySlice(j, n.Operators, anchor)
		j.flag(n.Deferrable)
		j.flag(n.InitiallyDeferred)
	case parser.ExclusionConstraint:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.InsertConflict:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("InsertConflict")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Keys, anchor)
		j.utilitySyntax(n.Predicate, anchor)
		j.utilitySyntax(&n.Constraint, anchor)
		j.flag(n.Update)
		utilitySlice(j, n.Assignments, anchor)
		j.utilitySyntax(n.Where, anchor)
	case parser.InsertConflict:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.InsertTarget:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("InsertTarget")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		utilitySlice(j, n.Steps, anchor)
	case parser.InsertTarget:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TargetStep:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TargetStep")
		j.utilitySyntax(n.Field, anchor)
		j.utilitySyntax(n.Subscript, anchor)
	case parser.TargetStep:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.MergeArm:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("MergeArm")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Match))
		j.number(uint64(n.Action))
		utilitySlice(j, n.Assignments, anchor)
		j.utilitySyntax(n.Condition, anchor)
		utilitySlice(j, n.Targets, anchor)
		utilitySlice(j, n.Values, anchor)
		j.flag(n.DefaultValues)
		j.number(uint64(n.Override))
	case parser.MergeArm:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.InsertSource:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("InsertSource")
		j.utilitySyntax(n.Select, anchor)
		j.utilitySyntax(n.Values, anchor)
	case parser.InsertSource:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.Returning:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Returning")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Targets, anchor)
		utilitySlice(j, n.Labels, anchor)
		utilitySlice(j, n.Aliases, anchor)
	case parser.Returning:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ReturningAlias:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ReturningAlias")
		j.utilityPosition(n.Pos, anchor)
		j.flag(n.New)
		j.utilitySyntax(&n.Alias, anchor)
	case parser.ReturningAlias:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.UpdateAssignment:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("UpdateAssignment")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Target, anchor)
		utilitySlice(j, n.Steps, anchor)
		utilitySlice(j, n.Targets, anchor)
		j.utilitySyntax(n.Value, anchor)
	case parser.UpdateAssignment:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CommonTableExpression:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CommonTableExpression")
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Columns, anchor)
		j.utilitySyntax(n.Query, anchor)
		j.number(uint64(n.Materialization))
		j.utilitySyntax(n.Search, anchor)
		j.utilitySyntax(n.Cycle, anchor)
	case parser.CommonTableExpression:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.SearchClause:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SearchClause")
		j.utilityPosition(n.Pos, anchor)
		j.flag(n.Breadth)
		utilitySlice(j, n.Keys, anchor)
		j.utilitySyntax(&n.Sequence, anchor)
	case parser.SearchClause:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CycleClause:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CycleClause")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Keys, anchor)
		j.utilitySyntax(&n.Mark, anchor)
		j.utilitySyntax(&n.Path, anchor)
		j.utilitySyntax(n.Value, anchor)
		j.utilitySyntax(n.Default, anchor)
	case parser.CycleClause:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.GroupClause:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("GroupClause")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Items, anchor)
		j.flag(n.Distinct)
	case parser.GroupClause:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.GroupItem:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("GroupItem")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Kind))
		j.utilitySyntax(n.Expr, anchor)
		utilitySlice(j, n.Items, anchor)
	case parser.GroupItem:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.RowLockClause:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RowLockClause")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Mode)
		utilitySlice(j, n.Targets, anchor)
		j.number(uint64(n.Wait))
	case parser.RowLockClause:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.DistinctClause:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DistinctClause")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.On, anchor)
	case parser.DistinctClause:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.SetQuery:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SetQuery")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Left, anchor)
		j.utilitySyntax(n.Right, anchor)
		j.text(n.Operator)
		j.flag(n.All)
	case parser.SetQuery:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.QueryBounds:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("QueryBounds")
		j.utilitySyntax(n.Offset, anchor)
		j.utilitySyntax(n.Count, anchor)
		j.flag(n.WithTies)
	case parser.QueryBounds:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.WindowSpec:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("WindowSpec")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Name, anchor)
		utilitySlice(j, n.Partition, anchor)
		utilitySlice(j, n.Order, anchor)
		j.utilitySyntax(n.Frame, anchor)
	case parser.WindowSpec:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.WindowDefinition:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("WindowDefinition")
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Spec, anchor)
	case parser.WindowDefinition:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.WindowFrame:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("WindowFrame")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Unit))
		j.number(uint64(n.Exclusion))
		j.utilitySyntax(&n.Start, anchor)
		j.utilitySyntax(&n.End, anchor)
	case parser.WindowFrame:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.FrameBound:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("FrameBound")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Kind))
		j.utilitySyntax(n.Offset, anchor)
	case parser.FrameBound:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.LockTarget:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("LockTarget")
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.Only)
	case parser.LockTarget:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.Order:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Order")
		utilitySlice(j, n.Keys, anchor)
	case parser.Order:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.OrderKey:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("OrderKey")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Expr, anchor)
		j.flag(n.Desc)
		j.number(uint64(n.Nulls))
	case parser.OrderKey:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TableReference:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TableReference")
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.Only)
	case parser.TableReference:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.FunctionFrom:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("FunctionFrom")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Calls, anchor)
		j.flag(n.Ordinality)
		j.flag(n.Lateral)
		j.flag(n.RowsFrom)
	case parser.FunctionFrom:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.FunctionTable:
		if n == nil {
			j.number(0)
			return true
		}
		// REL_18_6 RangeFunction selects functions (including each member's
		// coldefs), lateral, ordinality and is_rowsfrom. Keep the written raw
		// calls: FROM-only UNNEST rewriting belongs to analyzed query binding.
		j.text("FunctionTable")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Calls, anchor)
		j.flag(n.Ordinality)
		j.flag(n.Lateral)
		j.flag(n.RowsFrom)
	case parser.FunctionTable:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ValuesQuery:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ValuesQuery")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Rows, anchor)
		j.utilitySyntax(n.Order, anchor)
		j.utilitySyntax(n.Bounds, anchor)
	case parser.ValuesQuery:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.JoinReference:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("JoinReference")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Left, anchor)
		j.utilitySyntax(n.Right, anchor)
		j.number(uint64(n.Kind))
		j.utilitySyntax(n.On, anchor)
		utilitySlice(j, n.Using, anchor)
		j.flag(n.Natural)
		j.utilitySyntax(n.UsingAlias, anchor)
		j.flag(n.Comma)
	case parser.JoinReference:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.FunctionColumnDefinition:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("FunctionColumnDefinition")
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Type, anchor)
		j.utilitySyntax(n.Collation, anchor)
	case parser.FunctionColumnDefinition:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.AliasReference:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AliasReference")
		j.utilitySyntax(n.Input, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Columns, anchor)
		utilitySlice(j, n.ColumnDefinitions, anchor)
	case parser.AliasReference:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.DerivedQuery:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DerivedQuery")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Query, anchor)
	case parser.DerivedQuery:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CallExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CallExpr")
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Args, anchor)
	case parser.CallExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.RegclassExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RegclassExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Child, anchor)
	case parser.RegclassExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ArrayExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ArrayExpr")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Elements, anchor)
	case parser.ArrayExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ArraySubscript:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ArraySubscript")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Lower, anchor)
		j.utilitySyntax(n.Upper, anchor)
		j.flag(n.Slice)
	case parser.ArraySubscript:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.SubscriptExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SubscriptExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Base, anchor)
		utilitySlice(j, n.Subscripts, anchor)
	case parser.SubscriptExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TimeZoneExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TimeZoneExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Value, anchor)
		j.utilitySyntax(n.Zone, anchor)
		j.flag(n.Local)
	case parser.TimeZoneExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CollateExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CollateExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Child, anchor)
		j.utilitySyntax(&n.Name, anchor)
	case parser.CollateExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.LiteralExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("LiteralExpr")
		j.utilitySyntax(&n.Value, anchor)
	case parser.LiteralExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TypedLiteralExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TypedLiteralExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.TypeName, anchor)
		j.text(n.Text)
	case parser.TypedLiteralExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ColumnExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ColumnExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
	case parser.ColumnExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ParameterExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ParameterExpr")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Number))
	case parser.ParameterExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.DefaultExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DefaultExpr")
		j.utilityPosition(n.Pos, anchor)
	case parser.DefaultExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.StarExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("StarExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Qualifier, anchor)
	case parser.StarExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.OperatorExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("OperatorExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Args, anchor)
		j.flag(n.SQLSimilar)
	case parser.OperatorExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.InListExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("InListExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Left, anchor)
		utilitySlice(j, n.Items, anchor)
		j.flag(n.Negate)
	case parser.InListExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ArrayComparisonExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ArrayComparisonExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Left, anchor)
		j.utilitySyntax(n.Right, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.All)
	case parser.ArrayComparisonExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.LogicalExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("LogicalExpr")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Op))
		j.utilitySyntax(n.Left, anchor)
		j.utilitySyntax(n.Right, anchor)
	case parser.LogicalExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.PredicateExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("PredicateExpr")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Kind))
		j.utilitySyntax(n.Left, anchor)
		j.utilitySyntax(n.Right, anchor)
	case parser.PredicateExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.IdentityExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("IdentityExpr")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
	case parser.IdentityExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CurrentDateTimeExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CurrentDateTimeExpr")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Kind))
		j.number(uint64(n.Precision))
		j.flag(n.HasPrecision)
	case parser.CurrentDateTimeExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.FunctionExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("FunctionExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Args, anchor)
		utilitySlice(j, n.ArgNames, anchor)
		j.flag(n.Variadic)
		j.flag(n.Syntax == parser.SQLFunctionSyntax || n.StringSyntax != parser.OrdinaryStringSyntax)
		j.utilitySyntax(n.Window, anchor)
		j.flag(n.Star)
		j.flag(n.Distinct)
		utilitySlice(j, n.Order, anchor)
		j.utilitySyntax(n.Filter, anchor)
		j.flag(n.Within)
		utilitySlice(j, n.ColumnDefinitions, anchor)
	case parser.FunctionExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.AggregateExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AggregateExpr")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.flag(n.Star)
		utilitySlice(j, n.Args, anchor)
		utilitySlice(j, n.Order, anchor)
		j.flag(n.Distinct)
		j.flag(n.Variadic)
		j.flag(n.Within)
		j.utilitySyntax(n.Filter, anchor)
		j.utilitySyntax(n.Schema, anchor)
		j.utilitySyntax(n.Window, anchor)
		j.utilitySyntax(n.JSON, anchor)
	case parser.AggregateExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CastExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CastExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Child, anchor)
		j.utilitySyntax(&n.TypeName, anchor)
	case parser.CastExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.RowExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RowExpr")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Fields, anchor)
		j.flag(n.Explicit)
	case parser.RowExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.FieldExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("FieldExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Base, anchor)
		j.utilitySyntax(&n.Field, anchor)
		j.flag(n.Expansion)
	case parser.FieldExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.SubqueryExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SubqueryExpr")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Kind))
		j.utilitySyntax(n.Query, anchor)
		j.utilitySyntax(n.Left, anchor)
		j.utilitySyntax(&n.Operator, anchor)
		j.flag(n.Negate)
	case parser.SubqueryExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.GroupingExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("GroupingExpr")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Args, anchor)
	case parser.GroupingExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CaseExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CaseExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Test, anchor)
		utilitySlice(j, n.Arms, anchor)
		j.utilitySyntax(n.Else, anchor)
	case parser.CaseExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CaseArm:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CaseArm")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Condition, anchor)
		j.utilitySyntax(n.Result, anchor)
	case parser.CaseArm:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ConditionalExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ConditionalExpr")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Kind))
		utilitySlice(j, n.Args, anchor)
	case parser.ConditionalExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.MergeActionExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("MergeActionExpr")
		j.utilityPosition(n.Pos, anchor)
	case parser.MergeActionExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.JSONFormat:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("JSONFormat")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Encoding)
	case parser.JSONFormat:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.JSONAggregateOptions:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("JSONAggregateOptions")
		j.utilitySyntax(n.Input, anchor)
		j.flag(n.Absent)
		j.flag(n.Unique)
		j.utilitySyntax(n.Returning, anchor)
		j.utilitySyntax(n.Output, anchor)
	case parser.JSONAggregateOptions:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.SQLJSONExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SQLJSONExpr")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		utilitySlice(j, n.Args, anchor)
		utilitySlice(j, n.Formats, anchor)
		j.utilitySyntax(n.Query, anchor)
		j.utilitySyntax(n.Returning, anchor)
		j.utilitySyntax(n.Output, anchor)
		j.flag(n.Absent)
		j.flag(n.Unique)
	case parser.SQLJSONExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.SQLJSONQueryExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SQLJSONQueryExpr")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		j.utilitySyntax(n.Document, anchor)
		j.utilitySyntax(n.Path, anchor)
		j.utilitySyntax(n.Input, anchor)
		j.utilitySyntax(n.Output, anchor)
		utilitySlice(j, n.Passing, anchor)
		j.utilitySyntax(n.Returning, anchor)
		j.text(n.Wrapper)
		j.text(n.Quotes)
		j.utilitySyntax(&n.Empty, anchor)
		j.utilitySyntax(&n.Error, anchor)
	case parser.SQLJSONQueryExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.SQLJSONPassing:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SQLJSONPassing")
		j.utilitySyntax(n.Value, anchor)
		j.utilitySyntax(&n.Name, anchor)
	case parser.SQLJSONPassing:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.SQLJSONBehavior:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SQLJSONBehavior")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		j.utilitySyntax(n.Default, anchor)
	case parser.SQLJSONBehavior:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.JSONPredicateExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("JSONPredicateExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Child, anchor)
		j.text(n.Kind)
		j.flag(n.Unique)
		j.flag(n.Not)
	case parser.JSONPredicateExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.JSONTable:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("JSONTable")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Document, anchor)
		j.utilitySyntax(n.Path, anchor)
		j.utilitySyntax(n.Input, anchor)
		j.utilitySyntax(n.PathName, anchor)
		utilitySlice(j, n.Passing, anchor)
		utilitySlice(j, n.Columns, anchor)
		j.utilitySyntax(&n.Error, anchor)
		j.flag(n.Lateral)
	case parser.JSONTable:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.JSONTableColumn:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("JSONTableColumn")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Type, anchor)
		j.flag(n.Ordinality)
		j.flag(n.Exists)
		j.utilitySyntax(n.Format, anchor)
		j.utilitySyntax(n.Path, anchor)
		j.utilitySyntax(n.PathName, anchor)
		j.text(n.Wrapper)
		j.text(n.Quotes)
		j.utilitySyntax(&n.Empty, anchor)
		j.utilitySyntax(&n.Error, anchor)
		utilitySlice(j, n.Columns, anchor)
	case parser.JSONTableColumn:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.XMLParseExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("XMLParseExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Child, anchor)
		j.flag(n.Whitespace == "preserve")
	case parser.XMLParseExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.XMLSerializeExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("XMLSerializeExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Child, anchor)
		j.flag(n.Document)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.Indent == "indent")
	case parser.XMLSerializeExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.XMLDocumentPredicateExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("XMLDocumentPredicateExpr")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Child, anchor)
		j.flag(n.Negated)
	case parser.XMLDocumentPredicateExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.XMLConstructExpr:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("XMLConstructExpr")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Kind))
		utilitySlice(j, n.Named, anchor)
		utilitySlice(j, n.Args, anchor)
		if n.Kind == parser.XMLRoot && n.Version == nil {
			j.text("LiteralExpr")
			j.text("A_Const")
			j.number(uint64(parser.NullValue))
		} else {
			j.utilitySyntax(n.Version, anchor)
		}
		j.number(uint64(n.Standalone))
	case parser.XMLConstructExpr:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.XMLNamedOperand:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("XMLNamedOperand")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Expr, anchor)
	case parser.XMLNamedOperand:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.XMLTable:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("XMLTable")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Namespaces, anchor)
		j.utilitySyntax(n.Row, anchor)
		j.utilitySyntax(n.Document, anchor)
		utilitySlice(j, n.Columns, anchor)
		j.flag(n.Lateral)
	case parser.XMLTable:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.XMLNamespace:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("XMLNamespace")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.URI, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.Default)
	case parser.XMLNamespace:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.XMLTableOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("XMLTableOption")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		j.utilitySyntax(n.Value, anchor)
	case parser.XMLTableOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.XMLTableColumn:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("XMLTableColumn")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Type, anchor)
		j.flag(n.Ordinality)
		utilitySlice(j, n.Options, anchor)
	case parser.XMLTableColumn:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.RoleSpec:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RoleSpec")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.text(n.Identity)
	case parser.RoleSpec:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.RoleOptions:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RoleOptions")
		j.utilityRoleOptions(n, anchor)
	case parser.RoleOptions:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.InitialRoleMembership:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("InitialRoleMembership")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		utilitySlice(j, n.Members, anchor)
	case parser.InitialRoleMembership:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.MembershipOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("MembershipOption")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.flag(n.Value)
	case parser.MembershipOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.GrantPrivilege:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("GrantPrivilege")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		utilitySlice(j, n.Columns, anchor)
		j.flag(n.All)
		j.flag(n.RoleName)
	case parser.GrantPrivilege:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.GrantGrantee:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("GrantGrantee")
		j.utilitySyntax(&n.Role, anchor)
		j.flag(n.Public)
	case parser.GrantGrantee:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.GrantRoutineArgument:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("GrantRoutineArgument")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Mode)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Type, anchor)
	case parser.GrantRoutineArgument:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.GrantRoutineTarget:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("GrantRoutineTarget")
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Arguments, anchor)
		j.flag(n.SignatureSpecified)
	case parser.GrantRoutineTarget:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.DefaultPrivilegesOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DefaultPrivilegesOption")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Roles, anchor)
		utilitySlice(j, n.Schemas, anchor)
	case parser.DefaultPrivilegesOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.RoutineParameter:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RoutineParameter")
		j.utilitySyntax(&n.GrantRoutineArgument, anchor)
		j.utilitySyntax(n.Default, anchor)
	case parser.RoutineParameter:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.RoutineAttribute:
		if n == nil {
			j.number(0)
			return true
		}
		j.utilityRoutineAttribute(n, anchor)
	case parser.RoutineAttribute:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.RoutineTransformClause:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RoutineTransformClause")
		utilitySlice(j, n.Types, anchor)
	case parser.RoutineTransformClause:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ForeignOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ForeignOption")
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Value)
		j.text(n.Action)
	case parser.ForeignOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ForeignSupportOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ForeignSupportOption")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		j.utilitySyntax(n.Function, anchor)
	case parser.ForeignSupportOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ForeignTableAction:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ForeignTableAction")
		j.utilitySyntax(n.Ordinary, anchor)
		j.utilitySyntax(&n.Column, anchor)
		utilitySlice(j, n.Options, anchor)
		utilitySlice(j, n.ColumnOptions, anchor)
		j.utilitySyntax(n.Default, anchor)
	case parser.ForeignTableAction:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.OperatorSignature:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("OperatorSignature")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Left, anchor)
		j.utilitySyntax(&n.Right, anchor)
	case parser.OperatorSignature:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.OperatorOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("OperatorOption")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.flag(n.HasValue)
		j.number(uint64(n.Kind))
		j.utilitySyntax(&n.Type, anchor)
		j.utilitySyntax(&n.Reference, anchor)
		j.text(n.Value)
	case parser.OperatorOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.OperatorFamilyItem:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("OperatorFamilyItem")
		j.utilityPosition(n.Pos, anchor)
		j.flag(n.Function)
		j.utilitySyntax(n.Storage, anchor)
		j.text(n.Number)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Arguments, anchor)
		utilitySlice(j, n.Association, anchor)
		j.flag(n.Signature)
		j.text(n.Purpose)
		j.utilitySyntax(&n.SortFamily, anchor)
	case parser.OperatorFamilyItem:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.CollationOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CollationOption")
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Value)
		j.utilitySyntax(n.Source, anchor)
		j.flag(n.Numeric)
	case parser.CollationOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.DictionaryOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DictionaryOption")
		j.utilitySyntax(&n.CollationOption, anchor)
		j.flag(n.Supplied)
	case parser.DictionaryOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.ConfigurationOption:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ConfigurationOption")
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Value, anchor)
		j.flag(n.Supplied)
		j.flag(n.Numeric)
	case parser.ConfigurationOption:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.EventTriggerFilter:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("EventTriggerFilter")
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Values, anchor)
	case parser.EventTriggerFilter:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.PublicationMember:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("PublicationMember")
		j.text(n.Kind)
		j.utilitySyntax(&n.Table, anchor)
		j.utilitySyntax(&n.Schema, anchor)
		j.flag(n.CurrentSchema)
		j.flag(n.Only)
		utilitySlice(j, n.Columns, anchor)
		j.utilitySyntax(n.Filter, anchor)
	case parser.PublicationMember:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TriggerEvent:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TriggerEvent")
		j.text(n.Name)
		utilitySlice(j, n.Columns, anchor)
	case parser.TriggerEvent:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TriggerTransition:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TriggerTransition")
		j.utilityPosition(n.Pos, anchor)
		j.flag(n.New)
		j.flag(n.Row)
		j.utilitySyntax(&n.Alias, anchor)
	case parser.TriggerTransition:
		return j.utilitySyntaxFields(&n, anchor)
	case *parser.TransactionMode:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("TransactionMode")
		j.number(uint64(n.Isolation))
		j.flag(n.AccessSet)
		j.flag(n.ReadOnly)
		j.flag(n.DeferrableSet)
		j.flag(n.Deferrable)
	case parser.TransactionMode:
		return j.utilitySyntaxFields(&n, anchor)
	default:
		return false
	}
	return true
}
