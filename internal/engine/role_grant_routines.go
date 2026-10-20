package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) resolveGrantRoutine(op *Operation, v AuthorizationView, target parser.GrantRoutineTarget, kind string) (ids.ObjectRef, error) {
	path, err := e.lookupNamespaces(v, target.Name, analyze.FunctionLookup)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	var signature catalog.ProcedureSignature
	var exact *catalog.ProcedureSignature
	var inputTypes [100]ids.OID
	inputCount := 0
	if target.SignatureSpecified {
		types, err := e.captureRegtypeInput(v, op)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		var encoded strings.Builder
		for _, argument := range target.Arguments {
			if argument.Mode == "out" {
				continue
			}
			if err := op.Context().Err(); err != nil {
				return ids.ObjectRef{}, err
			}
			inputCount++
			if inputCount > len(inputTypes) {
				noun := "functions"
				if kind == "procedure" {
					noun = "procedures"
				}
				return ids.ObjectRef{}, sqlError("54023", noun+" cannot have more than 100 arguments")
			}
			id, err := types.resolveParsedTypeName(op.Context(), argument.Type, op)
			if err != nil {
				return ids.ObjectRef{}, err
			}
			if encoded.Len() != 0 {
				encoded.WriteByte(',')
			}
			var digits [20]byte
			encoded.Write(strconv.AppendUint(digits[:0], uint64(id), 10))
			inputTypes[inputCount-1] = ids.OID(id)
		}
		signature = catalog.ProcedureSignature(encoded.String())
		exact = &signature
	}
	name := target.Name.Parts[len(target.Name.Parts)-1].Text
	var selected catalog.RoutineIdentity
	var count int
	if !target.SignatureSpecified && kind == "procedure" {
		// With no argument list PostgreSQL filters by SQL kind before deciding
		// uniqueness. Native currently has no user-procedure creator/runtime.
		for i := range path.Count() {
			for _, descriptor := range v.Catalog.Descriptors() {
				p, ok := descriptor.(*catalog.Procedure)
				if ok && p.Namespace() == path.At(i) && p.Name() == name && p.RoutineKind == catalog.ProcedureRoutine {
					selected = catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}
					count++
				}
			}
		}
	} else {
		selected, count = selectRoutine(v.Catalog, path, name, exact)
	}
	noun := "function"
	if kind == "procedure" {
		noun = "procedure"
	}
	display := objectNameText(target.Name)
	if count == 0 {
		if !target.SignatureSpecified {
			return ids.ObjectRef{}, sqlError("42883", fmt.Sprintf(`could not find a %s named "%s"`, noun, display))
		}
		display += "(" + grantRoutineArgumentDisplay(v, inputTypes[:inputCount]) + ")"
		return ids.ObjectRef{}, sqlError("42883", noun+" "+display+" does not exist")
	}
	if count > 1 {
		diagnostic := sqlError("42725", fmt.Sprintf(`%s name "%s" is not unique`, kind, display)).(*sqlstate.Error)
		if !target.SignatureSpecified {
			diagnostic.Hint = "Specify the argument list to select the " + kind + " unambiguously."
		}
		return ids.ObjectRef{}, diagnostic
	}
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: selected.ID}
	p, ok := v.Catalog.Procedure(address)
	if !ok {
		// Lookup-only inventory cannot manufacture an owning ACL descriptor or
		// an execution capability.
		return ids.ObjectRef{}, sqlError("0A000", "routine privileges are unsupported for unimplemented routines in embedded mode")
	}
	if !routine.GrantRoutineKindMatches(kind, p.RoutineKind) {
		return ids.ObjectRef{}, sqlError("42809", display+"("+grantRoutineArgumentDisplay(v, inputTypes[:inputCount])+") is not a "+noun)
	}
	return e.catalog.Domain.Ref(v.Catalog.Epoch(), address), nil
}

func grantRoutineArgumentDisplay(v AuthorizationView, inputs []ids.OID) string {
	path, _ := v.engine.referencePath(v)
	var display strings.Builder
	for i, id := range inputs {
		if i != 0 {
			display.WriteString(", ")
		}
		typ := v.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
		if typ.SQLType.IsUserDefined() {
			display.WriteString(catalog.TypeDiagnosticNameAt(v.Catalog, typ, path))
		} else {
			display.WriteString(typ.SQLType.DiagnosticName())
		}
	}
	return display.String()
}
