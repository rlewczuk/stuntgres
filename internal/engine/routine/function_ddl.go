package routine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

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
	if err = admitFunctionLanguage(h, auth, s); err != nil {
		return err
	}
	formals := make([]proc.Formal, len(s.Arguments))
	resolve := func(raw parser.TypeName, result bool) (sqltype.Usage, error) {
		if raw.Setof {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "set-returning SQL functions are unsupported", raw.Pos)
		}
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
		if result && typ != nil && typ.ID() == 2279 && s.Language.Text == "plpgsql" && !s.Procedure {
			if len(s.Arguments) != 0 {
				return sqltype.Usage{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "trigger functions cannot have declared arguments", Hint: "The arguments of the trigger can be accessed through TG_NARGS and TG_ARGV instead.", Context: fmt.Sprintf("compilation of PL/pgSQL function %q near line 1", s.Name.Parts[len(s.Name.Parts)-1].Text)}
			}
			return typ.Usage(), nil
		}
		if !ok || !typ.Concrete() || typ.BaseType().IsComposite() {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "SQL functions require concrete scalar types", raw.Pos)
		}
		if facts, known := typ.PGTypeMetadata(); known && facts.Kind == "p" && !(result && typ.ID() == sqltype.Void) {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "SQL function pseudo-types are unsupported", raw.Pos)
		}
		return typ.Usage(), nil
	}
	for i, arg := range s.Arguments {
		if s.Procedure && (arg.Mode != "" && arg.Mode != "in" || arg.Default != nil) {
			return expr.ErrorAt("0A000", "procedure argument modes and defaults are unsupported", arg.Pos)
		}
		mode := "i"
		switch arg.Mode {
		case "out":
			mode = "o"
		case "inout":
			mode = "b"
		case "variadic":
			mode = "v"
		}
		usage, failure := resolve(arg.Type, false)
		if failure != nil {
			return failure
		}
		formals[i] = proc.Formal{Mode: mode, Name: arg.Name.Text, Usage: usage, Position: arg.Pos, HasDefault: arg.Default != nil}
	}
	layout := &proc.Definition{}
	if err = layout.SetFormals(formals); err != nil {
		return err
	}
	inputs, names := layout.Inputs, layout.Names
	signature := make([]string, len(inputs))
	for i, input := range inputs {
		signature[i] = strconv.FormatUint(uint64(input.Type.ID()), 10)
	}
	if !s.Procedure && len(layout.OutputFormals) > 1 {
		return sqlError("0A000", "multiple-output SQL function execution is unsupported")
	}
	resultSyntax := s.Result
	if s.Procedure {
		resultSyntax = parser.TypeName{Name: "void"}
	} else if !s.ResultSpecified {
		if len(layout.OutputFormals) == 0 {
			return sqlError("42P13", "function result type must be specified")
		}
		resultSyntax = s.Arguments[layout.OutputFormals[0]].Type
	}
	result, err := resolve(resultSyntax, true)
	if err != nil {
		return err
	}
	if !s.Procedure && len(layout.OutputFormals) == 1 && result.Type != formals[layout.OutputFormals[0]].Usage.Type {
		return sqlError("42P13", "function result type must be "+formals[layout.OutputFormals[0]].Usage.Type.DiagnosticName()+" because of OUT parameters")
	}
	if err = checkRoutineBodyForm(s); err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID}, ObjectNamespace: ns.Address, ObjectName: name, Signature: catalog.ProcedureSignature(strings.Join(signature, ",")), RoutineKind: catalog.FunctionRoutine, Result: ids.OID(result.Type.ID()), Owner: h.AccessView(auth).State.CurrentUser, Volatility: expr.Volatile}
	d.Language = h.AccessView(auth).Catalog.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: s.Language.Text})
	if s.Procedure {
		d.RoutineKind = catalog.ProcedureRoutine
	}
	if h.DefinitionLookup(auth, catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return sqlError("42723", fmt.Sprintf("function %q already exists with same argument types", name))
	}
	var definition *proc.Definition
	if s.Language.Text == "plpgsql" {
		var syntax *parser.PLBlock
		syntax, err = parser.ParsePLpgSQL(s.Body, proc.PLReturnPolicy(result, len(layout.OutputFormals), s.Procedure))
		definition = &proc.Definition{Kind: proc.PLpgSQLBody, Trigger: result.Type.ID() == 2279, Source: s.Body, Position: s.BodyPos, Inputs: inputs, Result: result, PLSyntax: syntax}
	} else {
		definition, err = proc.New(s.Body, s.BodyPos, inputs, result)
	}
	if err != nil && s.Language.Text == "plpgsql" {
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
	definition.Name, definition.Names = name, names
	definition.Formals, definition.InputFormals = layout.Formals, layout.InputFormals
	definition.OutputFormals, definition.FormalInputs = layout.OutputFormals, layout.FormalInputs
	if s.Standard != nil {
		definition.Standard, definition.Statements = true, s.Standard
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	d.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	d.ACL = h.CreationACL(d.Owner, d.Namespace(), authorization.DefaultRoutines)
	// Defaults see the legitimate pre-declaration stage. The new declaration
	// becomes visible only afterwards, including to its own body validation.
	if err = h.BindSQLDefaults(auth, d, s.Arguments, input); err != nil {
		return err
	}
	if err = h.Stage().Create(d); err != nil {
		return err
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
