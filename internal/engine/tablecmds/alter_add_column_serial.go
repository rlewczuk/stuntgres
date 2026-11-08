package tablecmds

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

// stageAddedSerials stages the owned sequence and nextval DEFAULT of every
// serial ADD COLUMN into the statement's sole stage, in ADD order. Descendants
// copy the default their immediate parent staged instead of creating a second
// sequence, so the hierarchy shares one generator.
func stageAddedSerials(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, stage *core.CatalogStage, old, next *catalog.Relation, additions []table.AddedColumn) error {
	stage = authorizedStage(e, stage, auth, op.Session)
	descendant := scope.Inheritance.Alter != nil && scope.Inheritance.Alter.Descendant
	for i := range additions {
		added := &additions[i]
		if !added.Declaration.Serial {
			continue
		}
		slot := next.Index[added.Column.Name]
		if descendant {
			copyInheritedSerialDefault(e, stage, old, next, added)
			continue
		}
		var declarations []analyze.ColumnDeclaration
		e.Mu.Lock()
		declarations = make([]analyze.ColumnDeclaration, len(next.Columns))
		declarations[slot] = added.Declaration
		err := table.StageCreateSerials(TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, next, declarations)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		bindAuth := auth
		bindAuth.Catalog = e.StageRead(stage)
		bindAuth.Recapture(bindAuth.Catalog)
		if err := table.BindAddedDefault(AlterHost{TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, scope}, bindAuth, old, next, next.Columns[slot], added.Attribute, declarations[slot].DefaultExpr, added); err != nil {
			return err
		}
	}
	return nil
}

// copyInheritedSerialDefault installs the parent's staged default on the child
// column. A local OID leaves the child definition OID to AssignDefinitionOIDs;
// Expression, dependencies and snapshots stay shared with the parent.
func copyInheritedSerialDefault(e *core.Engine, stage *core.CatalogStage, old, next *catalog.Relation, added *table.AddedColumn) {
	if old.Inheritance == nil {
		return
	}
	read := e.StageRead(stage)
	for _, edge := range old.Inheritance.Parents {
		parent, ok := read.Relation(edge.Relation)
		if !ok {
			continue
		}
		slot, ok := parent.Index[added.Column.Name]
		if !ok {
			continue
		}
		inherited, ok := parent.ColumnMeta.Defaults[parent.Attributes[slot]]
		if !ok {
			continue
		}
		persisted := catalog.ColumnDefault{Expression: inherited.Expression, Dependencies: inherited.Dependencies, CatalogSnapshot: inherited.CatalogSnapshot, CatalogAliasReferences: inherited.CatalogAliasReferences}
		if next.ColumnMeta.Defaults == nil {
			next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault)
		}
		next.ColumnMeta.Defaults[added.Attribute] = persisted
		added.Expression, added.Dependencies, added.Consumer = persisted.Expression, persisted.Dependencies, old
		return
	}
}