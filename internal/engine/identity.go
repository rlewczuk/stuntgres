package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

func identityInsertError(column string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "428C9", Message: fmt.Sprintf(`cannot insert a non-DEFAULT value into column "%s"`, column), Detail: fmt.Sprintf(`Column "%s" is an identity column defined as GENERATED ALWAYS.`, column), Hint: "Use OVERRIDING SYSTEM VALUE to override."}
}
func identitySequenceDropError(read catalog.Read, sequence *catalog.Relation) error {
	address := sequence.Sequence.Owned
	address.SubID = 0
	table := read.MustRelation(address)
	column := ""
	for slot, attribute := range table.Attributes {
		if attribute == sequence.Sequence.Owned.SubID {
			column = table.Columns[slot].Name
			break
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop sequence %s because column %s of table %s requires it", sequence.Name(), column, table.Name()), Hint: fmt.Sprintf("You can drop column %s of table %s instead.", column, table.Name())}
}

func ownedSequenceName(table, column string, used func(string) bool) string {
	return generatedRelationName(table, column, "seq", used)
}

func generatedRelationName(table, column, baseLabel string, used func(string) bool) string {
	for suffix := 0; ; suffix++ {
		label := baseLabel
		if suffix != 0 {
			label += strconv.Itoa(suffix)
		}
		left, right := len(table), len(column)
		for left+right > 63-len(label)-2 {
			if left > right {
				left--
			} else {
				right--
			}
		}
		for left > 0 && left < len(table) && !utf8.RuneStart(table[left]) {
			left--
		}
		for right > 0 && right < len(column) && !utf8.RuneStart(column[right]) {
			right--
		}
		name := table[:left] + "_" + column[:right] + "_" + label
		if !used(name) {
			return name
		}
	}
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
			id := table.Columns[slot].Usage.Type.ID()
			if id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 {
				return sequenceOptionError("identity column type must be smallint, integer, or bigint")
			}
			_, _, typeName := sequenceTypeLimits(id)
			raw := []parser.SequenceOption{{Name: "as", Type: parser.TypeName{Name: typeName}}}
			var explicit parser.ObjectName
			persistence := "logged"
			persistenceSet := false
			for _, option := range definition.Options {
				if option.Name == "logged" || option.Name == "unlogged" {
					if persistenceSet {
						return expr.ErrorAt("42601", "conflicting or redundant options", option.Pos)
					}
					persistence, persistenceSet = option.Name, true
					continue
				}
				if option.Name == "name" {
					if len(explicit.Parts) != 0 {
						return expr.ErrorAt("42601", "conflicting or redundant options", option.Pos)
					}
					explicit = option.Owned
					continue
				}
				raw = append(raw, option)
			}
			options, restart, err := bindSequenceOptions(nil, storage.SequenceGenerator{}, raw)
			if err != nil {
				return err
			}
			options.Persistence = persistence
			read := e.stageRead(stage)
			tableNamespace := read.MustNamespace(table.Namespace())
			if tableNamespace.Temporary != (ids.SessionToken{}) {
				if !tableNamespace.TemporaryToast && auth.session != nil && tableNamespace.Temporary == auth.session.token {
					auth.stagedTemporary = table.Namespace()
				}
				if persistenceSet {
					for _, option := range definition.Options {
						if option.Name == "logged" || option.Name == "unlogged" {
							return expr.ErrorAt("42P16", "cannot set logged status of a temporary sequence", option.Pos)
						}
					}
				}
				options.Persistence = "temporary"
			}
			namespace := table.Namespace()
			used := func(name string) bool {
				return len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})) != 0 || name == table.Name()
			}
			name := ownedSequenceName(table.Name(), table.Columns[slot].Name, used)
			if len(explicit.Parts) != 0 {
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
	d := &catalog.Relation{ObjectAddress: address, ObjectNamespace: table.Namespace(), ObjectName: name, ObjectRevision: rev, SequencePlanStamp: rev, Owner: table.Owner, Columns: e.sequenceColumnsLocked(), Index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, Attributes: []int32{1, 2, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, RelationKind: catalog.SequenceRelation, Sequence: &options}
	d.Security.ACL = stage.creationACL(table.Owner, table.Namespace(), authorization.DefaultSequences)
	if err := stage.Create(d); err != nil {
		return err
	}
	if table.ColumnMeta.Identities == nil {
		table.ColumnMeta.Identities = make(map[int32]catalog.ColumnIdentity)
	}
	table.ColumnMeta.Identities[table.Attributes[slot]] = catalog.ColumnIdentity{Mode: mode, Sequence: address}
	initial := options.Start
	if restart != nil {
		initial = *restart
	}
	stage.sequenceState().setGenerator(address, &storage.SequenceGenerator{Last: initial, Generation: rev})
	return nil
}

// Ordinary single VALUES is a destination-attribute projection. Multirow VALUES
// first demands the complete source row, like the shared query source cursor.
// Bound expressions and imported parameters survive USER pruning in both cases.
func evaluateInsertValuesRow(read catalog.Read, b *binding, supplied []expr.Expr, syntax []parser.Expr, row []sqltype.Value, sourceFirst bool, evaluation *EvalContext) error {
	if sourceFirst {
		for i, expression := range supplied {
			if expression == nil {
				continue
			}
			target := b.targets[i]
			value, err := expression.Eval(evaluation)
			if err != nil {
				return insertValueError(read, err, b.relation.Columns[target], syntax[i], nil)
			}
			if len(b.insert.identityDiscard) == 0 || !b.insert.identityDiscard[target] {
				if len(b.arrayTargets) != 0 && b.arrayTargets[i] != nil {
					value, err = b.arrayTargets[i].apply(evaluation, row[target], value)
					if err != nil {
						return err
					}
				}
				row[target] = value
			}
		}
	}
	for target, source := range b.insert.projection {
		discard := len(b.insert.identityDiscard) != 0 && b.insert.identityDiscard[target]
		if source >= 0 && supplied[source] != nil && !discard {
			if !sourceFirst {
				if len(b.arrayTargets) != 0 && b.arrayTargets[source] != nil {
					for i, slot := range b.targets {
						if slot != target || supplied[i] == nil {
							continue
						}
						value, err := b.arrayTargets[i].applyExpression(evaluation, row[target], supplied[i])
						if err != nil {
							return err
						}
						row[target] = value
					}
					continue
				}
				value, err := supplied[source].Eval(evaluation)
				if err != nil {
					return insertValueError(read, err, b.relation.Columns[target], syntax[source], nil)
				}
				row[target] = value
			}
			continue
		}
		if len(b.insert.defaults) != 0 && b.insert.defaults[target] != nil {
			value, err := b.evalDefault(evaluation, target, b.insert.defaults[target])
			if err != nil {
				return err
			}
			row[target] = value
		}
	}
	return nil
}
