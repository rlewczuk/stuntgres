package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Own recipes are immutable and alphabetically ordered. Ancestors remain on
// their own descriptors; conversion preparation and evaluation traverse them first.
type domainCheckConstraint struct {
	OID          ids.OID
	Name         string
	NotValid     bool
	ordinal      int
	expression   BoundExpr
	dependencies []expressionDependency
}

type domainValueExpression struct{ expr.Info }

func (e domainValueExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if !c.domainValueActive || c.domainValue.Type() != e.Usage.Type || c.domainFrameUsage != e.Usage {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing domain VALUE frame")
	}
	return c.domainValue, nil
}

func (e *Engine) bindDomainChecks(op *Operation, auth AuthorizationView, stage *CatalogStage, d *typeDescriptor, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	return e.bindDomainCheckRecipes(op.Context(), op, auth, stage, d, raw, input)
}

// The constructor uses the same VALUE binder and dependency cooking as user
// domains, without a session, SQL execution or a separately allocated type.
func (e *Engine) initializeBuiltinDomainChecks() error {
	view := e.catalog.read(e.catalog.state)
	d := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13256})
	raw, err := parser.Parse(`CREATE DOMAIN information_schema.yes_or_no AS character varying(3) COLLATE "C" CONSTRAINT yes_or_no_check CHECK (VALUE IN ('YES','NO'))`)
	if err != nil {
		return err
	}
	auth := AuthorizationView{
		engine: e, pairedCatalog: view, roleRecord: e.roles, catalog: view,
		roles: e.roleRead(view, e.roles), state: authorization.RoleState{CurrentUser: d.owner},
	}
	return e.bindDomainCheckRecipes(context.Background(), nil, auth, nil, d, raw[0].(*parser.CreateDomain).Constraints, sqltype.InputEnvironment{})
}

func (e *Engine) bindDomainCheckRecipes(ctx context.Context, op *Operation, auth AuthorizationView, stage *CatalogStage, d *typeDescriptor, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	used := constraintNames(auth.catalog, d.namespace, nil)
	installed := make(map[string]bool)
	for _, check := range d.domainChecks {
		installed[check.Name] = true
	}
	firstOrdinal := len(d.domainChecks)
	for _, check := range d.domainChecks {
		firstOrdinal = max(firstOrdinal, check.ordinal+1)
	}
	for ordinal, column := range raw {
		check := column.Check
		if check == nil {
			continue
		}
		if check.NoInherit {
			return expressionError("42P17", "check constraints for domains cannot be marked NO INHERIT", check.Pos)
		}
		name := check.Name.Text
		if name == "" {
			name = generatedConstraintName(d.Name(), "", "check", used)
		}
		if installed[name] {
			return sqlError("42710", fmt.Sprintf("constraint %q for domain %q already exists", name, d.Name()))
		}
		installed[name], used[name] = true, true
		b := newExpressionBinder(ctx, e, input, op, nil)
		b.queryAuth = &auth
		b.queryNamespace = &queryNamespace{}
		b.checkConsumer, b.noParameters = true, true
		base := d.typ.DomainBase()
		b.domainValueUsage = &base
		node, err := b.bindBoolean(check.Expression, projectionContext, "CHECK")
		if err != nil {
			return alterCookingError(err)
		}
		var refs []ids.ObjectRef
		if _, err := (scalarRenderer{domainValue: &base, dependencies: &refs}).sql(ctx, auth.catalog, node); err != nil {
			return err
		}
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		definition := domainCheckConstraint{OID: oid, Name: name, NotValid: check.NotValid, ordinal: firstOrdinal + ordinal, expression: node}
		for _, ref := range refs {
			address := ref.Address
			address.SubID = 0
			provider, ok := auth.catalog.Descriptor(address)
			if !ok {
				return fmt.Errorf("stuntgres: missing domain CHECK provider")
			}
			definition.dependencies = append(definition.dependencies, expressionDependency{ref: ref, revision: provider.Revision()})
			if stage != nil {
				stage.domainBases = append(stage.domainBases, provider)
			}
		}
		d.domainChecks = append(d.domainChecks, definition)
	}
	slices.SortFunc(d.domainChecks, func(a, b domainCheckConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}

func (c *EvalContext) CheckDomain(ctx context.Context, target *sqltype.Descriptor, candidate sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	view := c.domainCatalog()
	if view.state == nil {
		return fmt.Errorf("stuntgres: missing domain CHECK catalog")
	}
	d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.ID())})
	if !ok || d.typ != target {
		return fmt.Errorf("stuntgres: foreign domain CHECK generation")
	}
	oldValue, oldUsage, oldActive, oldInput := c.domainValue, c.domainFrameUsage, c.domainValueActive, c.Input.DomainChecks
	c.Input.DomainChecks = c
	defer func() {
		c.domainValue, c.domainFrameUsage, c.domainValueActive = oldValue, oldUsage, oldActive
		c.Input.DomainChecks = oldInput
	}()
	// Fresh input may arrive without expression initialization. Prepare every
	// recipe before ordered truth; a later constant error precedes earlier FALSE.
	if err := c.prepareDomainChecks(d); err != nil {
		return err
	}
	return c.evaluateDomainChecks(view, d, d, candidate)
}

// Expression initialization prepares recipes even for an empty consumer. It
// never supplies a candidate or executes CHECK truth; that remains row demand.
func (c *EvalContext) prepareDomainConversion(source, target *sqltype.Descriptor) error {
	if source == target {
		return nil
	}
	if target.IsDomain() && target.HasDomainChecks() {
		view := c.domainCatalog()
		if view.state == nil {
			return fmt.Errorf("stuntgres: missing domain CHECK catalog")
		}
		d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.ID())})
		if !ok || d.typ != target {
			return fmt.Errorf("stuntgres: foreign domain CHECK generation")
		}
		if err := c.prepareDomainChecks(d); err != nil {
			return err
		}
	}
	// A mere outer domain wrapper over an already-typed array does not create
	// fresh member input. Only actual physical element conversions initialize it.
	source, target = source.BaseType(), target.BaseType()
	if source != target && source.IsArray() && target.IsArray() {
		return c.prepareDomainConversion(source.Element(), target.Element())
	}
	return nil
}

func (c *EvalContext) prepareDomainChecks(d *typeDescriptor) error {
	if base := d.typ.DomainBase().Type; base.IsDomain() && base.HasDomainChecks() {
		parent, ok := c.domainCatalog().Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.ID())})
		if !ok || parent.typ != base {
			return fmt.Errorf("stuntgres: missing parent domain CHECK generation")
		}
		if err := c.prepareDomainChecks(parent); err != nil {
			return err
		}
	}
	return c.prepareOwnDomainChecks(d)
}

// Historical validation initializes only the selected own programme; ordinary
// conversions retain the ancestor traversal above.
func (c *EvalContext) prepareOwnDomainChecks(d *typeDescriptor) error {
	scope := c.liveScope()
	if scope != nil {
		c.preparedDomainChecks = scope.domainPreparedChecks
		c.domainPhase = domainCheckIdentity{transaction: scope.txRef, command: scope.command}
	}
	if len(d.domainChecks) == 0 {
		return nil
	}
	var auth AuthorizationView
	if c.routineAuthority != nil {
		auth = *c.routineAuthority
	} else if scope != nil {
		auth = scope.routineAuth
	}
	// Only the existing trusted RI phase establishes owner provenance. A
	// borrowed local capture for input/view admission is still an invoker phase.
	ri := scope != nil && scope.foreignActionAuth != nil &&
		scope.foreignActionAuth.state.CurrentUser == auth.state.CurrentUser
	programme, exists := c.preparedDomainChecks[d]
	for _, phase := range programme.phases {
		if phase.number == c.domainPhase && phase.actor == auth.state.CurrentUser && phase.ri == ri {
			return nil // Same initialized programme phase; never a per-row ACL lookup.
		}
	}
	phase := domainCheckPhase{number: c.domainPhase, actor: auth.state.CurrentUser, ri: ri}
	if exists {
		if err := phase.admission.initializePhase(&programme.phases[0].admission, auth); err != nil {
			return err
		}
	} else {
		for i, check := range d.domainChecks {
			checkEvaluation := *c
			checkEvaluation.routineRecipe, checkEvaluation.routineGraft = true, false
			checkEvaluation.routineChecks = check.dependencies
			checkEvaluation.routineReferences, checkEvaluation.routineConsumer = nil, nil
			checkEvaluation.routineProgram = &phase.admission
			node, changed, err := preflightCoercionsWithPolicy(&checkEvaluation, check.expression, true)
			c.preparedDomainChecks = checkEvaluation.preparedDomainChecks
			if err != nil {
				return err
			}
			if err := validateExpressionJoinExecution(&checkEvaluation, node, checkEvaluation.domainCatalog()); err != nil {
				return err
			}
			if changed {
				if programme.expressions == nil {
					programme.expressions = make([]BoundExpr, len(d.domainChecks))
					for j := range d.domainChecks {
						programme.expressions[j] = d.domainChecks[j].expression
					}
				}
				programme.expressions[i] = node
			}
		}
	}
	if c.routineProgram != nil && scope == nil {
		c.routineProgram.include(&phase.admission)
	}
	programme.phases = append(programme.phases, phase)
	if c.preparedDomainChecks == nil {
		c.preparedDomainChecks = make(map[*typeDescriptor]domainCheckProgram)
	}
	c.preparedDomainChecks[d] = programme
	if scope != nil {
		scope.domainPreparedChecks = c.preparedDomainChecks
	}
	return nil
}

func (c *EvalContext) domainCatalog() CatalogRead {
	if c.catalog.state != nil {
		return c.catalog
	}
	if scope := c.liveScope(); scope != nil {
		if scope.domainInputCatalog.state != nil {
			return scope.domainInputCatalog
		}
		return scope.ResolutionCatalog()
	}
	return CatalogRead{}
}

func (c *EvalContext) evaluateDomainChecks(view CatalogRead, target, d *typeDescriptor, candidate sqltype.Value) error {
	base := d.typ.DomainBase()
	if base.Type.IsDomain() && base.Type.HasDomainChecks() {
		parent := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Type.ID())})
		if err := c.evaluateDomainChecks(view, target, parent, candidate); err != nil {
			return err
		}
	}
	return c.evaluateOwnDomainChecks(view, target, d, candidate, nil)
}

func (c *EvalContext) evaluateOwnDomainChecks(view CatalogRead, target, d *typeDescriptor, candidate sqltype.Value, violation error) error {
	base := d.typ.DomainBase()
	if len(d.domainChecks) == 0 {
		return nil
	}
	value, err := candidate.DomainValue(base.Type)
	if err != nil {
		return err
	}
	c.domainValue, c.domainFrameUsage, c.domainValueActive = value, base, true
	expressions := c.preparedDomainChecks[d].expressions
	oldRecipe, oldGraft := c.routineRecipe, c.routineGraft
	oldChecks, oldReferences, oldConsumer := c.routineChecks, c.routineReferences, c.routineConsumer
	defer func() {
		c.routineRecipe, c.routineGraft = oldRecipe, oldGraft
		c.routineChecks, c.routineReferences, c.routineConsumer = oldChecks, oldReferences, oldConsumer
	}()
	for i, check := range d.domainChecks {
		// Preserve the original CHECK recipe for lazily demanded bodies,
		// without changing the phase's actual invoker/RI authority.
		c.routineRecipe, c.routineGraft = true, false
		c.routineChecks, c.routineReferences, c.routineConsumer = check.dependencies, nil, nil
		node := check.expression
		if expressions != nil {
			node = expressions[i]
		}
		result, err := node.Eval(c)
		if err != nil {
			return err
		}
		truth, err := TruthOf(result)
		if err != nil {
			return err
		}
		if truth == False {
			if violation != nil {
				return violation
			}
			name := target.typ.SQLName()
			if c.Input.TypeDiagnosticName != nil {
				if captured := c.Input.TypeDiagnosticName(target.typ.ID()); captured != "" {
					name = captured
				}
			}
			diagnostic := sqlError("23514", fmt.Sprintf("value for domain %s violates check constraint %q", name, check.Name)).(*sqlstate.Error)
			diagnostic.Schema = view.Get(target.namespace).Name()
			diagnostic.DataType, diagnostic.Constraint = target.Name(), check.Name
			return diagnostic
		}
	}
	return nil
}

// COPY owns a public transport cursor, rather than an expression node. Its live
// statement evaluator is allocated only when a checked input is actually used.
func (s *StatementScope) CheckDomain(ctx context.Context, target *sqltype.Descriptor, value sqltype.Value) error {
	if s.domainEvaluation == nil {
		catalog := s.domainInputCatalog
		if catalog.state == nil {
			catalog = s.ResolutionCatalog()
		}
		s.domainEvaluation = &EvalContext{Context: ctx, statementScope: s, catalog: catalog, Input: s.input, Output: s.output}
	}
	return s.domainEvaluation.CheckDomain(ctx, target, value)
}

func domainNullInputExpression(target sqltype.Usage) BoundExpr {
	base := target.BaseUsage()
	plan, err := sqltype.PlanCoercion(base, target, sqltype.Assignment)
	if err != nil {
		panic(err)
	}
	child := literalExpression{Info: expressionInfo{Usage: base}, value: sqltype.Null(base.Type)}
	return coercionExpression{Info: expressionInfo{Usage: target, Property: coercionProperties(child, plan)}, child: child, plan: plan, mode: sqltype.Assignment}
}

func validateDomainCheckDefinitions(view CatalogRead, domain *typeDescriptor) error {
	base := domain.typ.DomainBase()
	if domain.typ.HasDomainChecks() != (len(domain.domainChecks) != 0 || base.Type.HasDomainChecks()) {
		return fmt.Errorf("stuntgres: inconsistent domain CHECK ancestry")
	}
	previous := ""
	for _, check := range domain.domainChecks {
		if check.OID == 0 || check.Name == "" || len(check.Name) > 63 || !utf8.ValidString(check.Name) || strings.IndexByte(check.Name, 0) >= 0 || previous != "" && previous >= check.Name || check.ordinal < 0 || check.expression == nil || check.expression.ResultUsage().Type == nil || check.expression.ResultUsage().Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid domain CHECK definition")
		}
		previous = check.Name
		if _, err := (scalarRenderer{domainValue: &base}).sql(context.Background(), view, check.expression); err != nil {
			return err
		}
		for _, dependency := range check.dependencies {
			address := dependency.ref.Address
			address.SubID = 0
			provider, exists := view.Descriptor(address)
			if !exists || dependency.ref.Domain() != view.owner.domain || dependency.ref.Epoch != view.Epoch() || provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.ProcedureObject && provider.Kind() != ids.RelationObject {
				return fmt.Errorf("stuntgres: invalid domain CHECK provider")
			}
		}
	}
	return nil
}
