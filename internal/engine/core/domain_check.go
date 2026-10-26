package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) BindDomainConstraintDefinitions(ctx context.Context, op *session.Operation, auth AuthorizationView, stage *CatalogStage, d *catalog.Type, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	// Only a generated CHECK name reads the namespace's constraint names, so
	// explicitly named definitions skip that whole-catalog walk. Names claimed
	// before the first generated name join the set when it is built.
	var used map[string]bool
	var claimed []string
	claim := func(name string) {
		if used == nil {
			claimed = append(claimed, name)
		} else {
			used[name] = true
		}
	}
	generatedName := func() string {
		if used == nil {
			used = catalog.ConstraintNames(auth.Catalog, d.ObjectNamespace, nil)
			for _, name := range claimed {
				used[name] = true
			}
		}
		return catalog.GeneratedConstraintName(d.Name(), "", "check", used)
	}
	installed := make(map[string]bool)
	for _, check := range d.DomainChecks {
		installed[check.Name] = true
	}
	if d.DomainNotNull != nil {
		installed[d.DomainNotNull.Name] = true
		claim(d.DomainNotNull.Name)
	}
	firstOrdinal := len(d.DomainChecks)
	for _, check := range d.DomainChecks {
		firstOrdinal = max(firstOrdinal, check.Ordinal+1)
	}
	for ordinal, column := range raw {
		// Naming observes only definitions installed so far in declaration
		// order. CHECK ordinals still use the original raw-list positions;
		// alphabetical evaluation order is applied after all binding.
		if column.NotNull {
			if err := typedef.BindDomainNotNull(DDLHost{E: e, Op: op, CatalogStage: stage}, auth, d, column.Name.Text); err != nil {
				return err
			}
			installed[d.DomainNotNull.Name] = true
			claim(d.DomainNotNull.Name)
			continue
		}
		check := column.Check
		if check == nil {
			continue
		}
		if check.NoInherit {
			return expr.ErrorAt("42P17", "check constraints for domains cannot be marked NO INHERIT", check.Pos)
		}
		name := check.Name.Text
		if name == "" {
			name = generatedName()
		}
		if installed[name] {
			return SQLError("42710", fmt.Sprintf("constraint %q for domain %q already exists", name, d.Name()))
		}
		installed[name] = true
		claim(name)
		b := NewExpressionBinder(ctx, e, input, op, nil)
		b.Query.Auth = &auth
		b.Query.Namespace = &analyze.QueryNamespace{}
		b.Consumer.CheckConsumer, b.Params.NoParameters = true, true
		base := d.SQLType.DomainBase()
		b.Clause.DomainValueUsage = &base
		node, err := b.BindBoolean(check.Expression, analyze.ProjectionContext, "CHECK")
		if err != nil {
			return table.AlterCookingError(err)
		}
		var refs []ids.ObjectRef
		if _, err := (stored.ScalarRenderer{DomainValue: &base, Dependencies: &refs}).SQL(ctx, auth.Catalog, node); err != nil {
			return err
		}
		oid, err := e.Catalog.AllocateOIDLocked()
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
				stage.DomainBases = append(stage.DomainBases, provider)
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
		diagnostic := SQLError("23502", fmt.Sprintf("domain %s does not allow null values", name)).(*sqlstate.Error)
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
	return PrepareOwnDomainChecks(c, d)
}

// COPY owns a public transport cursor, rather than an expression node. Its live
// statement evaluator is allocated only when a checked input is actually used.
func (s *StatementScope) CheckDomain(ctx context.Context, target *sqltype.Descriptor, value sqltype.Value) error {
	if s.Domains.evaluation == nil {
		catalog := s.Domains.InputCatalog
		if catalog.State() == nil {
			catalog = s.ResolutionCatalog()
		}
		s.Domains.evaluation = &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: HostOf(s), Catalog: catalog, Input: s.Input, Output: s.Output}}
	}
	return s.Domains.evaluation.CheckDomain(ctx, target, value)
}
