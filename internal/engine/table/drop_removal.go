package table

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
)

// CheckRemovalLocked reports whether a DROP may remove the whole object ref
// from base. Objects the database system requires are refused, naming the
// object as seen from path; registered class providers validate the rest. The
// caller has validated ref against base and holds Engine.mu, which guards the
// providers' state; it takes no lock.
func CheckRemovalLocked(base catalog.Read, path []ids.ObjectAddress, ref ids.ObjectRef, namespace bool) error {
	d := base.Get(ref.Address)
	switch d.(type) {
	case *catalog.Relation:
		if ref.Address.ClassID != ids.RelationClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	case *catalog.Namespace:
		if !namespace || ref.Address.ClassID != ids.NamespaceClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	case *catalog.Type:
		t := d.(*catalog.Type)
		if t.OwningRelation == (ids.ObjectAddress{}) && !t.SQLType.IsUserDefined() && !(t.SQLType.IsArray() && t.SQLType.Element().IsUserDefined()) {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, t.Address(), path)+" because it is required by the database system")
		}
	case *catalog.Collation:
		if d.(*catalog.Collation).Seeded() {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
	case *catalog.Conversion:
		if d.(*catalog.Conversion).Seeded() {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
	case *catalog.AccessMethod:
		method := d.(*catalog.AccessMethod)
		if catalog.AccessMethodSeeded(method) {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
		if err := (catalog.AccessMethodProvider{}).Validate(base, d); err != nil {
			return err
		}
	case *catalog.OperatorClass:
		if catalog.OperatorClassSeeded(d) {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
	case *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
		if catalog.OperatorFamilySeeded(d) {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
	case *catalog.Operator:
		if routine.OperatorSeeded(d.(*catalog.Operator)) {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
	case *catalog.ForeignObject:
		if err := base.Owner().Provider(ref.Address.ClassID).Validate(base, d); err != nil {
			return err
		}
	case *catalog.Statistics:
		if err := (catalog.StatisticsProvider{}).Validate(base, d); err != nil {
			return err
		}
	case *catalog.Subscription, *catalog.EventTrigger, *catalog.Cast, *catalog.Extension, *catalog.Procedure, *catalog.TSTemplate, *catalog.TSDictionary, *catalog.TSConfiguration:
		if procedure, ok := d.(*catalog.Procedure); ok && procedure.Seeded() {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
		if configuration, ok := d.(*catalog.TSConfiguration); ok && configuration.Seeded() {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
		if dictionary, ok := d.(*catalog.TSDictionary); ok && dictionary.Seeded() {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
		if template, ok := d.(*catalog.TSTemplate); ok && template.Seeded() {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(base, d.Address(), path)+" because it is required by the database system")
		}
		objectProvider := base.Owner().Provider(ref.Address.ClassID)
		if objectProvider == nil {
			return fmt.Errorf("stuntgres: missing installed object provider")
		}
		if err := objectProvider.Validate(base, d); err != nil {
			return err
		}
	case *catalog.Parameter:
		if err := base.Owner().Provider(ids.ParameterClassID).Validate(base, d); err != nil {
			return err
		}
	case *catalog.Tablespace:
		return sqlError("2BP01", "cannot drop tablespace "+catalog.DependencyIdentifier(d.Name())+" because it is required by the database system")
	default:
		return fmt.Errorf("stuntgres: only user relations can be removed")
	}
	return nil
}

// ClosureTargetClass reports whether a dependent of class that the drop
// closure reaches becomes a drop target itself.
func ClosureTargetClass(class ids.OID) bool {
	switch class {
	case ids.SubscriptionClassID, ids.StatisticExtClassID:
		return true
	case ids.EventTriggerClassID, ids.AccessMethodClassID, ids.CastClassID, ids.OperatorClassCatalogID, ids.OperatorClassID, ids.OperatorFamilyClassID, ids.AccessMethodOperatorClassID, ids.AccessMethodProcedureClassID, ids.TypeClassID, ids.ExtensionClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID, ids.TSConfigurationClassID:
		return true
	}
	return ids.IsForeignCatalogClass(class)
}

// RetiredProviderClass reports whether a drop target of class is removed
// after the surviving column candidates finished binding against it: the
// closure target classes, collations and conversions.
func RetiredProviderClass(class ids.OID) bool {
	return ClosureTargetClass(class) || class == ids.CollationClassID || class == ids.ConversionClassID
}
