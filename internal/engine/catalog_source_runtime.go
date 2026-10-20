package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Retaining handles here is cheap. Only the selected producer demands facts;
// neither construction nor unrelated OIDs inspect mutable runtime state.
type engineCatalogRuntime struct {
	engine       *Engine
	auth         AuthorizationView
	cursors      *cursorRegistry
	settings     *sessionSettings
	input        sqltype.InputEnvironment
	database     string
	temporary    ids.SessionToken
	presentation viewPresentation
	sequenceBase *catalog.State
	sequenceOwn  *catalogChanges
}

func (r engineCatalogRuntime) CursorCount() int { return len(r.cursors.entries) }
func (r engineCatalogRuntime) VisitCursors(visit func(catalogCursorFact) error) error {
	for name, cursor := range r.cursors.entries {
		if err := visit(catalogCursorFact{name, cursor.statement, cursor.hold, cursor.scroll, cursor.created}); err != nil {
			return err
		}
	}
	return nil
}
func (r engineCatalogRuntime) SessionSettings() catalogSettingsFact {
	s := r.settings
	s.mu.Lock()
	defer s.mu.Unlock()
	source := "default"
	if s.rowSecurityProviderDefault {
		source = "client"
	}
	if _, present := s.values["row_security"]; present {
		source = "session"
	}
	return catalogSettingsFact{s.value("row_security"), s.defaults["row_security"], source}
}
func (r engineCatalogRuntime) PreparedCount() int { return len(r.engine.prepared) }
func (r engineCatalogRuntime) VisitPreparedTransactions(ctx context.Context, visit func(catalogPreparedFact) error) error {
	for _, entry := range r.engine.prepared {
		if err := ctx.Err(); err != nil {
			return err
		}
		fact := catalogPreparedFact{XID: r.engine.visibility.AssignedXIDLocked(entry.ref), GID: entry.gid, Prepared: entry.prepared, Database: entry.database}
		if role, ok := r.engine.roles.published.Root.Read(authorization.PrincipalRef{}).Find(entry.owner); ok {
			fact.Owner, fact.OwnerPresent = role.Name, true
		}
		if err := visit(fact); err != nil {
			return err
		}
	}
	return nil
}
func (r engineCatalogRuntime) TimezoneInput() sqltype.InputEnvironment { return r.input }
func (r engineCatalogRuntime) DatabaseName() string                    { return r.database }
func (r engineCatalogRuntime) TemporaryOwner() ids.SessionToken        { return r.temporary }
func (r engineCatalogRuntime) CurrentPrincipal() authorization.PrincipalRef {
	return r.auth.State.CurrentUser
}
func (r engineCatalogRuntime) ValidateCapture(view catalog.Read, roles authorization.Read, ref ids.ObjectRef, source *catalog.Relation, oid ids.OID) error {
	a := r.auth
	switch oid {
	case dataTypePrivilegesOID:
		if view.Owner() == nil || view.State() == nil || roles != a.Roles || a.Owner != view.Owner() || a.Catalog != view || a.RoleRecord == nil || source == nil || source.Address() != ref.Address || source.Address().ObjectID != oid || view.Get(ref.Address) != source {
			return fmt.Errorf("stuntgres: invalid data_type_privileges capture")
		}
		if _, err := a.Object(ref); err != nil {
			return err
		}
	case tablePrivilegesOID:
		if view.Owner() == nil || view.State() == nil || roles != a.Roles || a.Owner != view.Owner() || a.Catalog.State() != view.State() || a.RoleRecord == nil || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || source == nil || source.Address() != ref.Address || source.Address().ObjectID != oid || view.Get(ref.Address) != source {
			return fmt.Errorf("stuntgres: invalid table_privileges capture")
		}
	case 12002:
		if view.Owner() == nil || view.State() == nil || roles != a.Roles || a.Owner != view.Owner() || a.Catalog.State() != view.State() || a.Catalog.Owner() != view.Owner() || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || ref.Address.SubID != 0 || source == nil || source.Address() != ref.Address || source.Address().ObjectID != oid || view.Get(ref.Address) != source {
			return fmt.Errorf("stuntgres: invalid pg_indexes capture")
		}
		return nil
	}
	return a.Roles.ValidatePrincipal(a.State.CurrentUser)
}
func (r engineCatalogRuntime) Effective(ref ids.ObjectRef, owner bool) (authorization.Privileges, error) {
	return r.auth.Effective(ref, owner)
}
func (r engineCatalogRuntime) CanUse(ref ids.ObjectRef) error { return r.auth.CanUse(ref) }
func (r engineCatalogRuntime) SelectColumns(ref ids.ObjectRef, relation *catalog.Relation) error {
	return r.auth.checkColumns(binding{ref: ref, relation: relation}, authorization.SelectPrivilege, nil)
}
func (r engineCatalogRuntime) ValidateParameter(view catalog.Read, d *catalog.Parameter) error {
	return (parameterProvider{&r.engine.settings}).Validate(view, d)
}
func (r engineCatalogRuntime) ForeignServerVisible(server *catalog.ForeignObject, mapped authorization.PrincipalRef) (bool, error) {
	a := r.auth
	actor, err := a.Roles.Descriptor(a.State.CurrentUser)
	if err != nil {
		return false, err
	}
	owner := a.Check(access.Requirement{Object: foreignRef(a, server), OwnerOnly: true}) == nil
	visible := actor.Superuser || mapped == (authorization.PrincipalRef{}) && owner
	if mapped == a.State.CurrentUser {
		visible = visible || owner || a.Check(access.Requirement{Object: foreignRef(a, server), Privileges: authorization.UsagePrivilege}) == nil
	}
	return visible, nil
}
func (r engineCatalogRuntime) VisitPolicyExpressions(policy catalog.PolicyDefinition, visit func(expr.Expr) error) error {
	for _, condition := range []*policyExpression{policyExpressionOf(policy.Using), policyExpressionOf(policy.Check)} {
		if condition != nil {
			if err := visit(condition.expression); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r engineCatalogRuntime) VisitCheckAttributes(ctx context.Context, table *catalog.Relation, expression expr.Expr, visit func(int32) error) error {
	var failure error
	walkBoundExpression(expression, func(node expr.Expr) {
		if failure != nil {
			return
		}
		if failure = ctx.Err(); failure != nil {
			return
		}
		column, ok := node.(columnExpression)
		if ok && column.origin.Address.ClassID == ids.RelationClassID && column.origin.Address.ObjectID == table.Address().ObjectID {
			failure = visit(column.origin.Address.SubID)
		}
	})
	return failure
}
func (r engineCatalogRuntime) ExpressionReferences(ctx context.Context, view catalog.Read, value expr.Expr) ([]ids.ObjectAddress, error) {
	return pgDependExpressionRefs(ctx, view, value)
}
func (r engineCatalogRuntime) ViewReferences(ctx context.Context, view catalog.Read, relation *catalog.Relation) ([]ids.ObjectAddress, error) {
	return pgDependQueryRefs(ctx, view, nil, viewOf(relation).query)
}
func (r engineCatalogRuntime) IndexSimpleAttribute(parent ids.ObjectAddress, key catalog.IndexKey) (int32, bool) {
	if key.Derived == nil {
		return key.Attribute, true
	}
	if column, ok := key.Derived.Definition.(columnExpression); ok && column.origin.Address.ClassID == parent.ClassID && column.origin.Address.ObjectID == parent.ObjectID {
		return column.origin.Address.SubID, true
	}
	return 0, false
}
func (r engineCatalogRuntime) IndexReferences(ctx context.Context, view catalog.Read, definition *catalog.IndexDefinition) ([]ids.ObjectAddress, error) {
	refs := pgDependRefs{ctx: ctx, catalog: view}
	for _, key := range definition.Keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if key.Derived != nil {
			refs.expression(key.Derived.Definition)
		}
		refs.add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)})
	}
	return refs.finish()
}
func (r engineCatalogRuntime) PartitionReferences(ctx context.Context, view catalog.Read, relation *catalog.Relation) ([]ids.ObjectAddress, error) {
	refs := pgDependRefs{ctx: ctx, catalog: view}
	for _, key := range relation.Partition.Keys {
		if key.Attribute != 0 {
			address := relation.Address()
			address.SubID = key.Attribute
			refs.add(address)
		}
		if key.Derived != nil {
			refs.expression(key.Derived.Definition)
		}
		refs.add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(key.Usage.Type.ID())})
		refs.add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)})
		refs.add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Usage.Collation.ID)})
	}
	return refs.finish()
}
func (r engineCatalogRuntime) HasRules(relation *catalog.Relation) bool {
	return len(rulesOf(relation)) != 0
}
func (r engineCatalogRuntime) VisitRules(relation *catalog.Relation, visit func(catalogRuleFact) error) error {
	for _, rule := range rulesOf(relation) {
		if err := visit(catalogRuleFact{rule.OID, rule.Name, ruleEventCode(rule.event), rule.instead, rule.references}); err != nil {
			return err
		}
	}
	return nil
}
func (r engineCatalogRuntime) ViewOptions(relation *catalog.Relation) []string {
	return viewOf(relation).options
}
func (r engineCatalogRuntime) ViewDefinition(ctx context.Context, view catalog.Read, relation *catalog.Relation) (string, error) {
	presentation := r.presentation
	presentation.columns = relation.Columns
	renderer := newViewRenderer(viewOf(relation))
	renderer.presentation = presentation
	renderer.currentNames = true
	definition, err := renderer.selectQuerySQL(ctx, view, viewOf(relation).syntax, viewOf(relation).queries[viewOf(relation).syntax])
	return definition, err
}
func (r engineCatalogRuntime) IndexDefinition(ctx context.Context, view catalog.Read, relation *catalog.Relation) (string, error) {
	return indexDefinitionSQL(ctx, view, relation, indexPresentation{})
}
func (r engineCatalogRuntime) IndexKey(parent *catalog.Relation, key catalog.IndexKey) (int32, expr.Expr, error) {
	return indexCatalogKey(parent, key)
}
func (r engineCatalogRuntime) VisitSequenceDefinitions(ctx context.Context, visit func(catalog.Descriptor) error) error {
	for address, descriptor := range r.sequenceBase.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.sequenceOwn != nil {
			if _, overridden := r.sequenceOwn.descriptors[address]; overridden {
				continue
			}
		}
		if err := visit(descriptor); err != nil {
			return err
		}
	}
	if r.sequenceOwn != nil {
		for _, descriptor := range r.sequenceOwn.descriptors {
			if err := visit(descriptor); err != nil {
				return err
			}
		}
	}
	return nil
}
func catalogSourceTemporaryOwner(auth AuthorizationView) ids.SessionToken {
	if auth.session != nil {
		return auth.session.token
	}
	return ids.SessionToken{}
}
