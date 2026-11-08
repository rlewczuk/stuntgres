package analyze

import "stuntgres/internal/parser"

// Explicit REL_18_6 raw utility selections. Fields are read statically; new
// statement types cannot inherit a reflection/hash-all fallback.
func (j *StatementJumble) utilityStatementFields(node any, anchor *int) bool {
	switch n := node.(type) {
	case *parser.Explain:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Explain")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(n.Statement, anchor)
		utilitySlice(j, n.Options, anchor)
	case parser.Explain:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateDatabaseStmt:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateDatabaseStmt")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		utilitySlice(j, n.Options, anchor)
	case parser.CreateDatabaseStmt:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropDatabaseStmt:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropdbStmt")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.flag(n.IfExists)
		utilitySlice(j, n.Options, anchor)
	case parser.DropDatabaseStmt:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterDatabaseStmt:
		if n == nil {
			j.number(0)
			return true
		}
		j.utilityPosition(n.Pos, anchor)
		switch n.Action {
		case "rename":
			j.text("AlterDatabaseRename")
			j.text(n.Name)
			j.text(n.NewName)
		case "refresh":
			j.text("AlterDatabaseRefreshCollStmt")
			j.text(n.Name)
		case "", "options":
			j.text("AlterDatabaseStmt")
			j.text(n.Name)
			utilitySlice(j, n.Options, anchor)
		case "tablespace":
			// REL_18_6 gram.y builds the same one-DefElem list as the
			// general option spelling. Project it without a temporary list.
			j.text("AlterDatabaseStmt")
			j.text(n.Name)
			j.number(1)
			j.utilityDatabaseOption("tablespace", n.Tablespace, false, false)
		default:
			panic("stuntgres: unclassified ALTER DATABASE action")
		}
	case parser.AlterDatabaseStmt:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Vacuum:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Vacuum")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Options, anchor)
		utilitySlice(j, n.Targets, anchor)
	case parser.Vacuum:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Analyze:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Analyze")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Options, anchor)
		utilitySlice(j, n.Targets, anchor)
	case parser.Analyze:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Load:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Load")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Filename)
	case parser.Load:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Checkpoint:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Checkpoint")
		j.utilityPosition(n.Pos, anchor)
	case parser.Checkpoint:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterSystem:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterSystem")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.Default)
	case parser.AlterSystem:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateTablespace:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateTablespace")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(n.Owner, anchor)
		j.text(n.Location)
		utilitySlice(j, n.Options, anchor)
	case parser.CreateTablespace:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTablespaceRename:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTablespaceRename")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.NewName, anchor)
	case parser.AlterTablespaceRename:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropTablespace:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropTablespace")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.IfExists)
	case parser.DropTablespace:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTrigger:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTrigger")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
	case parser.AlterTrigger:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropTrigger:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropTrigger")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropTrigger:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateRule:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateRule")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.text(n.Event)
		j.utilitySyntax(n.Condition, anchor)
		utilitySlice(j, n.Actions, anchor)
		j.flag(n.Replace)
		j.flag(n.Instead)
	case parser.CreateRule:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterRule:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterRule")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.utilitySyntax(&n.Rename, anchor)
	case parser.AlterRule:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropRule:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropRule")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropRule:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Notify:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Notify")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Channel, anchor)
		j.text(n.Payload)
	case parser.Notify:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Listen:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Listen")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Channel, anchor)
	case parser.Listen:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Unlisten:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Unlisten")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Channel, anchor)
		j.flag(n.All)
	case parser.Unlisten:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreatePolicy:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreatePolicy")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.flag(n.Permissive)
		j.text(n.Command)
		utilitySlice(j, n.Roles, anchor)
		j.utilitySyntax(n.Using, anchor)
		j.utilitySyntax(n.Check, anchor)
	case parser.CreatePolicy:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterPolicy:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterPolicy")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		utilitySlice(j, n.Roles, anchor)
		j.utilitySyntax(n.Using, anchor)
		j.utilitySyntax(n.Check, anchor)
		j.utilitySyntax(n.Rename, anchor)
		j.flag(n.IfExists)
	case parser.AlterPolicy:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropPolicy:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropPolicy")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropPolicy:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Copy:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Copy")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(n.Query, anchor)
		utilitySlice(j, n.Columns, anchor)
		j.flag(n.From)
		j.number(uint64(n.Endpoint))
		j.text(n.Filename)
		utilitySlice(j, n.Options, anchor)
		j.utilitySyntax(n.Where, anchor)
	case parser.Copy:
		return j.utilityStatementFields(&n, anchor)
	case *parser.PrepareSQL:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("PrepareSQL")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Types, anchor)
		j.utilitySyntax(n.Body, anchor)
	case parser.PrepareSQL:
		return j.utilityStatementFields(&n, anchor)
	case *parser.ExecuteSQL:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ExecuteSQL")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Arguments, anchor)
	case parser.ExecuteSQL:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Discard:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Discard")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Category))
	case parser.Discard:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateExtension:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateExtension")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.IfNotExists)
		utilitySlice(j, n.Options, anchor)
	case parser.CreateExtension:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropExtension:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropExtension")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropExtension:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterExtensionUpdate:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterExtensionUpdate")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
	case parser.AlterExtensionUpdate:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterExtension:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterExtension")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Schema, anchor)
		j.utilitySyntax(&n.Member, anchor)
	case parser.AlterExtension:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateEnum:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateEnum")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Labels, anchor)
	case parser.CreateEnum:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateRange:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateRange")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
	case parser.CreateRange:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterEnum:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterEnum")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Label, anchor)
		j.utilitySyntax(&n.Old, anchor)
		j.utilitySyntax(&n.Neighbor, anchor)
		j.flag(n.Rename)
		j.flag(n.IfNotExists)
		j.flag(n.Before)
	case parser.AlterEnum:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterType:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterType")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.SetSchema)
	case parser.AlterType:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateComposite:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateComposite")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Members, anchor)
	case parser.CreateComposite:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterComposite:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterComposite")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Actions, anchor)
		j.utilitySyntax(n.Rename, anchor)
		j.utilitySyntax(&n.Column, anchor)
		j.number(uint64(n.Mode))
	case parser.AlterComposite:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropType:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropType")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropType:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTableOf:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableOf")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(n.Type, anchor)
		j.flag(n.Only)
	case parser.AlterTableOf:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateDomain:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateDomain")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Base, anchor)
		j.utilitySyntax(n.Collation, anchor)
		utilitySlice(j, n.Constraints, anchor)
	case parser.CreateDomain:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropDomain:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropDomain")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropDomain:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterDomain:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterDomain")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.number(uint64(n.Action))
		j.utilitySyntax(n.Check, anchor)
		j.utilitySyntax(n.Default, anchor)
		j.utilitySyntax(&n.Constraint, anchor)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.AlterDomain:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateTable:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateTable")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Elements, anchor)
		j.utilitySyntax(n.OfType, anchor)
		utilitySlice(j, n.Parents, anchor)
		j.flag(n.IfNotExists)
		j.text(n.Persistence)
		j.number(uint64(n.OnCommit))
		j.utilitySyntax(n.PartitionBy, anchor)
		j.utilitySyntax(n.PartitionOf, anchor)
	case parser.CreateTable:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTableAttachPartition:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableAttachPartition")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Partition, anchor)
		j.utilitySyntax(&n.Bound, anchor)
		j.flag(n.IfExists)
		j.flag(n.Only)
	case parser.AlterTableAttachPartition:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTableDetachPartition:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableDetachPartition")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Partition, anchor)
		j.flag(n.IfExists)
		j.flag(n.Only)
		j.number(uint64(n.Mode))
	case parser.AlterTableDetachPartition:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateQueryTable:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateQueryTable")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Columns, anchor)
		j.utilitySyntax(n.Query, anchor)
		j.number(uint64(n.Form))
		j.flag(n.IfNotExists)
		j.flag(n.NoData)
	case parser.CreateQueryTable:
		return j.utilityStatementFields(&n, anchor)
	case *parser.RefreshMaterializedView:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RefreshMaterializedView")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.NoData)
		j.flag(n.Concurrent.Character > 0)
	case parser.RefreshMaterializedView:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateIndex:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateIndex")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.flag(n.Only)
		j.text(n.Method)
		utilitySlice(j, n.Keys, anchor)
		utilitySlice(j, n.Include, anchor)
		j.utilitySyntax(n.Predicate, anchor)
		j.flag(n.IfNotExists)
		j.flag(n.Unique)
		j.flag(n.NullsNotDistinct)
		utilitySlice(j, n.Options, anchor)
		j.flag(n.Concurrent.Character > 0)
	case parser.CreateIndex:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateView:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateView")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Columns, anchor)
		utilitySlice(j, n.Options, anchor)
		j.utilitySyntax(n.Query, anchor)
		j.flag(n.Replace)
		j.text(n.Persistence)
	case parser.CreateView:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterView:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterView")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Column, anchor)
		j.utilitySyntax(&n.Target, anchor)
		utilitySlice(j, n.Options, anchor)
		j.flag(n.OwnerIdentity)
		j.flag(n.IfExists)
		j.flag(n.Materialized)
	case parser.AlterView:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropView:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropView")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
		j.flag(n.Materialized)
	case parser.DropView:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateSequence:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateSequence")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
		j.text(n.Persistence)
		j.flag(n.IfNotExists)
	case parser.CreateSequence:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterSequence:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterSequence")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
		j.flag(n.IfExists)
	case parser.AlterSequence:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropSequence:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropSequence")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropSequence:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropTable:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropTable")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropTable:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTableRenameColumn:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableRenameColumn")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Column, anchor)
		j.utilitySyntax(&n.NewName, anchor)
		j.flag(n.IfExists)
		j.flag(n.Only)
	case parser.AlterTableRenameColumn:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTableRenameTable:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableRenameTable")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.NewName, anchor)
		j.flag(n.IfExists)
	case parser.AlterTableRenameTable:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTableSetSchema:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableSetSchema")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Schema, anchor)
		j.flag(n.IfExists)
		j.flag(n.Only)
	case parser.AlterTableSetSchema:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTable:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTable")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Actions, anchor)
		j.flag(n.Only)
		j.flag(n.IfExists)
		j.flag(n.Materialized)
	case parser.AlterTable:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTableAllInTablespace:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableAllInTablespace")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Source, anchor)
		j.utilitySyntax(&n.Destination, anchor)
		utilitySlice(j, n.Owners, anchor)
		j.flag(n.Nowait)
		j.flag(n.Materialized)
	case parser.AlterTableAllInTablespace:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTableDefault:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableDefault")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Column, anchor)
		j.flag(n.Only)
		j.flag(n.IfExists)
		j.number(uint64(n.Action))
		j.utilitySyntax(n.Expression, anchor)
	case parser.AlterTableDefault:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Insert:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Insert")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.With, anchor)
		j.flag(n.WithRecursive)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Alias, anchor)
		utilitySlice(j, n.Targets, anchor)
		utilitySlice(j, n.Rows, anchor)
		j.utilitySyntax(n.Source, anchor)
		j.flag(n.DefaultValues)
		j.number(uint64(n.Override))
		j.utilitySyntax(n.Conflict, anchor)
		j.utilitySyntax(n.Returning, anchor)
	case parser.Insert:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Merge:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Merge")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.With, anchor)
		j.flag(n.WithRecursive)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Alias, anchor)
		j.utilitySyntax(n.Source, anchor)
		j.utilitySyntax(n.On, anchor)
		utilitySlice(j, n.Arms, anchor)
		j.utilitySyntax(n.Returning, anchor)
	case parser.Merge:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Delete:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Delete")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.With, anchor)
		j.flag(n.WithRecursive)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.Only)
		j.utilitySyntax(&n.Alias, anchor)
		j.utilitySyntax(n.Using, anchor)
		j.utilitySyntax(n.Where, anchor)
		j.utilitySyntax(n.CurrentOf, anchor)
		j.utilitySyntax(n.Returning, anchor)
	case parser.Delete:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Update:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Update")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.With, anchor)
		j.flag(n.WithRecursive)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.Only)
		j.utilitySyntax(&n.Alias, anchor)
		j.utilitySyntax(n.From, anchor)
		utilitySlice(j, n.Assignments, anchor)
		j.utilitySyntax(n.Where, anchor)
		j.utilitySyntax(n.CurrentOf, anchor)
		j.utilitySyntax(n.Returning, anchor)
	case parser.Update:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Select:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Select")
		j.utilityPosition(n.Pos, anchor)
		j.flag(n.RoutineReturn)
		utilitySlice(j, n.With, anchor)
		j.flag(n.WithRecursive)
		j.utilitySyntax(n.From, anchor)
		j.utilitySyntax(n.Values, anchor)
		utilitySlice(j, n.Targets, anchor)
		utilitySlice(j, n.Labels, anchor)
		j.utilitySyntax(n.Where, anchor)
		j.utilitySyntax(n.GroupBy, anchor)
		j.utilitySyntax(n.Having, anchor)
		utilitySlice(j, n.Windows, anchor)
		j.utilitySyntax(n.Order, anchor)
		j.utilitySyntax(n.Into, anchor)
		utilitySlice(j, n.Locking, anchor)
		j.utilitySyntax(n.Bounds, anchor)
		j.utilitySyntax(n.Distinct, anchor)
		j.utilitySyntax(n.Set, anchor)
	case parser.Select:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DeclareCursor:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DeclareCursor")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(n.Query, anchor)
		j.flag(n.Hold)
		j.number(uint64(n.Scroll))
	case parser.DeclareCursor:
		return j.utilityStatementFields(&n, anchor)
	case *parser.FetchCursor:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("FetchCursor")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.number(uint64(n.Count))
		j.flag(n.All)
		j.number(uint64(n.Direction))
		j.flag(n.Move)
	case parser.FetchCursor:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CloseCursor:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CloseCursor")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.All)
	case parser.CloseCursor:
		return j.utilityStatementFields(&n, anchor)
	case *parser.LockTable:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("LockTable")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Targets, anchor)
		j.number(uint64(n.Mode))
		j.flag(n.Nowait)
	case parser.LockTable:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Truncate:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Truncate")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Targets, anchor)
		j.number(uint64(n.Mode))
		j.flag(n.RestartIdentity)
	case parser.Truncate:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Comment:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Comment")
		j.utilityPosition(n.Pos, anchor)
		j.number(uint64(n.Kind))
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Text)
		j.flag(n.Null)
	case parser.Comment:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Show:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Show")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.All)
	case parser.Show:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateRole:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateRole")
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Kind)
		j.utilitySyntax(&n.Options, anchor)
	case parser.CreateRole:
		return j.utilityStatementFields(&n, anchor)
	case *parser.RoleMembership:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RoleMembership")
		utilitySlice(j, n.Targets, anchor)
		utilitySlice(j, n.Members, anchor)
		utilitySlice(j, n.Options, anchor)
		j.utilitySyntax(n.Grantor, anchor)
		j.flag(n.Revoke)
		j.text(n.RevokeOption)
		j.flag(n.Cascade)
	case parser.RoleMembership:
		return j.utilityStatementFields(&n, anchor)
	case *parser.ObjectGrant:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ObjectGrant")
		utilitySlice(j, n.Privileges, anchor)
		utilitySlice(j, n.Relations, anchor)
		utilitySlice(j, n.Routines, anchor)
		utilitySlice(j, n.Schemas, anchor)
		j.text(n.Kind)
		utilitySlice(j, n.Grantees, anchor)
		j.utilitySyntax(n.Grantor, anchor)
		j.flag(n.GrantOption)
		j.flag(n.Revoke)
		j.flag(n.OptionOnly)
		j.flag(n.Cascade)
	case parser.ObjectGrant:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterRole:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterRole")
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Identity)
		j.utilitySyntax(&n.Options, anchor)
	case parser.AlterRole:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropRole:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropRole")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
	case parser.DropRole:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateSchema:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateSchema")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.text(n.Authorization)
		j.flag(n.AuthorizationIdentity)
		j.flag(n.IfNotExists)
		utilitySlice(j, n.Tables, anchor)
	case parser.CreateSchema:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterSchema:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterSchema")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Name)
		j.text(n.NewName)
		j.text(n.Owner)
		j.flag(n.OwnerIdentity)
	case parser.AlterSchema:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropSchema:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropSchema")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.flag(n.Cascade)
	case parser.DropSchema:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateAccessMethod:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateAccessMethod")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.MethodKind)
		j.utilitySyntax(&n.Handler, anchor)
	case parser.CreateAccessMethod:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropAccessMethod:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropAccessMethod")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropAccessMethod:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateCast:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateCast")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Source, anchor)
		j.utilitySyntax(&n.Target, anchor)
		j.utilitySyntax(&n.Function, anchor)
		j.text(n.Method)
		j.text(n.Context)
	case parser.CreateCast:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropCast:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropCast")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Source, anchor)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropCast:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Cluster:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Cluster")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.utilitySyntax(&n.Index, anchor)
	case parser.Cluster:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateCollation:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateCollation")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(n.From, anchor)
		utilitySlice(j, n.Options, anchor)
		j.flag(n.IfNotExists)
	case parser.CreateCollation:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterCollation:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterCollation")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
	case parser.AlterCollation:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropCollation:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropCollation")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropCollation:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateConversion:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateConversion")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Source)
		j.text(n.Destination)
		j.utilitySyntax(&n.Function, anchor)
		j.flag(n.Default)
	case parser.CreateConversion:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterConversion:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterConversion")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
	case parser.AlterConversion:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropConversion:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropConversion")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropConversion:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterDefaultPrivileges:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterDefaultPrivileges")
		utilitySlice(j, n.Options, anchor)
		j.text(n.Class)
		utilitySlice(j, n.Privileges, anchor)
		utilitySlice(j, n.Grantees, anchor)
		j.flag(n.GrantOption)
		j.flag(n.Revoke)
		j.flag(n.OptionOnly)
		j.flag(n.Cascade)
	case parser.AlterDefaultPrivileges:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateDictionary:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateDictionary")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
	case parser.CreateDictionary:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterDictionary:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterDictionary")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
	case parser.AlterDictionary:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropDictionary:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropDictionary")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropDictionary:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateEventTrigger:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateEventTrigger")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Event, anchor)
		utilitySlice(j, n.Filters, anchor)
		j.utilitySyntax(&n.Function, anchor)
	case parser.CreateEventTrigger:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterEventTrigger:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterEventTrigger")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Action)
		j.number(uint64(n.FiringMode))
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
	case parser.AlterEventTrigger:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropEventTrigger:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropEventTrigger")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropEventTrigger:
		return j.utilityStatementFields(&n, anchor)
	case *parser.SetConstraints:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SetConstraints")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.All)
		j.flag(n.Deferred)
	case parser.SetConstraints:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTableRenameConstraint:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTableRenameConstraint")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Constraint, anchor)
		j.utilitySyntax(&n.NewName, anchor)
		j.flag(n.Only)
		j.flag(n.IfExists)
	case parser.AlterTableRenameConstraint:
		return j.utilityStatementFields(&n, anchor)
	case *parser.ForeignDDL:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ForeignDDL")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Action)
		j.text(n.Kind)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.flag(n.IfNotExists)
		j.number(uint64(n.Mode))
		utilitySlice(j, n.Support, anchor)
		utilitySlice(j, n.Options, anchor)
		j.utilitySyntax(&n.Server, anchor)
		j.utilitySyntax(&n.Role, anchor)
		if n.ServerType == nil {
			j.number(0)
		} else {
			j.number(1)
			j.text(*n.ServerType)
		}
		if n.Version == nil {
			j.number(0)
		} else {
			j.number(1)
			j.text(*n.Version)
		}
		if n.Action == "alter" && n.Kind == "server" {
			j.flag(n.VersionSet)
		}
		j.utilitySyntax(&n.NewName, anchor)
		j.utilitySyntax(n.Owner, anchor)
		j.utilitySyntax(n.Table, anchor)
		j.utilityColumnOptions(n.Table, n.ColumnOptions, anchor)
		utilitySlice(j, n.TableActions, anchor)
		j.utilitySyntax(n.Inner, anchor)
		j.utilitySyntax(&n.RemoteSchema, anchor)
		j.utilitySyntax(&n.LocalSchema, anchor)
		utilitySlice(j, n.ImportFilter, anchor)
		j.flag(n.ImportExcept)
	case parser.ForeignDDL:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropIndex:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropIndex")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
		j.flag(n.Concurrent.Character > 0)
	case parser.DropIndex:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterIndex:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterIndex")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.IfExists)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
		j.utilitySyntax(&n.Partition, anchor)
		utilitySlice(j, n.Options, anchor)
	case parser.AlterIndex:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Reindex:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Reindex")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Kind, anchor)
		utilitySlice(j, n.Options, anchor)
		j.flag(n.Concurrent.Character > 0)
	case parser.Reindex:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateOperator:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateOperator")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
	case parser.CreateOperator:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterOperator:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterOperator")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Signature, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
		utilitySlice(j, n.Options, anchor)
	case parser.AlterOperator:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropOperator:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropOperator")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Signatures, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropOperator:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateOperatorClass:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateOperatorClass")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.flag(n.Default)
		j.utilitySyntax(&n.InputType, anchor)
		j.utilitySyntax(&n.Method, anchor)
		j.utilitySyntax(&n.Family, anchor)
		utilitySlice(j, n.Items, anchor)
	case parser.CreateOperatorClass:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterOperatorClass:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterOperatorClass")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Method, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
	case parser.AlterOperatorClass:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropOperatorClass:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropOperatorClass")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Method, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropOperatorClass:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateOperatorFamily:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateOperatorFamily")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Method, anchor)
	case parser.CreateOperatorFamily:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterOperatorFamily:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterOperatorFamily")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Method, anchor)
		j.text(n.Action)
		utilitySlice(j, n.Items, anchor)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
	case parser.AlterOperatorFamily:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropOperatorFamily:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropOperatorFamily")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Method, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropOperatorFamily:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterOwner:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterOwner")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Number, anchor)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.IfExists)
	case parser.AlterOwner:
		return j.utilityStatementFields(&n, anchor)
	case *parser.ReassignOwned:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("ReassignOwned")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Sources, anchor)
		j.utilitySyntax(&n.Target, anchor)
	case parser.ReassignOwned:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropOwned:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropOwned")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Sources, anchor)
		j.number(uint64(n.Mode))
	case parser.DropOwned:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Do:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Do")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Body)
		j.utilitySyntax(&n.Language, anchor)
	case parser.Do:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Publication:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Publication")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Target, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.OwnerIdentity)
		j.flag(n.AllTables)
		utilitySlice(j, n.Members, anchor)
		utilitySlice(j, n.Options, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.Publication:
		return j.utilityStatementFields(&n, anchor)
	case *parser.RenameRole:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("RenameRole")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Role, anchor)
		j.utilitySyntax(&n.Name, anchor)
	case parser.RenameRole:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterRoleSettings:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterRoleSettings")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Role, anchor)
		j.flag(n.All)
		j.utilitySyntax(n.Database, anchor)
		j.utilitySyntax(n.Set, anchor)
		j.utilitySyntax(n.Reset, anchor)
		j.flag(n.FromCurrent)
	case parser.AlterRoleSettings:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterDatabaseSettings:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterDatabaseSettings")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Database, anchor)
		j.utilitySyntax(n.Set, anchor)
		j.utilitySyntax(n.Reset, anchor)
		j.flag(n.FromCurrent)
	case parser.AlterDatabaseSettings:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Call:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Call")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Function, anchor)
	case parser.Call:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateFunction:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateFunction")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Arguments, anchor)
		j.utilitySyntax(&n.Result, anchor)
		j.flag(n.ResultSpecified)
		utilitySlice(j, n.TableResult, anchor)
		j.utilityFunctionOptions(n, anchor)
		j.flag(n.Procedure)
		j.flag(n.Replace)
		utilitySlice(j, n.Standard, anchor)
		j.flag(n.StandardReturn)
	case parser.CreateFunction:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateLanguage:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateLanguage")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Handler, anchor)
		j.utilitySyntax(&n.Inline, anchor)
		j.utilitySyntax(&n.Validator, anchor)
		j.flag(n.Replace)
		j.flag(n.Trusted)
		j.flag(n.Procedural)
	case parser.CreateLanguage:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterLanguage:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterLanguage")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
	case parser.AlterLanguage:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropLanguage:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropLanguage")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropLanguage:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateTransform:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateTransform")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Type, anchor)
		j.utilitySyntax(&n.Language, anchor)
		j.utilitySyntax(n.From, anchor)
		j.utilitySyntax(n.To, anchor)
		j.flag(n.Replace)
	case parser.CreateTransform:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropTransform:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropTransform")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Type, anchor)
		j.utilitySyntax(&n.Language, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropTransform:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateAggregate:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateAggregate")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Arguments, anchor)
		j.flag(n.Ordered)
		j.number(uint64(n.DirectArgs))
		utilitySlice(j, n.Options, anchor)
	case parser.CreateAggregate:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropRoutine:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropRoutine")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		utilitySlice(j, n.Targets, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropRoutine:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterRoutine:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterRoutine")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		j.utilitySyntax(&n.Routine, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
		utilitySlice(j, n.Attributes, anchor)
	case parser.AlterRoutine:
		return j.utilityStatementFields(&n, anchor)
	case *parser.SecurityLabel:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("SecurityLabel")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Kind)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Type, anchor)
		j.utilitySyntax(&n.Routine, anchor)
		j.utilitySyntax(&n.Number, anchor)
		j.utilitySyntax(&n.Provider, anchor)
		j.flag(n.ProviderSpecified)
		j.text(n.Text)
		j.flag(n.Null)
	case parser.SecurityLabel:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateStatistics:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateStatistics")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Relation, anchor)
		utilitySlice(j, n.Elements, anchor)
		utilitySlice(j, n.Kinds, anchor)
		j.flag(n.IfNotExists)
	case parser.CreateStatistics:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropStatistics:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropStatistics")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropStatistics:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterStatisticsTarget:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterStatisticsTarget")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.number(uint64(n.Target))
		j.flag(n.IfExists)
	case parser.AlterStatisticsTarget:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterStatisticsRename:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterStatisticsRename")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.NewName, anchor)
	case parser.AlterStatisticsRename:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterStatisticsSchema:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterStatisticsSchema")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Schema, anchor)
	case parser.AlterStatisticsSchema:
		return j.utilityStatementFields(&n, anchor)
	case *parser.Subscription:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("Subscription")
		j.utilityPosition(n.Pos, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
		j.text(n.Connection)
		utilitySlice(j, n.Publications, anchor)
		utilitySlice(j, n.Options, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.Subscription:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateTemplate:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateTemplate")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
	case parser.CreateTemplate:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterTemplate:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterTemplate")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
	case parser.AlterTemplate:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropTemplate:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropTemplate")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropTemplate:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateTrigger:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateTrigger")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.utilitySyntax(&n.Table, anchor)
		j.flag(n.Replace)
		j.flag(n.Constraint)
		j.utilitySyntax(&n.From, anchor)
		j.flag(n.Deferrable)
		j.flag(n.InitiallyDeferred)
		j.text(n.Timing)
		j.flag(n.Row)
		utilitySlice(j, n.Events, anchor)
		utilitySlice(j, n.Transitions, anchor)
		j.utilitySyntax(n.When, anchor)
		j.utilitySyntax(&n.Function, anchor)
		utilitySlice(j, n.Arguments, anchor)
	case parser.CreateTrigger:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateConfiguration:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateConfiguration")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
	case parser.CreateConfiguration:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterConfiguration:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterConfiguration")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Action)
		j.number(uint64(n.MappingAction))
		utilitySlice(j, n.Tokens, anchor)
		utilitySlice(j, n.Dictionaries, anchor)
		j.utilitySyntax(&n.OldDictionary, anchor)
		j.flag(n.IfExists)
		j.utilitySyntax(&n.Target, anchor)
		j.flag(n.OwnerIdentity)
	case parser.AlterConfiguration:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropConfiguration:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropConfiguration")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropConfiguration:
		return j.utilityStatementFields(&n, anchor)
	case *parser.CreateParser:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("CreateParser")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		utilitySlice(j, n.Options, anchor)
	case parser.CreateParser:
		return j.utilityStatementFields(&n, anchor)
	case *parser.AlterParser:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("AlterParser")
		j.utilityPosition(n.Pos, anchor)
		j.utilitySyntax(&n.Name, anchor)
		j.text(n.Action)
		j.utilitySyntax(&n.Target, anchor)
	case parser.AlterParser:
		return j.utilityStatementFields(&n, anchor)
	case *parser.DropParser:
		if n == nil {
			j.number(0)
			return true
		}
		j.text("DropParser")
		j.utilityPosition(n.Pos, anchor)
		utilitySlice(j, n.Names, anchor)
		j.flag(n.IfExists)
		j.number(uint64(n.Mode))
	case parser.DropParser:
		return j.utilityStatementFields(&n, anchor)
	default:
		return false
	}
	return true
}
