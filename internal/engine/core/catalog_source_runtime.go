package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// Retaining handles here is cheap. Only the selected producer demands facts;
// neither construction nor unrelated OIDs inspect mutable runtime state.
type EngineCatalogRuntime struct {
	engine       *Engine
	Auth         AuthorizationView
	cursors      *cursorRegistry
	settings     *session.SettingsStore
	registry     settings.Registry
	input        sqltype.InputEnvironment
	Database     string
	temporary    ids.SessionToken
	presentation stored.Presentation
	sequenceBase *catalog.State
	sequenceOwn  *session.Journal
}

var _ pgcatalog.Runtime = EngineCatalogRuntime{}

func (r EngineCatalogRuntime) CursorCount() int { return len(r.cursors.Entries) }
func (r EngineCatalogRuntime) VisitCursors(visit func(pgcatalog.CursorFact) error) error {
	for name, cursor := range r.cursors.Entries {
		if err := visit(pgcatalog.CursorFact{name, cursor.statement, cursor.hold, cursor.scroll, cursor.created}); err != nil {
			return err
		}
	}
	return nil
}
func (r EngineCatalogRuntime) SessionSettings(ctx context.Context) ([]pgcatalog.SettingsFact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	readRestricted, err := r.Auth.canReadRestrictedSettings()
	if err != nil {
		return nil, err
	}
	facts := make([]pgcatalog.SettingsFact, 0, len(r.registry.Order()))
	s := r.settings
	s.Mu.Lock()
	for _, name := range r.registry.Order() {
		d := r.registry.Definitions()[name]
		if d.ReadRestricted && !readRestricted {
			continue
		}
		value := s.TypedValue(name)
		switch name {
		case "is_superuser":
			text, err := r.Auth.settingValue(s, name)
			if err != nil {
				s.Mu.Unlock()
				return nil, err
			}
			value = settings.Value{Boolean: text == "on"}
		case "transaction_isolation":
			if s.CurrentIsolation != "" {
				value = settings.Value{Text: s.CurrentIsolation}
			}
		case "transaction_read_only":
			value = settings.Value{Boolean: s.CurrentReadOnly}
		case "transaction_deferrable":
			value = settings.Value{Boolean: s.CurrentDeferrable}
		}
		source := "default"
		if d.NativeStartup != nil && !d.NativeStartup.ResetNull {
			source = "override"
			if d.NativeStartup.Source != "" {
				source = d.NativeStartup.Source
			}
		}
		if captured, present := s.DefaultSources[name]; present {
			source = captured.String()
		}
		if s.ProviderDefaults[name] {
			source = "client"
		}
		if _, present := s.Values[name]; present {
			source = "session"
		}
		facts = append(facts, pgcatalog.SettingsFact{Definition: d, Value: value, Reset: s.DefaultTyped[name], Source: source, ResetCaptured: s.ProviderDefaults[name] || s.DefaultSources[name] != session.DefaultSourceDefault})
	}
	s.Mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}
func (r EngineCatalogRuntime) PreparedCount() int { return r.engine.prepared.Len() }
func (r EngineCatalogRuntime) VisitPreparedTransactions(ctx context.Context, visit func(pgcatalog.PreparedFact) error) error {
	for _, entry := range r.engine.prepared.All() {
		if err := ctx.Err(); err != nil {
			return err
		}
		fact := pgcatalog.PreparedFact{XID: r.engine.Visibility.AssignedXIDLocked(entry.Ref), GID: entry.GID, Prepared: entry.Prepared, Database: entry.Database}
		if role, ok := r.engine.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Find(entry.Owner); ok {
			fact.Owner, fact.OwnerPresent = role.Name, true
		}
		if err := visit(fact); err != nil {
			return err
		}
	}
	return nil
}
func (r EngineCatalogRuntime) TimezoneInput() sqltype.InputEnvironment { return r.input }
func (r EngineCatalogRuntime) DatabaseName() string                    { return r.Database }
func (r EngineCatalogRuntime) TemporaryOwner() ids.SessionToken        { return r.temporary }
func (r EngineCatalogRuntime) CurrentPrincipal() authorization.PrincipalRef {
	return r.Auth.State.CurrentUser
}

// sharedCaptureNames names the information_schema sources that share one
// capture check; only the error names them apart.
var sharedCaptureNames = map[ids.OID]string{
	pgcatalog.TablePrivilegesOID:                 "table_privileges",
	pgcatalog.InfoEnabledRolesOID:                "enabled_roles",
	pgcatalog.InfoCharacterSetsOID:               "character_sets",
	pgcatalog.InfoCollationsOID:                  "collations",
	pgcatalog.InfoSchemataOID:                    "schemata",
	pgcatalog.InfoColumnOptionsOID:               "column_options",
	pgcatalog.InfoTransformsOID:                  "transforms",
	pgcatalog.KeyColumnUsageOID:                  "key_column_usage",
	pgcatalog.ConstraintColumnUsageOID:           "constraint_column_usage",
	pgcatalog.InfoColumnColumnUsageOID:           "column_column_usage",
	pgcatalog.InfoColumnDomainUsageOID:           "column_domain_usage",
	pgcatalog.InfoRoutineSequenceUsageOID:        "routine_sequence_usage",
	pgcatalog.InfoRoutineRoutineUsageOID:         "routine_routine_usage",
	pgcatalog.InfoDomainConstraintsOID:           "domain_constraints",
	pgcatalog.InfoTableConstraintsOID:            "table_constraints",
	pgcatalog.InfoSequencesOID:                   "sequences",
	pgcatalog.InfoDomainsOID:                     "domains",
	pgcatalog.InfoUserDefinedTypesOID:            "user_defined_types",
	pgcatalog.InfoViewTableUsageOID:              "view_table_usage",
	pgcatalog.InfoTriggersOID:                    "triggers",
	pgcatalog.InfoUserMappingsOID:                "user_mappings",
	pgcatalog.InfoUserMappingOptionsOID:          "user_mapping_options",
	pgcatalog.DataTypePrivilegesOID:              "data_type_privileges",
	pgcatalog.InfoRoleColumnGrantsOID:            "role_column_grants",
	pgcatalog.InfoColumnsOID:                     "columns",
	pgcatalog.InfoViewsOID:                       "views",
	pgcatalog.InfoTablesOID:                      "tables",
	pgcatalog.InfoRoleTableGrantsOID:             "role_table_grants",
	pgcatalog.InfoRoutinesOID:                    "routines",
	pgcatalog.InfoParametersOID:                  "parameters",
	pgcatalog.InfoRoleRoutineGrantsOID:           "role_routine_grants",
	pgcatalog.InfoRoleUdtGrantsOID:               "role_udt_grants",
	pgcatalog.InfoSQLFeaturesOID:                 "sql_features",
	pgcatalog.InfoRoleUsageGrantsOID:             "role_usage_grants",
	pgcatalog.InfoUsagePrivilegesOID:             "usage_privileges",
	pgcatalog.InfoSQLSizingOID:                   "sql_sizing",
	pgcatalog.InfoSQLImplementationInfoOID:       "sql_implementation_info",
	pgcatalog.InfoSQLPartsOID:                    "sql_parts",
	pgcatalog.InfoRoutinePrivilegesOID:           "routine_privileges",
	pgcatalog.InfoCheckConstraintRoutineUsageOID: "check_constraint_routine_usage",
	pgcatalog.InfoReferentialConstraintsOID:      "referential_constraints",
	pgcatalog.InfoCheckConstraintsOID:            "check_constraints",
}

func (r EngineCatalogRuntime) ValidateCapture(view catalog.Read, roles authorization.Read, ref ids.ObjectRef, source *catalog.Relation, oid ids.OID) error {
	a := r.Auth
	if name, ok := sharedCaptureNames[oid]; ok {
		if view.Owner() == nil || view.State() == nil || roles != a.Roles || a.Owner != view.Owner() || a.Catalog != view || a.RoleRecord == nil || source == nil || source.Address() != ref.Address || source.Address().ObjectID != oid || view.Get(ref.Address) != source {
			return fmt.Errorf("stuntgres: invalid %s capture", name)
		}
		if _, err := a.Object(ref); err != nil {
			return err
		}
		return a.Roles.ValidatePrincipal(a.State.CurrentUser)
	}
	switch oid {
	case 12002:
		if view.Owner() == nil || view.State() == nil || roles != a.Roles || a.Owner != view.Owner() || a.Catalog.State() != view.State() || a.Catalog.Owner() != view.Owner() || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || ref.Address.SubID != 0 || source == nil || source.Address() != ref.Address || source.Address().ObjectID != oid || view.Get(ref.Address) != source {
			return fmt.Errorf("stuntgres: invalid pg_indexes capture")
		}
		return nil
	}
	return a.Roles.ValidatePrincipal(a.State.CurrentUser)
}
func (r EngineCatalogRuntime) Effective(ref ids.ObjectRef, owner bool) (authorization.Privileges, error) {
	return r.Auth.Effective(ref, owner)
}
func (r EngineCatalogRuntime) CanUse(ref ids.ObjectRef) error { return r.Auth.CanUse(ref) }
func (r EngineCatalogRuntime) SelectColumns(ref ids.ObjectRef, relation *catalog.Relation) error {
	return r.Auth.CheckColumns(ref, relation, authorization.SelectPrivilege, nil)
}
func (r EngineCatalogRuntime) ValidateParameter(view catalog.Read, d *catalog.Parameter) error {
	return (role.ParameterProvider{Settings: &r.engine.Settings}).Validate(view, d)
}
func (r EngineCatalogRuntime) ForeignServerVisible(server *catalog.ForeignObject, mapped authorization.PrincipalRef) (bool, error) {
	a := r.Auth
	actor, err := a.Roles.Descriptor(a.State.CurrentUser)
	if err != nil {
		return false, err
	}
	owner := a.Check(access.Requirement{Object: fdw.ObjectRef(a.View, server), OwnerOnly: true}) == nil
	visible := actor.Superuser || mapped == (authorization.PrincipalRef{}) && owner
	if mapped == a.State.CurrentUser {
		visible = visible || owner || a.Check(access.Requirement{Object: fdw.ObjectRef(a.View, server), Privileges: authorization.UsagePrivilege}) == nil
	}
	return visible, nil
}
func (r EngineCatalogRuntime) VisitCheckAttributes(ctx context.Context, table *catalog.Relation, expression expr.Expr, visit func(int32) error) error {
	var failure error
	plan.WalkExpression(expression, func(node expr.Expr) {
		if failure != nil {
			return
		}
		if failure = ctx.Err(); failure != nil {
			return
		}
		column, ok := node.(plan.ColumnExpr)
		if ok && column.Origin.Address.ClassID == ids.RelationClassID && column.Origin.Address.ObjectID == table.Address().ObjectID {
			failure = visit(column.Origin.Address.SubID)
		}
	})
	return failure
}
func (r EngineCatalogRuntime) ExpressionReferences(ctx context.Context, view catalog.Read, value expr.Expr) ([]ids.ObjectAddress, error) {
	return stored.ExpressionDependRefs(ctx, view, value)
}
func (r EngineCatalogRuntime) ViewReferences(ctx context.Context, view catalog.Read, relation *catalog.Relation) ([]ids.ObjectAddress, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if relation == nil || view.Get(relation.Address()) != relation {
		return nil, ids.ErrCatalogReplaced
	}
	definition := stored.ViewOf(relation)
	if definition == nil {
		return nil, ids.ErrCatalogReplaced
	}
	validate := func(ref ids.ObjectRef) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ref.Address.ClassID != ids.RelationClassID {
			return nil
		}
		if !stored.ViewReferenceValid(view, relation, ref) {
			return ids.ErrCatalogReplaced
		}
		address := ref.Address
		address.SubID = 0
		target, ok := view.Relation(address)
		if !ok || ref.Address.SubID != 0 && !target.HasSupportedAttribute(ref.Address.SubID) {
			return fmt.Errorf("stuntgres: invalid stored view dependency attribute")
		}
		return nil
	}
	// The immutable lifetime manifest retains ObjectRefs; the dependency
	// recording pass below intentionally returns only PostgreSQL addresses.
	// Validate before pinning or pair deduplication can hide a malformed ref.
	for _, source := range definition.Sources() {
		if source.Ref.Address.SubID != 0 {
			return nil, ids.ErrCatalogReplaced
		}
		if err := validate(source.Ref); err != nil {
			return nil, err
		}
	}
	for _, dependency := range definition.Dependencies() {
		if err := validate(dependency.Ref); err != nil {
			return nil, err
		}
	}
	return stored.QueryDependRefs(ctx, view, nil, definition.Query())
}
func (r EngineCatalogRuntime) IndexSimpleAttribute(parent ids.ObjectAddress, key catalog.IndexKey) (int32, bool) {
	if key.Derived == nil {
		return key.Attribute, true
	}
	if column, ok := key.Derived.Definition.(plan.ColumnExpr); ok && column.Origin.Address.ClassID == parent.ClassID && column.Origin.Address.ObjectID == parent.ObjectID {
		return column.Origin.Address.SubID, true
	}
	return 0, false
}
func (r EngineCatalogRuntime) IndexReferences(ctx context.Context, view catalog.Read, definition *catalog.IndexDefinition) ([]ids.ObjectAddress, error) {
	return stored.IndexDependRefs(ctx, view, definition)
}
func (r EngineCatalogRuntime) PartitionReferences(ctx context.Context, view catalog.Read, relation *catalog.Relation) ([]ids.ObjectAddress, error) {
	return stored.PartitionDependRefs(ctx, view, relation)
}
func (r EngineCatalogRuntime) ViewDefinition(ctx context.Context, view catalog.Read, relation *catalog.Relation) (string, error) {
	if relation.View == nil {
		return r.builtinViewDefinition(ctx, view, relation)
	}
	return stored.ViewDefinitionSQL(ctx, view, relation, r.presentation)
}
func (r EngineCatalogRuntime) IndexDefinition(ctx context.Context, view catalog.Read, relation *catalog.Relation) (string, error) {
	return index.DefinitionSQL(ctx, view, relation, index.Presentation{})
}
func (r EngineCatalogRuntime) IndexVectors(ctx context.Context, view catalog.Read, relation *catalog.Relation, types [4]*sqltype.Descriptor) ([4]sqltype.Value, error) {
	var values [4]sqltype.Value
	facts, _, err := index.ProjectIndexMetadata(ctx, view, view.Owner().Domain.Ref(view.Epoch(), relation.Address()))
	if err != nil {
		return values, err
	}
	values[0], err = sqltype.CatalogInt2Vector(types[0], facts.IndKey)
	if err != nil {
		return values, err
	}
	values[1], err = sqltype.CatalogOIDVector(types[1], facts.IndCollation)
	if err != nil {
		return values, err
	}
	values[2], err = sqltype.CatalogOIDVector(types[2], facts.IndClass)
	if err != nil {
		return values, err
	}
	values[3], err = sqltype.CatalogInt2Vector(types[3], facts.IndOption)
	return values, err
}
func (r EngineCatalogRuntime) VisitRuleDefinitions(ctx context.Context, view catalog.Read, relation *catalog.Relation, visit func(catalog.RuleFact, string) error) error {
	for _, rule := range stored.RulesOf(relation) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rule.Name == "_RETURN" {
			continue
		}
		definition, err := stored.RuleDefinitionSQL(ctx, view, relation, rule, r.presentation, r.temporary)
		if err != nil {
			return err
		}
		event := "1"
		switch rule.Event() {
		case "UPDATE":
			event = "2"
		case "INSERT":
			event = "3"
		case "DELETE":
			event = "4"
		}
		if err := visit(catalog.RuleFact{OID: rule.OID, Name: rule.Name, Event: event, Instead: rule.Instead(), References: rule.References()}, definition); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (r EngineCatalogRuntime) VisitTriggerDefinitions(ctx context.Context, view catalog.Read, relation *catalog.Relation, disclose bool, visit func(pgcatalog.TriggerFact) error) error {
	return stored.VisitTriggerDefinitions(ctx, view, relation, r.presentation, disclose, func(f stored.TriggerPresentation) error {
		return visit(pgcatalog.TriggerFact{OID: f.OID, Name: f.Name, Insert: f.Insert, Update: f.Update, Delete: f.Delete, Timing: f.Timing, Orientation: f.Orientation, Condition: f.Condition, HasCondition: f.HasCondition, Statement: f.Statement})
	})
}
func (r EngineCatalogRuntime) IndexKey(parent *catalog.Relation, key catalog.IndexKey) (int32, expr.Expr, error) {
	return index.CatalogKey(parent, key)
}
func (r EngineCatalogRuntime) VisitSequenceDefinitions(ctx context.Context, visit func(catalog.Descriptor) error) error {
	return visitSequenceOverlay(r.sequenceBase, r.sequenceOwn, ctx.Err, visit)
}

// visitSequenceOverlay visits the base definitions that own does not
// override, then own's. A non-nil check runs before each base definition.
func visitSequenceOverlay(base *catalog.State, own *session.Journal, check func() error, visit func(catalog.Descriptor) error) error {
	for address, descriptor := range base.Descriptors() {
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		if own != nil {
			if _, overridden := own.Descriptors[address]; overridden {
				continue
			}
		}
		if err := visit(descriptor); err != nil {
			return err
		}
	}
	if own != nil {
		for _, descriptor := range own.Descriptors {
			if err := visit(descriptor); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r EngineCatalogRuntime) ExpressionTree(typ *sqltype.Descriptor, expression expr.Expr) (sqltype.Value, error) {
	return plan.CatalogTree(typ, expression)
}
func (r EngineCatalogRuntime) ForeignKeyComparisons(view catalog.Read, child, parent, index *catalog.Relation, key catalog.ForeignKeyConstraint) ([]pgcatalog.ForeignKeyComparison, error) {
	bound, err := table.BindForeignKeyComparison(view, child, parent, index, key)
	if err != nil {
		return nil, err
	}
	comparisons := make([]pgcatalog.ForeignKeyComparison, len(bound))
	for i, comparison := range bound {
		comparisons[i] = pgcatalog.ForeignKeyComparison{comparison.Child, comparison.Parent, comparison.Comparison}
	}
	return comparisons, nil
}
func (r EngineCatalogRuntime) ValidateType(view catalog.Read, d *catalog.Type) error {
	return (stored.TypeProvider{}).Validate(view, d)
}
func (r EngineCatalogRuntime) TypeOutput(owner *catalog.Owner, view catalog.Read, roles authorization.Read) sqltype.OutputEnvironment {
	return sqltype.OutputEnvironment{
		Regclass: regobj.ClassOutput{View: view},
		Regrole:  regobj.RoleOutput{Owner: owner, Roles: roles},
		ACLItem:  regobj.ACLItemOutput{Owner: owner, Roles: roles},
	}
}
func (r EngineCatalogRuntime) ObjectACL(view catalog.Read, address ids.ObjectAddress) (authorization.ACLValue, error) {
	return access.RawObjectACL(view, address)
}
func (r EngineCatalogRuntime) ValidateACL(roles authorization.Read, d catalog.Descriptor) error {
	return access.ValidateACL(roles, d)
}
func (r EngineCatalogRuntime) Pinned(view catalog.Read, address ids.ObjectAddress) bool {
	return stored.DependPinned(view, nil, address)
}
func (r EngineCatalogRuntime) OperatorSeeded(d *catalog.Operator) bool {
	return routine.OperatorSeeded(d)
}
func (r EngineCatalogRuntime) OperatorDependencies(view catalog.Read, d *catalog.Operator) []ids.ObjectAddress {
	return routine.OperatorDependencyAddresses(view, d)
}
func (r EngineCatalogRuntime) TypeProviderProcedure(d *catalog.Procedure) bool {
	return extension.HstoreProviderOf(d) != nil && !extension.HstoreComparisonName(d.Display.Source) || extension.SegProviderOf(d) != nil || extension.IntarrayProviderOf(d) != nil || extension.LtreeProviderOf(d) != nil || extension.CubeProviderOf(d) != nil && !extension.CubeComparisonName(d.Name()) || extension.CitextProviderOf(d) != nil && (d.Signature == "2275" || d.Result == 2275) || extension.IsnProviderOf(d) != nil
}
func CatalogSourceTemporaryOwner(auth AuthorizationView) ids.SessionToken {
	if auth.Session != nil {
		return auth.Session.SessionToken
	}
	return ids.SessionToken{}
}
