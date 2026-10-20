package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) partitionContextPlural() string {
	if b.partitionConsumer == "partition key expression" {
		return "partition key expressions"
	}
	return b.partitionConsumer
}
func (b *expressionBinder) partitionExpressionError(code, message string, pos parser.Position) error {
	if b.partitionConsumer == "partition key expression" {
		return sqlError(code, message)
	}
	return expr.ErrorAt(code, message, pos)
}

func partitionGeneratedError(_ error, pos parser.Position, name string) error {
	d := expr.ErrorAt("42P17", "cannot use generated column in partition key", pos).(*sqlstate.Error)
	d.Detail = fmt.Sprintf(`Column "%s" is a generated column.`, name)
	return d
}

func (e *Engine) bindPartitionKeys(op *Operation, auth AuthorizationView, d *catalog.Relation, raw *parser.PartitionKey, input sqltype.InputEnvironment) (*catalog.PartitionDefinition, error) {
	if raw == nil {
		return nil, nil
	}
	if len(raw.Keys) == 0 || len(raw.Keys) > 32 {
		return nil, sqlError("54011", "cannot partition using more than 32 columns")
	}
	definition := &catalog.PartitionDefinition{Strategy: raw.Strategy, Keys: make([]catalog.PartitionKeyEntry, len(raw.Keys))}
	for i, r := range raw.Keys {
		key := catalog.PartitionKeyEntry{}
		var usage sqltype.Usage
		if r.Expression == nil {
			slot, ok := d.Index[r.Column.Text]
			if !ok {
				if id, _ := sqltype.SystemAttributeType(r.Column.Text); id != 0 {
					return nil, expr.ErrorAt("42P17", fmt.Sprintf(`cannot use system column "%s" in partition key`, r.Column.Text), r.Pos)
				}
				return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" named in partition key does not exist`, r.Column.Text), r.Pos)
			}
			key.Attribute = d.Attributes[slot]
			usage = d.Columns[slot].Usage
			key.Label = r.Column.Text
			if _, generated := d.ColumnMeta.Generations[key.Attribute]; generated {
				diagnostic := sqlError("42P17", "cannot use generated column in partition key")
				return nil, partitionGeneratedError(diagnostic, r.Pos, key.Label)
			}
		} else {
			ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
			columns := make([]queryColumn, len(d.Columns))
			for j, c := range d.Columns {
				columns[j] = queryColumn{descriptor: c, slot: j, leaf: 0, physical: j}
			}
			namespace := queryNamespace{columns: columns, qualifiers: []queryQualifier{{name: d.Name(), original: d.Name(), schema: auth.Catalog.Get(d.Namespace()).Name(), ref: ref, columns: columns}}, frame: d.Columns}
			binder := newExpressionBinder(op.Context(), e, input, op, nil)
			binder.partitionConsumer, binder.noParameters, binder.queryAuth = "partition key expression", true, &auth
			binder.queryNamespace = &namespace
			binder.querySources = []queryLeaf{{ref: ref, relation: d}}
			node, err := binder.bind(r.Expression, expressionPolicy{context: projectionContext})
			if err != nil {
				return nil, err
			}
			evaluation := EvalContext{evalStatement: evalStatement{Context: op.Context(), Input: binder.input, catalog: auth.Catalog}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
			planned, _, err := preflightCoercionsWithPolicy(&evaluation, node, true)
			if err != nil {
				return nil, err
			}
			if plannedVolatility(planned) != expr.Immutable {
				return nil, sqlError("42P17", "functions in partition key expression must be marked IMMUTABLE")
			}
			if !planned.Properties().RowDependent {
				return nil, sqlError("42P17", "cannot use constant expression as partition key")
			}
			for _, slot := range binder.querySources[0].uses {
				if _, generated := d.ColumnMeta.Generations[d.Attributes[slot]]; generated {
					return nil, partitionGeneratedError(sqlError("42P17", "cannot use generated column in partition key"), r.Pos, d.Columns[slot].Name)
				}
			}
			key.Derived = &catalog.DerivedIndexKey{Definition: node, Execution: planned, Attributes: d.Attributes}
			text, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.Owner, roles: auth.Roles}, owner: d, dependencies: &key.Derived.Dependencies}).sql(op.Context(), auth.Catalog, node)
			if err != nil {
				return nil, err
			}
			key.Label = text
			usage = planned.ResultUsage()
		}
		method := catalog.BtreeMethod
		if raw.Strategy == parser.PartitionHash {
			method = catalog.HashMethod
		}
		bound, err := e.bindIndexKey(op, auth, usage, r, method)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42704" && r.Opclass == nil && raw.Strategy == parser.PartitionHash {
				copy := *diagnostic
				copy.Hint = "You must specify a hash operator class or define a default hash operator class for the data type."
				return nil, &copy
			}
			return nil, err
		}
		key.Usage = indexKeyCollation(usage, bound)
		key.Class, key.Collation, key.ExplicitClass = bound.Class, bound.Collation, bound.ExplicitClass
		if raw.Strategy == parser.PartitionHash {
			key.Hash, err = sqltype.BindPartitionHash(key.Usage, key.Class)
		} else {
			key.Comparison, err = sqltype.BindIndexOrdering(key.Usage, key.Class)
		}
		if err != nil {
			return nil, err
		}
		definition.Keys[i] = key
	}
	return definition, nil
}

// preparePartitionBounds checks both arities before any expression demand.
// Transformation remains deferred: a later bind error must not mask an earlier
// NULL or prevent a required nontransactional sequence draw.
func preparePartitionBounds(parent *catalog.Relation, raw parser.PartitionBound) (*catalog.PartitionMembership, error) {
	bound := &catalog.PartitionMembership{Parent: parent.Address(), Strategy: parent.Partition.Strategy, DefaultBound: raw.Default}
	if parent.Partition.Strategy == parser.PartitionHash {
		if raw.Default {
			return nil, sqlError("42P16", "a hash-partitioned table may not have a default partition")
		}
		if raw.Strategy != parser.PartitionHash {
			return nil, expr.ErrorAt("42P16", "invalid bound specification for a hash partition", raw.Pos)
		}
		if raw.Modulus <= 0 {
			return nil, sqlError("42P16", "modulus for hash partition must be an integer value greater than zero")
		}
		if raw.Remainder >= raw.Modulus {
			return nil, sqlError("42P16", "remainder for hash partition must be less than modulus")
		}
		bound.Modulus, bound.Remainder = raw.Modulus, raw.Remainder
		return bound, nil
	}
	if raw.Strategy == parser.PartitionHash {
		return nil, expr.ErrorAt("42P16", "invalid bound specification for a range partition", raw.Pos)
	}
	if raw.Default {
		return bound, nil
	}
	keys := parent.Partition.Keys
	if len(raw.Lower) != len(keys) {
		return nil, sqlError("42P16", "FROM must specify exactly one value per partitioning column")
	}
	if len(raw.Upper) != len(keys) {
		return nil, sqlError("42P16", "TO must specify exactly one value per partitioning column")
	}
	bound.Lower, bound.Upper = make([]catalog.PartitionDatum, len(keys)), make([]catalog.PartitionDatum, len(keys))
	return bound, nil
}

// bindPartitionDatum captures one datum's exact provider references under mu.
// Its caller evaluates the returned tree only after this method releases mu;
// transient sequence calls retain their ordinary RX/grant-time admission.
func (p *pendingPartitionDefinition) bindPartitionDatum(scope *StatementScope, datum parser.PartitionDatum, key catalog.PartitionKeyEntry) (expr.Expr, error) {
	e := scope.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return nil, err
	}
	if auth.Catalog.Epoch() != p.auth.Catalog.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	if auth.Revision != p.stage.authRevision || auth.RoleRecord.Revision != p.stage.roleRevision {
		return nil, fmt.Errorf("stuntgres: authorization changed before publication")
	}
	binder := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
	binder.queryAuth = &p.auth
	binder.noParameters = true
	binder.partitionConsumer = "partition bound"
	node, err := binder.bind(datum.Expression, expressionPolicy{context: projectionContext, expected: key.Usage.Type.Usage()})
	if err != nil {
		return nil, err
	}
	if node.ResultUsage() != key.Usage {
		input, annotations := domainCoercionInput(node, key.Usage)
		info, err := storageCoercionInfo(binder.bindingCatalog(), binder.info(key.Usage, datum.Pos, input.Properties()), input)
		if err != nil {
			return nil, err
		}
		plan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if err != nil {
			return nil, expr.ErrorAt("42804", fmt.Sprintf(`specified value cannot be cast to type %s for column "%s"`, key.Usage.Type.DiagnosticName(), key.Label), datum.Pos)
		}
		info.Property = coercionProperties(input, plan)
		node = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Assignment, false})
	}
	return storageDestination(node, key.Usage, node.SourcePosition())
}

func (p *pendingPartitionDefinition) evaluateBounds(scope *StatementScope, evaluation *EvalContext) error {
	if p.bound.Strategy == parser.PartitionHash {
		return scope.op.Context().Err()
	}
	for side, raw := range [2][]parser.PartitionDatum{p.rawBounds.Lower, p.rawBounds.Upper} {
		tuple := p.bound.Lower
		if side == 1 {
			tuple = p.bound.Upper
		}
		finite := 0
		for i, datum := range raw {
			if err := evaluation.Context.Err(); err != nil {
				return err
			}
			tuple[i] = catalog.PartitionDatum{Kind: datum.Kind, Pos: datum.Pos}
			if datum.Kind != 0 {
				continue
			}
			// PostgreSQL advances the key type cursor only for finite datums,
			// including malformed finite entries after an infinite datum.
			node, err := p.bindPartitionDatum(scope, datum, p.parent.Partition.Keys[finite])
			if err != nil {
				return err
			}
			value, err := node.Eval(evaluation)
			if err != nil {
				return err
			}
			if value.IsNull() {
				return sqlError("42P17", "cannot specify NULL in range bound")
			}
			tuple[i].Value = value
			finite++
		}
		var sentinel int8
		for _, datum := range tuple {
			if sentinel != 0 && datum.Kind != sentinel {
				name := "MINVALUE"
				if sentinel > 0 {
					name = "MAXVALUE"
				}
				return expr.ErrorAt("42804", "every bound following "+name+" must also be "+name, datum.Pos)
			}
			if datum.Kind != 0 {
				sentinel = datum.Kind
			}
		}
	}
	return nil
}
