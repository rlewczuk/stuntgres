package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) executeCreateFunction(scope *StatementScope, s *parser.CreateFunction, count int) (Result, error) {
	result, err := e.executeSchema(scope, scope.op, s, count, scope.input)
	if err == nil {
		scope.completed = true
	}
	return result, err
}

func (e *Engine) stageCreateFunctionLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateFunction, input sqltype.InputEnvironment) error {
	if s.Language.Text != "sql" {
		return expr.ErrorAt("0A000", "only LANGUAGE SQL functions are supported", s.Language.Pos)
	}
	ns, err := e.collationCreationNamespaceLocked(&auth, stage, s.Name)
	if err != nil {
		return err
	}
	if err = e.conversionCheckCreate(op, auth, ns); err != nil {
		return err
	}
	inputs := make([]sqltype.Usage, len(s.Arguments))
	signature := make([]string, len(inputs))
	resolve := func(raw parser.TypeName) (sqltype.Usage, error) {
		if raw.Setof {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "set-returning SQL functions are unsupported", raw.Pos)
		}
		id, err := e.resolveOperatorArgument(op, auth, raw)
		if err != nil {
			return sqltype.Usage{}, err
		}
		if err = e.checkOperatorArgumentUsage(auth, id); err != nil {
			return sqltype.Usage{}, err
		}
		typ, ok := auth.definitionType(sqltype.ID(id))
		if !ok || !typ.Concrete() || typ.BaseType().IsComposite() {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "SQL functions require concrete scalar types", raw.Pos)
		}
		if facts, known := typ.PGTypeMetadata(); known && facts.Kind == "p" {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "SQL function pseudo-types are unsupported", raw.Pos)
		}
		return typ.Usage(), nil
	}
	for i, arg := range s.Arguments {
		if arg.Name.Text != "" || arg.Mode != "" {
			return expr.ErrorAt("0A000", "named and mode function arguments are unsupported", arg.Pos)
		}
		inputs[i], err = resolve(arg.Type)
		if err != nil {
			return err
		}
		signature[i] = strconv.FormatUint(uint64(inputs[i].Type.ID()), 10)
	}
	result, err := resolve(s.Result)
	if err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID}, ObjectNamespace: ns.Address, ObjectName: name, Signature: catalog.ProcedureSignature(strings.Join(signature, ",")), RoutineKind: catalog.FunctionRoutine, Result: ids.OID(result.Type.ID()), Owner: auth.State.CurrentUser, Volatility: expr.Volatile}
	if auth.definitionLookup(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return sqlError("42723", fmt.Sprintf("function %q already exists with same argument types", name))
	}
	definition, err := proc.New(s.Body, s.BodyPos, inputs, result)
	if err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) && diagnostic.Code == "42601" {
			copy := *diagnostic
			// Dollar-quoted bodies retain their source span; parser positions are
			// Unicode characters. Ordinary string bodies use their enclosing span.
			if n, parseErr := strconv.Atoi(copy.Position); parseErr == nil {
				copy.Position = strconv.Itoa(s.BodyPos.Character + 1 + n)
			}
			return &copy
		}
		return err
	}
	d.SQL = definition
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	d.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	d.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	d.ACL = stage.creationACL(d.Owner, d.Namespace(), authorization.DefaultRoutines)
	if err = stage.Create(d); err != nil {
		return err
	}
	if op.replayOrZero().sqlBodies {
		return nil
	}
	previous := op.definition
	op.definition = stageDefinitionReader{stage}
	defer func() { op.definition = previous }()
	auth.definition = op.definition
	_, _, err = e.bindSQLRoutineLocked(op, auth, d, input, nil)
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) && diagnostic.Code == "42P02" {
		copy := *diagnostic
		if n, parseErr := strconv.Atoi(copy.Position); parseErr == nil {
			copy.Position = strconv.Itoa(s.BodyPos.Character + 1 + n)
		}
		copy.Context = ""
		return &copy
	}
	return err
}

// Binding is deliberately producer-free. In particular this does not evaluate
// constants/providers or take a data snapshot while validating CREATE.
func (e *Engine) bindSQLRoutineLocked(op *Operation, auth AuthorizationView, d *catalog.Procedure, input sqltype.InputEnvironment, scope *StatementScope) (analyze.Binding, sqltype.CoercionPlan, error) {
	var admission *analyze.LockAdmission
	var expected *StatementDescription
	if scope != nil {
		admission = &scope.queryLockAdmission
		expected = scope.Expected
	}
	b, err := analyze.Describe(op.Context(), e.analysisHost(op), d.SQL.Query, authorityOf(auth), ids.ObjectRef{}, input, expectedOf(expected), admission, nil, true, d.SQL.Inputs)
	if err != nil {
		return b, sqltype.CoercionPlan{}, exec.SQLRoutineContext(err, d.Name(), false)
	}
	plan, err := validateSQLRoutineResult(b.Query.Root.Schema(), d.SQL.Result)
	return b, plan, exec.SQLRoutineContext(err, d.Name(), false)
}

// The same producer-free contract applies to CREATE/Restore and the actual
// runtime binding before CTE preparation or query opening.
func validateSQLRoutineResult(columns []catalog.Column, result sqltype.Usage) (sqltype.CoercionPlan, error) {
	mismatch := func(detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "return type mismatch in function declared to return " + result.Type.DiagnosticName(), Detail: detail}
	}
	if len(columns) != 1 {
		return sqltype.CoercionPlan{}, mismatch("Final statement must return exactly one column.")
	}
	plan, err := sqltype.PlanCoercion(columns[0].Usage, result, sqltype.Assignment)
	if err != nil {
		return plan, mismatch("Actual return type is " + columns[0].Usage.Type.DiagnosticName() + ".")
	}
	return plan, nil
}
