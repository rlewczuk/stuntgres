package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) resolveGrantRoutine(op *Operation, v AuthorizationView, target parser.GrantRoutineTarget, kind string) (ids.ObjectRef, error) {
	path, err := e.lookupNamespaces(v, target.Name, functionLookup)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	var signature procedureSignature
	var exact *procedureSignature
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
		signature = procedureSignature(encoded.String())
		exact = &signature
	}
	name := target.Name.Parts[len(target.Name.Parts)-1].Text
	var selected routineIdentity
	var count int
	if !target.SignatureSpecified && kind == "procedure" {
		// With no argument list PostgreSQL filters by SQL kind before deciding
		// uniqueness. Native currently has no user-procedure creator/runtime.
		for i := range path.count() {
			for _, descriptor := range v.catalog.Descriptors() {
				p, ok := descriptor.(*procedureDescriptor)
				if ok && p.namespace == path.at(i) && p.name == name && p.routineKind == procedureRoutine {
					selected = routineIdentity{p.address.ObjectID, p.namespace.ObjectID, p.name, p.signature}
					count++
				}
			}
		}
	} else {
		selected, count = selectRoutine(v.catalog, path, name, exact)
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
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: selected.id}
	p, ok := v.catalog.Procedure(address)
	if !ok {
		// Lookup-only inventory cannot manufacture an owning ACL descriptor or
		// an execution capability.
		return ids.ObjectRef{}, sqlError("0A000", "routine privileges are unsupported for unimplemented routines in embedded mode")
	}
	if !grantRoutineKindMatches(kind, p.routineKind) {
		return ids.ObjectRef{}, sqlError("42809", display+"("+grantRoutineArgumentDisplay(v, inputTypes[:inputCount])+") is not a "+noun)
	}
	return e.catalog.domain.Ref(v.catalog.Epoch(), address), nil
}

func grantRoutineArgumentDisplay(v AuthorizationView, inputs []ids.OID) string {
	path, _ := v.engine.referencePath(v)
	var display strings.Builder
	for i, id := range inputs {
		if i != 0 {
			display.WriteString(", ")
		}
		typ := v.catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
		if typ.typ.IsUserDefined() {
			display.WriteString(typeDiagnosticNameAt(v.catalog, typ, path))
		} else {
			display.WriteString(typ.typ.DiagnosticName())
		}
	}
	return display.String()
}
