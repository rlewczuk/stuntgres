package control

import (
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Admit adapters for Restore's statement allow-list. The caller's hook table
// keeps the order; these entries never read the caller's run state R.

// AdmitKind admits every statement of kind T unchecked.
func AdmitKind[R any, T parser.Statement](_ R, _ *session.Operation, stmt parser.Statement) (Admission, error) {
	if _, ok := stmt.(T); ok {
		return Admitted, nil
	}
	return NotHandled, nil
}

// AdmitStatement admits a statement of kind T that check accepts. check reads
// only the statement.
func AdmitStatement[R any, T parser.Statement](check func(T) error) func(R, *session.Operation, parser.Statement) (Admission, error) {
	return func(_ R, _ *session.Operation, stmt parser.Statement) (Admission, error) {
		s, ok := stmt.(T)
		if !ok {
			return NotHandled, nil
		}
		if err := check(s); err != nil {
			return NotHandled, err
		}
		return Admitted, nil
	}
}

// AdmitSchemaObject admits the schema-level object DDL.
func AdmitSchemaObject[R any](_ R, _ *session.Operation, stmt parser.Statement) (Admission, error) {
	switch stmt.(type) {
	case *parser.CreateLanguage, *parser.AlterLanguage, *parser.DropLanguage:
		return Admitted, nil
	case *parser.CreateStatistics, *parser.AlterStatisticsTarget:
		return Admitted, nil
	case *parser.CreateParser, *parser.AlterParser, *parser.DropParser:
		return Admitted, nil
	case *parser.CreateTemplate, *parser.AlterTemplate, *parser.DropTemplate, *parser.CreateDictionary, *parser.AlterDictionary, *parser.DropDictionary, *parser.CreateConfiguration, *parser.AlterConfiguration, *parser.DropConfiguration:
		return Admitted, nil
	case *parser.Publication:
		return Admitted, nil
	case *parser.CreateEventTrigger, *parser.AlterEventTrigger, *parser.CreateCast, *parser.DropCast, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.CreateRange, *parser.CreateEnum, *parser.CreateDomain, *parser.AlterType, *parser.CreateCollation, *parser.AlterCollation, *parser.CreateConversion, *parser.AlterConversion:
		return Admitted, nil
	case *parser.DropType, *parser.DropDomain, *parser.DropRoutine:
		return Admitted, nil
	}
	return NotHandled, nil
}

// AdmitOperator admits operator DDL.
func AdmitOperator[R any](_ R, _ *session.Operation, stmt parser.Statement) (Admission, error) {
	switch stmt.(type) {
	case *parser.CreateAccessMethod:
		return Admitted, nil
	case *parser.AlterRoutine:
		return Admitted, nil
	case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
		return Admitted, nil
	}
	return NotHandled, nil
}

// AdmitExtension replays the installation planner's real membership, placement,
// release and retirement operations. Ordinary execution still resolves every
// object and enforces ownership, dependencies and registered provider authority.
func AdmitExtension[R any](_ R, _ *session.Operation, stmt parser.Statement) (Admission, error) {
	switch stmt.(type) {
	case *parser.AlterExtension, *parser.AlterExtensionUpdate, *parser.DropExtension:
		return Admitted, nil
	}
	return NotHandled, nil
}

// AdmitRelationIdentity admits the qualified parent operations used to replay
// associated-array names. Protected rowtypes are never altered independently.
func AdmitRelationIdentity[R any](_ R, _ *session.Operation, stmt parser.Statement) (Admission, error) {
	switch s := stmt.(type) {
	case *parser.AlterTableRenameTable:
		if !s.IfExists && len(s.Name.Parts) == 2 && !stagesTemporary("", s.Name) {
			return Admitted, nil
		}
	case *parser.AlterTableSetSchema:
		if !s.IfExists && !s.Only && len(s.Name.Parts) == 2 && !stagesTemporary("", s.Name) {
			return Admitted, nil
		}
	default:
		return NotHandled, nil
	}
	return NotHandled, sqlError("0A000", "restore accepts only unconditional qualified relation identity ALTER")
}
