package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) bindDomainChecks(op *session.Operation, auth AuthorizationView, stage *CatalogStage, d *catalog.Type, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	return e.bindDomainCheckRecipes(op.Context(), op, auth, stage, d, raw, input)
}

func (e *Engine) bindDomainCheckRecipes(ctx context.Context, op *session.Operation, auth AuthorizationView, stage *CatalogStage, d *catalog.Type, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	used := catalog.ConstraintNames(auth.Catalog, d.ObjectNamespace, nil)
	installed := make(map[string]bool)
	for _, check := range d.DomainChecks {
		installed[check.Name] = true
	}
	firstOrdinal := len(d.DomainChecks)
	for _, check := range d.DomainChecks {
		firstOrdinal = max(firstOrdinal, check.Ordinal+1)
	}
	for ordinal, column := range raw {
		check := column.Check
		if check == nil {
			continue
		}
		if check.NoInherit {
			return expr.ErrorAt("42P17", "check constraints for domains cannot be marked NO INHERIT", check.Pos)
		}
		name := check.Name.Text
		if name == "" {
			name = catalog.GeneratedConstraintName(d.Name(), "", "check", used)
		}
		if installed[name] {
			return sqlError("42710", fmt.Sprintf("constraint %q for domain %q already exists", name, d.Name()))
		}
		installed[name], used[name] = true, true
		b := newExpressionBinder(ctx, e, input, op, nil)
		b.Query.Auth = &auth
		b.Query.Namespace = &analyze.QueryNamespace{}
		b.Consumer.CheckConsumer, b.Params.NoParameters = true, true
		base := d.SQLType.DomainBase()
		b.Clause.DomainValueUsage = &base
		node, err := b.BindBoolean(check.Expression, analyze.ProjectionContext, "CHECK")
		if err != nil {
			return alterCookingError(err)
		}
		var refs []ids.ObjectRef
		if _, err := (scalarRenderer{domainValue: &base, dependencies: &refs}).sql(ctx, auth.Catalog, node); err != nil {
			return err
		}
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		definition := catalog.DomainCheckConstraint{OID: oid, Name: name, NotValid: check.NotValid, Ordinal: firstOrdinal + ordinal, Expression: node}
		for _, ref := range refs {
			address := ref.Address
			address.SubID = 0
			provider, ok := auth.Catalog.Descriptor(address)
			if !ok {
				return fmt.Errorf("stuntgres: missing domain CHECK provider")
			}
			definition.Dependencies = append(definition.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: provider.Revision()})
			if stage != nil {
				stage.domainBases = append(stage.domainBases, provider)
			}
		}
		d.DomainChecks = append(d.DomainChecks, definition)
	}
	slices.SortFunc(d.DomainChecks, func(a, b catalog.DomainCheckConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}

// CheckDomain prepares the domain's CHECK programme for the context's phase
// and evaluates it. The context may have no statement scope (DDL admission).
func (enginePreparation) CheckDomain(ctx context.Context, c *exec.Context, target *sqltype.Descriptor, candidate sqltype.Value) error {
	view := domainCatalog(c)
	if view.State() == nil {
		return fmt.Errorf("stuntgres: missing domain CHECK catalog")
	}
	d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.ID())})
	if !ok || d.SQLType != target {
		return fmt.Errorf("stuntgres: foreign domain CHECK generation")
	}
	oldValue, oldUsage, oldActive, oldInput := c.DomainValue, c.DomainFrameUsage, c.DomainValueActive, c.Input.DomainChecks
	c.Input.DomainChecks = c
	defer func() {
		c.DomainValue, c.DomainFrameUsage, c.DomainValueActive = oldValue, oldUsage, oldActive
		c.Input.DomainChecks = oldInput
	}()
	// Fresh input may arrive without expression initialization. Prepare every
	// recipe before ordered truth; a later constant error precedes earlier FALSE.
	if err := prepareDomainChecks(c, d); err != nil {
		return err
	}
	if candidate.IsNull() && target.DomainNotNull() {
		name := target.SQLName()
		if c.Input.TypeDiagnosticName != nil {
			if captured := c.Input.TypeDiagnosticName(target.ID()); captured != "" {
				name = captured
			}
		}
		diagnostic := sqlError("23502", fmt.Sprintf("domain %s does not allow null values", name)).(*sqlstate.Error)
		diagnostic.Schema, diagnostic.DataType = view.Get(d.ObjectNamespace).Name(), d.Name()
		return diagnostic
	}
	return c.EvaluateDomainChecks(view, d, d, candidate)
}

// Expression initialization prepares recipes even for an empty consumer. It
// never supplies a candidate or executes CHECK truth; that remains row demand.
func prepareDomainConversion(c *exec.Context, source, target *sqltype.Descriptor) error {
	if source == target {
		return nil
	}
	if target.IsDomain() && target.HasDomainConstraints() {
		view := domainCatalog(c)
		if view.State() == nil {
			return fmt.Errorf("stuntgres: missing domain CHECK catalog")
		}
		d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.ID())})
		if !ok || d.SQLType != target {
			return fmt.Errorf("stuntgres: foreign domain CHECK generation")
		}
		if err := prepareDomainChecks(c, d); err != nil {
			return err
		}
	}
	// A mere outer domain wrapper over an already-typed array does not create
	// fresh member input. Only actual physical element conversions initialize it.
	source, target = source.BaseType(), target.BaseType()
	if source != target && source.IsArray() && target.IsArray() {
		return prepareDomainConversion(c, source.Element(), target.Element())
	}
	return nil
}

func prepareDomainChecks(c *exec.Context, d *catalog.Type) error {
	if base := d.SQLType.DomainBase().Type; base.IsDomain() && base.HasDomainChecks() {
		parent, ok := domainCatalog(c).Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.ID())})
		if !ok || parent.SQLType != base {
			return fmt.Errorf("stuntgres: missing parent domain CHECK generation")
		}
		if err := prepareDomainChecks(c, parent); err != nil {
			return err
		}
	}
	return prepareOwnDomainChecks(c, d)
}

// Historical validation initializes only the selected own programme; ordinary
// conversions retain the ancestor traversal above.
func prepareOwnDomainChecks(c *exec.Context, d *catalog.Type) error {
	scope := scopeOf(c.LiveHost())
	if scope != nil {
		c.PreparedDomainChecks = scope.domains.preparedChecks
		c.DomainPhase = ids.CommandIdentity{Transaction: scope.txRef, Command: scope.command}
	}
	if len(d.DomainChecks) == 0 {
		return nil
	}
	var auth AuthorizationView
	if routineAuth := routineAuthorityView(c); routineAuth != nil {
		auth = *routineAuth
	} else if scope != nil {
		auth = scope.routineAuth
	}
	// Only the existing trusted RI phase establishes owner provenance. A
	// borrowed local capture for input/view admission is still an invoker phase.
	ri := scope != nil && scope.foreignActionAuth != nil &&
		scope.foreignActionAuth.State.CurrentUser == auth.State.CurrentUser
	programme, exists := c.PreparedDomainChecks[d]
	for _, phase := range programPhases(programme) {
		if phase.number == c.DomainPhase && phase.actor == auth.State.CurrentUser && phase.ri == ri {
			return nil // Same initialized programme phase; never a per-row ACL lookup.
		}
	}
	phase := domainCheckPhase{number: c.DomainPhase, actor: auth.State.CurrentUser, ri: ri}
	if exists {
		if err := phase.admission.initializePhase(&programPhases(programme)[0].admission, routineAccessOf(auth)); err != nil {
			return err
		}
	} else {
		for i, check := range d.DomainChecks {
			checkEvaluation := *c
			checkEvaluation.RoutineRecipe, checkEvaluation.RoutineGraft = true, false
			checkEvaluation.RoutineChecks = check.Dependencies
			checkEvaluation.RoutineReferences, checkEvaluation.RoutineConsumer = nil, nil
			checkEvaluation.RoutineProgram = &phase.admission
			node, changed, err := preflightCoercionsWithPolicy(&checkEvaluation, check.Expression, true)
			c.PreparedDomainChecks = checkEvaluation.PreparedDomainChecks
			if err != nil {
				return err
			}
			if err := validateExpressionJoinExecution(&checkEvaluation, node, domainCatalog(&checkEvaluation)); err != nil {
				return err
			}
			if changed {
				if programme.Expressions == nil {
					programme.Expressions = make([]expr.Expr, len(d.DomainChecks))
					for j := range d.DomainChecks {
						programme.Expressions[j] = d.DomainChecks[j].Expression
					}
				}
				programme.Expressions[i] = node
			}
		}
	}
	if program := routineProgramOf(c); program != nil && scope == nil {
		program.include(&phase.admission)
	}
	programme.Phases = append(programPhases(programme), phase)
	if c.PreparedDomainChecks == nil {
		c.PreparedDomainChecks = make(map[*catalog.Type]exec.DomainCheckProgram)
	}
	c.PreparedDomainChecks[d] = programme
	if scope != nil {
		scope.domains.preparedChecks = c.PreparedDomainChecks
	}
	return nil
}

func domainCatalog(c *exec.Context) catalog.Read {
	if c.Catalog.State() != nil {
		return c.Catalog
	}
	if scope := scopeOf(c.LiveHost()); scope != nil {
		if scope.domains.inputCatalog.State() != nil {
			return scope.domains.inputCatalog
		}
		return scope.ResolutionCatalog()
	}
	return catalog.Read{}
}

// COPY owns a public transport cursor, rather than an expression node. Its live
// statement evaluator is allocated only when a checked input is actually used.
func (s *StatementScope) CheckDomain(ctx context.Context, target *sqltype.Descriptor, value sqltype.Value) error {
	if s.domains.evaluation == nil {
		catalog := s.domains.inputCatalog
		if catalog.State() == nil {
			catalog = s.ResolutionCatalog()
		}
		s.domains.evaluation = &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(s), Catalog: catalog, Input: s.input, Output: s.output}}
	}
	return s.domains.evaluation.CheckDomain(ctx, target, value)
}

func validateDomainCheckDefinitions(view catalog.Read, domain *catalog.Type) error {
	base := domain.SQLType.DomainBase()
	if domain.SQLType.HasDomainChecks() != (len(domain.DomainChecks) != 0 || base.Type.HasDomainChecks()) {
		return fmt.Errorf("stuntgres: inconsistent domain CHECK ancestry")
	}
	if domain.SQLType.DomainNotNull() != (domain.SQLType.OwnDomainNotNull() || base.Type.DomainNotNull()) {
		return fmt.Errorf("stuntgres: inconsistent domain NOT NULL ancestry")
	}
	previous := ""
	for _, check := range domain.DomainChecks {
		if check.OID == 0 || check.Name == "" || len(check.Name) > 63 || !utf8.ValidString(check.Name) || strings.IndexByte(check.Name, 0) >= 0 || previous != "" && previous >= check.Name || check.Ordinal < 0 || check.Expression == nil || check.Expression.ResultUsage().Type == nil || check.Expression.ResultUsage().Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid domain CHECK definition")
		}
		previous = check.Name
		if _, err := (scalarRenderer{domainValue: &base}).sql(context.Background(), view, check.Expression); err != nil {
			return err
		}
		for _, dependency := range check.Dependencies {
			address := dependency.Ref.Address
			address.SubID = 0
			provider, exists := view.Descriptor(address)
			if !exists || dependency.Ref.Domain() != view.Owner().Domain || dependency.Ref.Epoch != view.Epoch() || provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.ProcedureObject && provider.Kind() != ids.RelationObject {
				return fmt.Errorf("stuntgres: invalid domain CHECK provider")
			}
		}
	}
	return nil
}
