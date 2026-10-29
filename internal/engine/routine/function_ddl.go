package routine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// StageCreateFunction stages CREATE FUNCTION ... LANGUAGE SQL and binds its
// body against the stage, except during Restore SQL body replay.
func StageCreateFunction(h DDLHost, auth ddl.Authority, s *parser.CreateFunction, input sqltype.InputEnvironment) error {
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return err
	}
	language, execution, err := admitFunctionLanguage(h, auth, s)
	if err != nil {
		return err
	}
	bodyKind := proc.SQLBody
	if execution == catalog.PLpgSQLLanguage {
		bodyKind = proc.PLpgSQLBody
	}
	arguments := s.Arguments
	if s.TableResult != nil {
		arguments = append(append([]parser.RoutineParameter(nil), arguments...), s.TableResult...)
	}
	formals := make([]proc.Formal, len(arguments))
	resolve := func(raw parser.TypeName, result bool) (sqltype.Usage, error) {
		raw.Setof = false
		id, err := h.ResolveRegtype(auth, raw)
		if err != nil {
			var diagnostic *sqlstate.Error
			if !s.Procedure && errors.As(err, &diagnostic) && diagnostic.Code == "42704" {
				// Parsed declaration types have SQL source coordinates and use
				// declaration spelling, not regtype text-input diagnostics.
				return sqltype.Usage{}, expr.ErrorAt("42704", "type "+writtenRoutineTypeName(raw)+" does not exist", raw.Pos)
			}
			return sqltype.Usage{}, err
		}
		if err = checkOperatorArgumentUsage(h, auth, id); err != nil {
			return sqltype.Usage{}, err
		}
		typ, ok := h.DefinitionType(auth, sqltype.ID(id))
		if bodyKind != proc.SQLBody && !s.Procedure && typ != nil && typ.BaseType().IsComposite() && !(result && bodyKind == proc.PLpgSQLBody && s.Result.Setof) {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "SQL functions require concrete scalar types", raw.Pos)
		}
		if !ok || !typ.Concrete() && !sqltype.IsPseudo(typ.ID()) {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "SQL functions require concrete types", raw.Pos)
		}
		return typ.Usage(), nil
	}
	for i, arg := range arguments {
		if s.Procedure && arg.Type.Setof {
			return expr.ErrorAt("42P13", "procedures cannot accept set arguments", arg.Pos)
		}
		mode := "i"
		switch arg.Mode {
		case "out":
			mode = "o"
		case "inout":
			mode = "b"
		case "variadic":
			mode = "v"
		case "table":
			mode = "t"
		}
		usage, failure := resolve(arg.Type, false)
		if failure != nil {
			return failure
		}
		formals[i] = proc.Formal{Mode: mode, Name: arg.Name.Text, Usage: usage, Position: arg.Pos, HasDefault: arg.Default != nil}
	}
	layout := &proc.Definition{Procedure: s.Procedure}
	if err = layout.SetFormals(formals); err != nil {
		return err
	}
	inputs, names := layout.Inputs, layout.Names
	signature := make([]string, len(inputs))
	for i, input := range inputs {
		signature[i] = strconv.FormatUint(uint64(input.Type.ID()), 10)
	}
	resultSyntax := s.Result
	var result sqltype.Usage
	if s.Procedure && len(layout.OutputFormals) == 0 {
		resultSyntax = parser.TypeName{Name: "void"}
	} else if len(layout.OutputFormals) > 0 {
		if layout.DerivedResult() {
			if s.ResultSpecified && s.TableResult == nil {
				explicit := resultSyntax
				explicit.Setof = false
				id, failure := h.ResolveRegtype(auth, explicit)
				if failure != nil {
					return failure
				}
				if id != 2249 {
					return sqlError("42P13", "function result type must be record because of OUT parameters")
				}
				if failure := checkOperatorArgumentUsage(h, auth, id); failure != nil {
					return failure
				}
			}
			shape, failure := layout.OutputTuple()
			if failure != nil {
				return failure
			}
			result = shape.Usage()
		} else {
			result = formals[layout.OutputFormals[0]].Usage
			if s.ResultSpecified && s.TableResult == nil {
				explicit, failure := resolve(resultSyntax, true)
				if failure != nil {
					return failure
				}
				if explicit.Type != result.Type {
					return sqlError("42P13", "function result type must be "+result.Type.DiagnosticName()+" because of OUT parameters")
				}
			}
		}
	} else if !s.ResultSpecified {
		return sqlError("42P13", "function result type must be specified")
	}
	if result.Type == nil {
		result, err = resolve(resultSyntax, true)
		if err != nil {
			return err
		}
	}
	layout.Result, layout.ReturnsSet, layout.TableResult = result, s.Result.Setof, s.TableResult != nil
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID}, ObjectNamespace: ns.Address, ObjectName: name, Signature: catalog.ProcedureSignature(strings.Join(signature, ",")), RoutineKind: catalog.FunctionRoutine, Result: ids.OID(result.Type.ID()), Owner: h.AccessView(auth).State.CurrentUser, Volatility: expr.Volatile}
	d.Parallel, d.Cost = 'u', 100
	if layout.ReturnsSet {
		d.Rows = 1000
	}
	d.Language = language.Address()
	if s.Procedure {
		d.RoutineKind = catalog.ProcedureRoutine
	}
	outputs := []sqltype.Usage{result}
	for _, index := range layout.OutputFormals {
		outputs = append(outputs, formals[index].Usage)
	}
	if err = proc.ValidatePseudoResultSafety(inputs, outputs); err != nil {
		return err
	}
	var previous *catalog.Procedure
	if address := h.DefinitionLookup(auth, catalog.DescriptorNameKey(d)); address != (ids.ObjectAddress{}) {
		if !s.Replace {
			return sqlError("42723", fmt.Sprintf("function %q already exists with same argument types", name))
		}
		previous, _ = h.AccessView(auth).Catalog.Get(address).(*catalog.Procedure)
		if previous == nil {
			return fmt.Errorf("stuntgres: existing function definition has no descriptor")
		}
		if err := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, address), OwnerOnly: true}); err != nil {
			return err
		}
		if previous.RoutineKind != d.RoutineKind {
			noun := "function"
			if previous.RoutineKind == catalog.ProcedureRoutine {
				noun = "procedure"
			} else if previous.RoutineKind == catalog.AggregateRoutine {
				noun = "aggregate function"
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot change routine kind", Detail: fmt.Sprintf("%q is a %s.", previous.Name(), noun)}
		}
		if err := checkFunctionReplacement(h, auth, previous, d.Result, layout); err != nil {
			return err
		}
		d.ObjectAddress, d.Owner, d.ACL, d.Extension = previous.Address(), previous.Owner, previous.ACL, previous.Extension
	}
	if err = normalizeRoutineAttributes(h, auth, d, s.Attributes, layout.ReturnsSet); err != nil {
		return err
	}
	if err = h.ValidateRoutineConfiguration(d); err != nil {
		return err
	}
	// Language validation follows replacement authority and compatibility.
	// Catalog result safety above remains unconditional.
	if bodyKind == proc.PLpgSQLBody {
		if language.Validator != (ids.ObjectAddress{}) {
			if err = proc.ValidatePLSignature(inputs, outputs, formals, name, s.Procedure); err != nil {
				return err
			}
		}
	} else if err = proc.ValidatePseudoLanguageSignature(inputs, outputs, formals, bodyKind, s.Standard != nil); err != nil {
		return err
	}
	var definition *proc.Definition
	if bodyKind == proc.PLpgSQLBody {
		var syntax *parser.PLBlock
		if language.Validator != (ids.ObjectAddress{}) {
			syntax, err = parser.ParsePLpgSQL(s.Body, proc.PLReturnPolicy(result, len(layout.OutputFormals), s.Procedure))
		}
		invocation := proc.OrdinaryInvocation
		if result.Type.ID() == 2279 {
			invocation = proc.DMLTriggerInvocation
		}
		if result.Type.ID() == 3838 {
			invocation = proc.EventTriggerInvocation
		}
		definition = &proc.Definition{Kind: proc.PLpgSQLBody, Invocation: invocation, Source: s.Body, Position: s.BodyPos, Inputs: inputs, Result: result, PLSyntax: syntax}
	} else {
		definition, err = proc.New(s.Body, s.BodyPos, inputs, result)
	}
	if err != nil && bodyKind == proc.PLpgSQLBody {
		return parser.PLBodyError(err, s.BodyPos)
	}
	if err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) && (diagnostic.Code == "42601" || diagnostic.Code == "42P02") {
			copy := *diagnostic
			// BodyPos retains the actual quote delimiter's content origin.
			if n, parseErr := strconv.Atoi(copy.Position); parseErr == nil {
				copy.Position = strconv.Itoa(s.BodyPos.Character + n)
			}
			return &copy
		}
		return err
	}
	d.SQL = definition
	d.Display = catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage, Source: definition.Source}
	if bodyKind == proc.PLpgSQLBody {
		d.Display.Language = catalog.PLpgSQLRoutineLanguage
	}
	if s.Standard != nil {
		d.Display.Source = ""
	}
	definition.Name, definition.Names = name, names
	definition.Procedure = s.Procedure
	definition.Formals, definition.InputFormals = layout.Formals, layout.InputFormals
	definition.OutputFormals, definition.FormalInputs = layout.OutputFormals, layout.FormalInputs
	definition.ReturnsSet, definition.TableResult = s.Result.Setof, s.TableResult != nil
	if s.Standard != nil {
		definition.Standard, definition.Statements = true, s.Standard
		definition.StandardReturn = s.StandardReturn
	}
	if previous == nil {
		oid, allocationErr := h.AllocateOID()
		if allocationErr != nil {
			return allocationErr
		}
		d.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	}
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if previous == nil {
		d.ACL = h.CreationACL(d.Owner, d.Namespace(), authorization.DefaultRoutines)
	}
	// Defaults see the legitimate pre-declaration stage. The new declaration
	// becomes visible only afterwards, including to its own body validation.
	if err = h.BindSQLDefaults(auth, d, arguments, input); err != nil {
		return err
	}
	if previous == nil {
		err = h.Stage().Create(d)
	} else {
		err = h.Stage().Replace(d)
	}
	if err != nil {
		return err
	}
	if !s.Procedure {
		ddl.RecordCommand(h.Stage(), "CREATE FUNCTION", d.Address())
	}
	err = h.BindSQLRoutine(auth, d, input)
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) && diagnostic.Code == "42P02" && !d.SQL.Standard {
		copy := *diagnostic
		if n, parseErr := strconv.Atoi(copy.Position); parseErr == nil {
			copy.Position = strconv.Itoa(s.BodyPos.Character + n)
		}
		copy.Context = ""
		return &copy
	}
	return err
}
