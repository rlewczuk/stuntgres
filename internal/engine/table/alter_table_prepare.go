package table

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type AlterTablePreparation struct {
	Actions      []parser.AlterTableAction
	Destination  ids.ObjectRef
	Types        []AlterTypePreparation
	Persistence  PersistencePreparation
	AccessMethod AccessMethodPreparation
}

// Prepare root actions in their original order against the original row shape.
// Destination resolution is preparation; movement restrictions are execution.
// Engine.mu is held by the caller.
// On completion error only, the result retains the admitted TYPE prefix for
// sequence AS; that error-path result must never reach a descriptor candidate.
func PrepareAlterTableActions(h AlterHost, auth ddl.Authority, original *catalog.Relation, ref ids.ObjectRef, actions []parser.AlterTableAction) (AlterTablePreparation, error) {
	view := h.AccessView(auth)
	prepared := AlterTablePreparation{Actions: actions}
	fail := func(err error) (AlterTablePreparation, error) {
		return AlterTablePreparation{}, err
	}
	for _, action := range actions {
		if (action.Action == parser.SetLogged || action.Action == parser.SetUnlogged || action.Action == parser.SetAccessMethod) && original.SQLIndex != nil {
			if parent, ok := view.Catalog.Relation(original.SQLIndex.Parent); ok && parent.Partition != nil {
				label := "SET LOGGED"
				if action.Action == parser.SetUnlogged {
					label = "SET UNLOGGED"
				}
				if action.Action == parser.SetAccessMethod {
					label = "SET ACCESS METHOD"
				}
				return fail(persistenceKindError(original, label, "partitioned indexes"))
			}
		}
		if err := AlterTableActionAdmission(original, action); err != nil {
			return fail(err)
		}
		switch action.Action {
		case parser.SetLogged, parser.SetUnlogged:
			var err error
			prepared.Persistence, err = PreparePersistence(view.Catalog, original, action.Action, prepared.Persistence)
			if err != nil {
				return fail(err)
			}
		case parser.SetAccessMethod:
			var err error
			prepared.AccessMethod, err = PrepareAccessMethod(view.Catalog, original, action, h.DefaultTableAccessMethod(), prepared.AccessMethod)
			if err != nil {
				return fail(err)
			}
		case parser.AlterColumnType:
			admitted, err := AdmitAlterType(h, auth, original, ref, action, nil)
			if err != nil {
				return fail(err)
			}
			prepared.Types = append(prepared.Types, admitted)
			captured, err := CompleteAlterType(h, auth, original, action, admitted)
			if err != nil {
				// Sequence AS precedes this error, but not admission failures.
				// Retain only the admitted prefix; later actions are unvisited.
				return prepared, err
			}
			prepared.Types[len(prepared.Types)-1] = captured
		case parser.SetTablespace:
			destination, err := ddl.TablespaceRef(view, action.Tablespace.Text)
			if err != nil {
				return fail(err)
			}
			if err := ddl.CheckAlterTablespaceCreate(view, auth, destination); err != nil {
				return fail(err)
			}
			if prepared.Destination.Address.ObjectID != 0 {
				return fail(sqlError("42601", "cannot have multiple SET TABLESPACE subcommands"))
			}
			prepared.Destination = destination
		}
	}
	if prepared.Destination.Address.ObjectID == 0 {
		return prepared, nil
	}
	if len(actions) == 1 {
		prepared.Actions = nil
		return prepared, nil
	}
	prepared.Actions = make([]parser.AlterTableAction, 0, len(actions)-1)
	for _, action := range actions {
		if action.Action != parser.SetTablespace {
			prepared.Actions = append(prepared.Actions, action)
		}
	}
	return prepared, nil
}

// AlterTableActionKind rejects action on a relation kind it does not apply to.
func AlterTableActionKind(original *catalog.Relation, action parser.AlterTableActionKind) error {
	label := "ALTER COLUMN ... SET NOT NULL"
	switch action {
	case parser.ClusterOn, parser.SetWithoutCluster, parser.SetWithoutOIDs:
		label = "CLUSTER ON"
		if action == parser.SetWithoutCluster {
			label = "SET WITHOUT CLUSTER"
		}
		if action == parser.SetWithoutOIDs {
			label = "SET WITHOUT OIDS"
		}
		switch original.RelationKind {
		case catalog.TableRelation, catalog.PartitionedRelation:
			return nil
		case catalog.MaterializedViewRelation:
			if action != parser.SetWithoutOIDs {
				return nil
			}
			return persistenceKindError(original, label, "materialized views")
		case catalog.ForeignRelation:
			if action == parser.SetWithoutOIDs {
				return nil
			}
			return persistenceKindError(original, label, "foreign tables")
		case catalog.ViewRelation:
			return persistenceKindError(original, label, "views")
		case catalog.IndexRelation:
			return persistenceKindError(original, label, "indexes")
		case catalog.SequenceRelation:
			return persistenceKindError(original, label, "sequences")
		case catalog.CompositeRelation:
			return persistenceCompositeError(original)
		}
		return analyze.TableKindError(original, label)
	case parser.SetStorage, parser.SetCompression:
		if original.RelationKind == catalog.TableRelation || original.RelationKind == catalog.PartitionedRelation || original.RelationKind == catalog.MaterializedViewRelation || action == parser.SetStorage && original.RelationKind == catalog.ForeignRelation {
			return nil
		}
		label = "ALTER COLUMN ... SET STORAGE"
		if action == parser.SetCompression { label = "ALTER COLUMN ... SET COMPRESSION" }
	case parser.InheritTable:
		return RelationshipChildKind(original, action)
	case parser.NoInheritTable:
		label = "NO INHERIT"
	case parser.SetAccessMethod:
		switch original.RelationKind {
		case catalog.TableRelation, catalog.PartitionedRelation, catalog.MaterializedViewRelation:
			return nil
		case catalog.CompositeRelation:
			return persistenceCompositeError(original)
		case catalog.ViewRelation:
			return persistenceKindError(original, "SET ACCESS METHOD", "views")
		case catalog.IndexRelation:
			return persistenceKindError(original, "SET ACCESS METHOD", "indexes")
		case catalog.SequenceRelation:
			return persistenceKindError(original, "SET ACCESS METHOD", "sequences")
		case catalog.ForeignRelation:
			return persistenceKindError(original, "SET ACCESS METHOD", "foreign tables")
		}
		label = "SET ACCESS METHOD"
	case parser.SetLogged, parser.SetUnlogged:
		label = "SET LOGGED"
		if action == parser.SetUnlogged {
			label = "SET UNLOGGED"
		}
		switch original.RelationKind {
		case catalog.TableRelation, catalog.SequenceRelation:
			return nil
		case catalog.PartitionedRelation:
			return persistenceKindError(original, label, "partitioned tables")
		case catalog.ViewRelation:
			return persistenceKindError(original, label, "views")
		case catalog.MaterializedViewRelation:
			return persistenceKindError(original, label, "materialized views")
		case catalog.IndexRelation:
			return persistenceKindError(original, label, "indexes")
		case catalog.ForeignRelation:
			return persistenceKindError(original, label, "foreign tables")
		case catalog.CompositeRelation:
			return persistenceCompositeError(original)
		}
	case parser.SetTablespace:
		return ddl.AlterTablespaceKind(original)
	case parser.SetRelationOptions, parser.ResetRelationOptions:
		if original.RelationKind == catalog.MaterializedViewRelation {
			return nil
		}
		label = "SET"
	case parser.DropNotNull:
		label = "ALTER COLUMN ... DROP NOT NULL"
	case parser.AddNotNull, parser.AddCheck, parser.AddUnique, parser.AddForeignKey, parser.AddExclusion, parser.AddUsingIndex:
		label = "ADD CONSTRAINT"
	case parser.DropConstraint:
		label = "DROP CONSTRAINT"
	case parser.ValidateConstraint:
		label = "VALIDATE CONSTRAINT"
	case parser.AlterConstraintEnforcement, parser.AlterConstraintTiming:
		label = "ALTER CONSTRAINT"
	case parser.EnableRule:
		label = "ENABLE RULE"
	case parser.DisableRule:
		label = "DISABLE RULE"
	case parser.EnableReplicaRule:
		label = "ENABLE REPLICA RULE"
	case parser.EnableAlwaysRule:
		label = "ENABLE ALWAYS RULE"
	case parser.EnableTrigger:
		label = "ENABLE TRIGGER"
	case parser.DisableTrigger:
		label = "DISABLE TRIGGER"
	case parser.EnableReplicaTrigger:
		label = "ENABLE REPLICA TRIGGER"
	case parser.EnableAlwaysTrigger:
		label = "ENABLE ALWAYS TRIGGER"
	case parser.DropColumn:
		label = "DROP COLUMN"
	case parser.SetNotNull:
	case parser.AddIdentity, parser.DropIdentity, parser.SetIdentity:
		label = "ALTER COLUMN"
	case parser.AlterColumnType:
		label = "ALTER COLUMN ... TYPE"
	case parser.AddColumn:
		label = "ADD COLUMN"
	case parser.SetExpression, parser.DropExpression:
		label = "ALTER COLUMN"
	case parser.EnableRowSecurity, parser.DisableRowSecurity, parser.ForceRowSecurity, parser.NoForceRowSecurity:
		return access.RowSecurityKind(original, action, analyze.TableKindError)
	default:
		return fmt.Errorf("stuntgres: invalid ALTER action")
	}
	return analyze.TableKindError(original, label)
}

func AlterTableActionAdmission(original *catalog.Relation, action parser.AlterTableAction) error {
	if !TriggerModeAction(action.Action) {
		return AlterTableActionKind(original, action.Action)
	}
	label := "ENABLE TRIGGER"
	switch action.Action {
	case parser.DisableTrigger:
		label = "DISABLE TRIGGER"
	case parser.EnableReplicaTrigger:
		label = "ENABLE REPLICA TRIGGER"
	case parser.EnableAlwaysTrigger:
		label = "ENABLE ALWAYS TRIGGER"
	}
	switch action.TriggerSelector {
	case parser.TriggerAll:
		label += " ALL"
	case parser.TriggerUser:
		label += " USER"
	}
	return analyze.TableKindError(original, label)
}
