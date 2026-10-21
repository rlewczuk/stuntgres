package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ownedSequenceName(table, column string, used func(string) bool) string {
	return ddl.GeneratedRelationName(table, column, "seq", used)
}

// Both reciprocal descriptors and initial generator belong to one CREATE stage.
func (e *Engine) stageCreateIdentities(auth AuthorizationView, stage *CatalogStage, table *catalog.Relation, syntax *parser.CreateTable) error {
	for _, element := range syntax.Elements {
		if element.Column == nil {
			continue
		}
		slot := table.Index[element.Column.Name]
		for _, constraint := range element.Column.Constraints {
			if constraint.Identity == nil {
				continue
			}
			definition := constraint.Identity
			as, err := sequence.IdentityTypeOption(table.Columns[slot].Usage.Type.ID())
			if err != nil {
				return err
			}
			split, err := sequence.SplitIdentityOptions(as, definition.Options)
			if err != nil {
				return err
			}
			options, restart, err := sequence.BindOptions(nil, storage.SequenceGenerator{}, split.Raw)
			if err != nil {
				return err
			}
			options.Persistence = split.Persistence
			read := e.stageRead(stage)
			tableNamespace := read.MustNamespace(table.Namespace())
			if tableNamespace.Temporary != (ids.SessionToken{}) {
				if !tableNamespace.TemporaryToast && auth.session != nil && tableNamespace.Temporary == auth.session.SessionToken {
					auth.stagedTemporary = table.Namespace()
				}
				if err := split.CheckTemporary(); err != nil {
					return err
				}
				options.Persistence = "temporary"
			}
			namespace := table.Namespace()
			used := func(name string) bool {
				return len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})) != 0 || name == table.Name()
			}
			name := ownedSequenceName(table.Name(), table.Columns[slot].Name, used)
			if explicit := split.Name; len(explicit.Parts) != 0 {
				auth.Catalog = read
				auth.Recapture(auth.Catalog)
				ns, err := e.creationNamespace(auth, explicit)
				if err != nil {
					return err
				}
				if ns.Address != namespace {
					return sqlError("55000", "sequence must be in same schema as table it is linked to")
				}
				name = explicit.Parts[len(explicit.Parts)-1].Text
				if used(name) {
					return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
				}
			}
			if err := e.stageIdentitySequence(stage, table, slot, name, definition.Mode, options, restart); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) stageIdentitySequence(stage *CatalogStage, table *catalog.Relation, slot int, name string, mode parser.IdentityMode, options catalog.SequenceOptions, restart *int64) error {
	namespace := stage.Base.Get(table.Namespace())
	if staged, ok := stage.Find(table.Namespace()); ok {
		namespace = staged
	}
	if namespace.(*catalog.Namespace).Temporary != (ids.SessionToken{}) {
		options.Persistence = "temporary"
	}
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	rev, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	options.Owned = table.Address()
	options.Owned.SubID = table.Attributes[slot]
	options.Identity = true
	d := sequence.NewRelation(sequence.RelationSpec{Address: address, Namespace: table.Namespace(), Name: name, Revision: rev, Owner: table.Owner, Columns: sequence.Columns(e.catalog.read(e.catalog.state)), Options: options})
	d.Security.ACL = stage.creationACL(table.Owner, table.Namespace(), authorization.DefaultSequences)
	if err := stage.Create(d); err != nil {
		return err
	}
	if table.ColumnMeta.Identities == nil {
		table.ColumnMeta.Identities = make(map[int32]catalog.ColumnIdentity)
	}
	table.ColumnMeta.Identities[table.Attributes[slot]] = catalog.ColumnIdentity{Mode: mode, Sequence: address}
	stage.sequenceState().SetGenerator(address, sequence.InitialGenerator(options, restart, rev))
	return nil
}

// Ordinary single VALUES is a destination-attribute projection. Multirow VALUES
// first demands the complete source row, like the shared query source cursor.
// Bound expressions and imported parameters survive USER pruning in both cases.
func evaluateInsertValuesRow(read catalog.Read, b *analyze.Binding, supplied []expr.Expr, syntax []parser.Expr, row []sqltype.Value, sourceFirst bool, evaluation *exec.Context) error {
	if sourceFirst {
		for i, expression := range supplied {
			if expression == nil {
				continue
			}
			target := b.Targets[i]
			value, err := expression.Eval(evaluation)
			if err != nil {
				return analyze.InsertValueError(read, err, b.Relation.Columns[target], syntax[i], nil)
			}
			if len(b.Insert.IdentityDiscard) == 0 || !b.Insert.IdentityDiscard[target] {
				if len(b.ArrayTargets) != 0 && b.ArrayTargets[i] != nil {
					value, err = b.ArrayTargets[i].Apply(evaluation, row[target], value)
					if err != nil {
						return err
					}
				}
				row[target] = value
			}
		}
	}
	for target, source := range b.Insert.Projection {
		discard := len(b.Insert.IdentityDiscard) != 0 && b.Insert.IdentityDiscard[target]
		if source >= 0 && supplied[source] != nil && !discard {
			if !sourceFirst {
				if len(b.ArrayTargets) != 0 && b.ArrayTargets[source] != nil {
					for i, slot := range b.Targets {
						if slot != target || supplied[i] == nil {
							continue
						}
						value, err := b.ArrayTargets[i].ApplyExpression(evaluation, row[target], supplied[i])
						if err != nil {
							return err
						}
						row[target] = value
					}
					continue
				}
				value, err := supplied[source].Eval(evaluation)
				if err != nil {
					return analyze.InsertValueError(read, err, b.Relation.Columns[target], syntax[source], nil)
				}
				row[target] = value
			}
			continue
		}
		if len(b.Insert.Defaults) != 0 && b.Insert.Defaults[target] != nil {
			value, err := evalBindingDefault(b, evaluation, target, b.Insert.Defaults[target])
			if err != nil {
				return err
			}
			row[target] = value
		}
	}
	return nil
}
