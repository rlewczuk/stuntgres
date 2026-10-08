package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

type columnIdentity struct {
	mode     parser.IdentityMode
	sequence ObjectAddress
}

func identityInsertError(column string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "428C9", Message: fmt.Sprintf(`cannot insert a non-DEFAULT value into column "%s"`, column), Detail: fmt.Sprintf(`Column "%s" is an identity column defined as GENERATED ALWAYS.`, column), Hint: "Use OVERRIDING SYSTEM VALUE to override."}
}
func identitySequenceDropError(read CatalogRead, sequence *relationDescriptor) error {
	address := sequence.sequence.owned
	address.SubID = 0
	table := read.state.descriptors[address].(*relationDescriptor)
	column := ""
	for slot, attribute := range table.attributes {
		if attribute == sequence.sequence.owned.SubID {
			column = table.columns[slot].Name
			break
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop sequence %s because column %s of table %s requires it", sequence.name, column, table.name), Hint: fmt.Sprintf("You can drop column %s of table %s instead.", column, table.name)}
}

func ownedSequenceName(table, column string, used func(string) bool) string {
	for suffix := 0; ; suffix++ {
		label := "seq"
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
func (e *Engine) stageCreateIdentities(auth AuthorizationView, stage *CatalogStage, table *relationDescriptor, syntax *parser.CreateTable) error {
	for _, element := range syntax.Elements {
		if element.Column == nil {
			continue
		}
		slot := table.index[element.Column.Name]
		for _, constraint := range element.Column.Constraints {
			if constraint.Identity == nil {
				continue
			}
			definition := constraint.Identity
			id := table.columns[slot].Usage.Type.ID()
			if id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 {
				return sequenceOptionError("sequence type must be smallint, integer, or bigint")
			}
			_, _, typeName := sequenceTypeLimits(id)
			raw := []parser.SequenceOption{{Name: "as", Type: parser.TypeName{Name: typeName}}}
			var explicit parser.ObjectName
			persistence := "logged"
			persistenceSet := false
			for _, option := range definition.Options {
				if option.Name == "logged" || option.Name == "unlogged" {
					if persistenceSet {
						return expressionError("42601", "conflicting or redundant options", option.Pos)
					}
					persistence, persistenceSet = option.Name, true
					continue
				}
				if option.Name == "name" {
					if len(explicit.Parts) != 0 {
						return expressionError("42601", "conflicting or redundant options", option.Pos)
					}
					explicit = option.Owned
					continue
				}
				raw = append(raw, option)
			}
			options, restart, err := bindSequenceOptions(nil, sequenceGenerator{}, raw)
			if err != nil {
				return err
			}
			options.persistence = persistence
			read := e.stageRead(stage)
			namespace := table.namespace
			used := func(name string) bool {
				return len(read.Lookup(NameKey{ClassID: RelationClassID, Namespace: namespace, Name: name})) != 0 || name == table.name
			}
			name := ownedSequenceName(table.name, table.columns[slot].Name, used)
			if len(explicit.Parts) != 0 {
				auth.catalog = read
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
			oid, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			rev, err := e.revisionLocked()
			if err != nil {
				return err
			}
			address := ObjectAddress{ClassID: RelationClassID, ObjectID: oid}
			options.owned = table.address
			options.owned.SubID = table.attributes[slot]
			options.identity = true
			d := &relationDescriptor{address: address, namespace: namespace, name: name, revision: rev, sequencePlanStamp: rev, owner: table.owner, columns: e.sequenceColumnsLocked(), index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, attributes: []int32{1, 2, 3}, relationKind: sequenceRelation, sequence: &options}
			if err := stage.Create(d); err != nil {
				return err
			}
			if table.identities == nil {
				table.identities = make(map[int32]columnIdentity)
			}
			table.identities[table.attributes[slot]] = columnIdentity{mode: definition.Mode, sequence: address}
			if stage.generators == nil {
				stage.generators = make(map[ObjectAddress]*sequenceGenerator)
			}
			initial := options.start
			if restart != nil {
				initial = *restart
			}
			stage.generators[address] = &sequenceGenerator{last: initial, generation: rev}
		}
	}
	return nil
}

// Ordinary single VALUES is a destination-attribute projection. Multirow VALUES
// first demands the complete source row, like the shared query source cursor.
// Bound expressions and imported parameters survive USER pruning in both cases.
func evaluateIdentityValuesRow(read CatalogRead, b *binding, supplied []BoundExpr, syntax []parser.Expr, row []sqltype.Value, sourceFirst bool, evaluation *EvalContext) error {
	if sourceFirst {
		for i, expression := range supplied {
			if expression == nil {
				continue
			}
			target := b.targets[i]
			value, err := expression.Eval(evaluation)
			if err != nil {
				return insertValueError(read, err, b.relation.columns[target], syntax[i], nil)
			}
			if len(b.identityDiscard) == 0 || !b.identityDiscard[target] {
				row[target] = value
			}
		}
	}
	for target, source := range b.insertProjection {
		discard := len(b.identityDiscard) != 0 && b.identityDiscard[target]
		if source >= 0 && supplied[source] != nil && !discard {
			if !sourceFirst {
				value, err := supplied[source].Eval(evaluation)
				if err != nil {
					return insertValueError(read, err, b.relation.columns[target], syntax[source], nil)
				}
				row[target] = value
			}
			continue
		}
		if len(b.defaults) != 0 && b.defaults[target] != nil {
			value, err := b.defaults[target].Eval(evaluation)
			if err != nil {
				return err
			}
			row[target] = value
		}
	}
	return nil
}
