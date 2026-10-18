package engine

import (
	"fmt"
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
	return expressionError(code, message, pos)
}

func partitionGeneratedError(_ error, pos parser.Position, name string) error {
	d := expressionError("42P17", "cannot use generated column in partition key", pos).(*sqlstate.Error)
	d.Detail = fmt.Sprintf(`Column "%s" is a generated column.`, name)
	return d
}

func (e *Engine) bindPartitionKeys(op *Operation, auth AuthorizationView, d *relationDescriptor, raw *parser.PartitionKey, input sqltype.InputEnvironment) (*partitionDefinition, error) {
	if raw == nil {
		return nil, nil
	}
	if len(raw.Keys) == 0 || len(raw.Keys) > 32 {
		return nil, sqlError("54011", "cannot partition using more than 32 columns")
	}
	definition := &partitionDefinition{strategy: raw.Strategy, keys: make([]partitionKeyEntry, len(raw.Keys))}
	for i, r := range raw.Keys {
		key := partitionKeyEntry{}
		var usage sqltype.Usage
		if r.Expression == nil {
			slot, ok := d.index[r.Column.Text]
			if !ok {
				if id, _ := sqltype.SystemAttributeType(r.Column.Text); id != 0 {
					return nil, expressionError("42P17", fmt.Sprintf(`cannot use system column "%s" in partition key`, r.Column.Text), r.Pos)
				}
				return nil, expressionError("42703", fmt.Sprintf(`column "%s" named in partition key does not exist`, r.Column.Text), r.Pos)
			}
			key.attribute = d.attributes[slot]
			usage = d.columns[slot].Usage
			key.label = r.Column.Text
			if _, generated := d.generations[key.attribute]; generated {
				diagnostic := sqlError("42P17", "cannot use generated column in partition key")
				return nil, partitionGeneratedError(diagnostic, r.Pos, key.label)
			}
		} else {
			ref := e.catalog.domain.Ref(auth.catalog.Epoch(), d.address)
			columns := make([]queryColumn, len(d.columns))
			for j, c := range d.columns {
				columns[j] = queryColumn{descriptor: c, slot: j, leaf: 0, physical: j}
			}
			namespace := queryNamespace{columns: columns, qualifiers: []queryQualifier{{name: d.name, original: d.name, schema: auth.catalog.Get(d.namespace).Name(), ref: ref, columns: columns}}, frame: d.columns}
			binder := newExpressionBinder(op.Context(), e, input, op, nil)
			binder.partitionConsumer, binder.noParameters, binder.queryAuth = "partition key expression", true, &auth
			binder.queryNamespace = &namespace
			binder.querySources = []queryLeaf{{ref: ref, relation: d}}
			node, err := binder.bind(r.Expression, expressionPolicy{context: projectionContext})
			if err != nil {
				return nil, err
			}
			evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: binder.input, catalog: auth.catalog}
			planned, _, err := preflightCoercionsWithPolicy(&evaluation, node, true)
			if err != nil {
				return nil, err
			}
			if plannedVolatility(planned) != Immutable {
				return nil, sqlError("42P17", "functions in partition key expression must be marked IMMUTABLE")
			}
			if !planned.Properties().RowDependent {
				return nil, sqlError("42P17", "cannot use constant expression as partition key")
			}
			for _, slot := range binder.querySources[0].uses {
				if _, generated := d.generations[d.attributes[slot]]; generated {
					return nil, partitionGeneratedError(sqlError("42P17", "cannot use generated column in partition key"), r.Pos, d.columns[slot].Name)
				}
			}
			key.derived = &derivedIndexKey{definition: node, execution: planned, attributes: d.attributes}
			text, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: d, dependencies: &key.derived.dependencies}).sql(op.Context(), auth.catalog, node)
			if err != nil {
				return nil, err
			}
			key.label = text
			usage = planned.ResultUsage()
		}
		method := btreeMethod
		if raw.Strategy == parser.PartitionHash {
			method = hashMethod
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
		key.usage = indexKeyCollation(usage, bound)
		key.class, key.collation, key.explicitClass = bound.class, bound.collation, bound.explicitClass
		if raw.Strategy == parser.PartitionHash {
			key.hash, err = sqltype.BindPartitionHash(key.usage, key.class)
		} else {
			key.comparison, err = sqltype.BindIndexOrdering(key.usage, key.class)
		}
		if err != nil {
			return nil, err
		}
		definition.keys[i] = key
	}
	return definition, nil
}

// preparePartitionBounds checks both arities before any expression demand.
// Transformation remains deferred: a later bind error must not mask an earlier
// NULL or prevent a required nontransactional sequence draw.
func preparePartitionBounds(parent *relationDescriptor, raw parser.PartitionBound) (*partitionMembership, error) {
	bound := &partitionMembership{parent: parent.address, strategy: parent.partition.strategy, defaultBound: raw.Default}
	if parent.partition.strategy == parser.PartitionHash {
		if raw.Default {
			return nil, sqlError("42P16", "a hash-partitioned table may not have a default partition")
		}
		if raw.Strategy != parser.PartitionHash {
			return nil, expressionError("42P16", "invalid bound specification for a hash partition", raw.Pos)
		}
		if raw.Modulus <= 0 {
			return nil, sqlError("42P16", "modulus for hash partition must be an integer value greater than zero")
		}
		if raw.Remainder >= raw.Modulus {
			return nil, sqlError("42P16", "remainder for hash partition must be less than modulus")
		}
		bound.modulus, bound.remainder = raw.Modulus, raw.Remainder
		return bound, nil
	}
	if raw.Strategy == parser.PartitionHash {
		return nil, expressionError("42P16", "invalid bound specification for a range partition", raw.Pos)
	}
	if raw.Default {
		return bound, nil
	}
	keys := parent.partition.keys
	if len(raw.Lower) != len(keys) {
		return nil, sqlError("42P16", "FROM must specify exactly one value per partitioning column")
	}
	if len(raw.Upper) != len(keys) {
		return nil, sqlError("42P16", "TO must specify exactly one value per partitioning column")
	}
	bound.lower, bound.upper = make([]partitionDatum, len(keys)), make([]partitionDatum, len(keys))
	return bound, nil
}

// bindPartitionDatum captures one datum's exact provider references under mu.
// Its caller evaluates the returned tree only after this method releases mu;
// transient sequence calls retain their ordinary RX/grant-time admission.
func (p *pendingPartitionDefinition) bindPartitionDatum(scope *StatementScope, datum parser.PartitionDatum, key partitionKeyEntry) (BoundExpr, error) {
	e := scope.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return nil, err
	}
	if auth.catalog.Epoch() != p.auth.catalog.Epoch() {
		return nil, ErrCatalogReplaced
	}
	if auth.revision != p.stage.authRevision || auth.roleRecord.revision != p.stage.roleRevision {
		return nil, fmt.Errorf("stuntgres: authorization changed before publication")
	}
	binder := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
	binder.queryAuth = &p.auth
	binder.noParameters = true
	binder.partitionConsumer = "partition bound"
	node, err := binder.bind(datum.Expression, expressionPolicy{context: projectionContext, expected: key.usage.Type.Usage()})
	if err != nil {
		return nil, err
	}
	if node.ResultUsage() != key.usage {
		input, annotations := domainCoercionInput(node, key.usage)
		info, err := storageCoercionInfo(binder.bindingCatalog(), binder.info(key.usage, datum.Pos, input.Properties()), input)
		if err != nil {
			return nil, err
		}
		plan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if err != nil {
			return nil, expressionError("42804", fmt.Sprintf(`specified value cannot be cast to type %s for column "%s"`, key.usage.Type.DiagnosticName(), key.label), datum.Pos)
		}
		info.Property = coercionProperties(input, plan)
		node = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Assignment, false})
	}
	return storageDestination(node, key.usage, node.SourcePosition())
}

func (p *pendingPartitionDefinition) evaluateBounds(scope *StatementScope, evaluation *EvalContext) error {
	if p.bound.strategy == parser.PartitionHash {
		return scope.op.Context().Err()
	}
	for side, raw := range [2][]parser.PartitionDatum{p.rawBounds.Lower, p.rawBounds.Upper} {
		tuple := p.bound.lower
		if side == 1 {
			tuple = p.bound.upper
		}
		finite := 0
		for i, datum := range raw {
			if err := evaluation.Context.Err(); err != nil {
				return err
			}
			tuple[i] = partitionDatum{kind: datum.Kind, pos: datum.Pos}
			if datum.Kind != 0 {
				continue
			}
			// PostgreSQL advances the key type cursor only for finite datums,
			// including malformed finite entries after an infinite datum.
			node, err := p.bindPartitionDatum(scope, datum, p.parent.partition.keys[finite])
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
			tuple[i].value = value
			finite++
		}
		var sentinel int8
		for _, datum := range tuple {
			if sentinel != 0 && datum.kind != sentinel {
				name := "MINVALUE"
				if sentinel > 0 {
					name = "MAXVALUE"
				}
				return expressionError("42804", "every bound following "+name+" must also be "+name, datum.pos)
			}
			if datum.kind != 0 {
				sentinel = datum.kind
			}
		}
	}
	return nil
}
