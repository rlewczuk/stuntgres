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
	case *parser.CreateCast, *parser.DropCast, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.CreateEnum, *parser.CreateDomain, *parser.AlterType, *parser.CreateCollation, *parser.AlterCollation, *parser.CreateConversion, *parser.AlterConversion:
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
