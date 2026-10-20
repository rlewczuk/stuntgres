package routine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// StageCreateFunction stages CREATE FUNCTION ... LANGUAGE SQL and binds its
// body against the stage, except during Restore SQL body replay.
func StageCreateFunction(h DDLHost, auth Authority, s *parser.CreateFunction, input sqltype.InputEnvironment) error {
	if s.Language.Text != "sql" {
		return expr.ErrorAt("0A000", "only LANGUAGE SQL functions are supported", s.Language.Pos)
	}
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return err
	}
	inputs := make([]sqltype.Usage, len(s.Arguments))
	signature := make([]string, len(inputs))
	resolve := func(raw parser.TypeName) (sqltype.Usage, error) {
		if raw.Setof {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "set-returning SQL functions are unsupported", raw.Pos)
		}
		id, err := h.ResolveType(auth, raw)
		if err != nil {
			return sqltype.Usage{}, err
		}
		if err = checkOperatorArgumentUsage(h, auth, id); err != nil {
			return sqltype.Usage{}, err
		}
		typ, ok := h.DefinitionType(auth, sqltype.ID(id))
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
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID}, ObjectNamespace: ns.Address, ObjectName: name, Signature: catalog.ProcedureSignature(strings.Join(signature, ",")), RoutineKind: catalog.FunctionRoutine, Result: ids.OID(result.Type.ID()), Owner: h.AccessView(auth).State.CurrentUser, Volatility: expr.Volatile}
	if h.DefinitionLookup(auth, catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
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
	if err = h.Stage().Create(d); err != nil {
		return err
	}
	err = h.BindSQLRoutine(auth, d, input)
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
