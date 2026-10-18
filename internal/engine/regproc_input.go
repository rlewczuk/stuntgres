package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// capturedRoutineInput uses the same immutable catalog and namespace authority
// as type input. Its function path excludes temporary namespaces.
type capturedRoutineInput struct {
	types    capturedRegtypeInput
	path     []ids.ObjectAddress
	operands *[]expressionDependency
}

func (e *Engine) captureRoutineInput(auth AuthorizationView, types capturedRegtypeInput) (capturedRoutineInput, error) {
	path, err := e.lookupPath(auth, functionLookup)
	if err != nil {
		return capturedRoutineInput{}, err
	}
	return capturedRoutineInput{types: types, path: path}, nil
}

func (r capturedRoutineInput) ResolveRegproc(ctx context.Context, text string) (uint32, error) {
	return r.resolveRoutine(ctx, text, false)
}
func (r capturedRoutineInput) ResolveRegprocedure(ctx context.Context, text string) (uint32, error) {
	return r.resolveRoutine(ctx, text, true)
}

func (r capturedRoutineInput) resolveRoutine(ctx context.Context, text string, signatureQualified bool) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if text == "-" {
		return 0, nil
	}
	numeric := len(text) != 0
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			numeric = false
			break
		}
	}
	if numeric {
		oid, _ := r.types.auth.definitionType(sqltype.Oid)
		value, err := oid.Input(ctx, text, -1)
		if err != nil {
			return 0, err
		}
		return value.OID()
	}
	var name parser.ObjectName
	var err error
	var signature procedureSignature
	var exact *procedureSignature
	if signatureQualified {
		var encoded strings.Builder
		name, err = parser.ParseRegprocedureName(text, func(argument string) error {
			id, err := r.types.resolveTypeName(ctx, argument)
			if err != nil {
				return err
			}
			if encoded.Len() != 0 {
				encoded.WriteByte(',')
			}
			encoded.WriteString(strconv.FormatUint(uint64(id), 10))
			return nil
		})
		signature = procedureSignature(encoded.String())
		exact = &signature
	} else {
		name, err = parser.ParseRegclassName(text)
		if err != nil {
			err = sqlError("42602", "invalid name syntax")
		}
	}
	if err != nil {
		return 0, err
	}
	parts := name.Parts
	if len(parts) > 3 {
		return 0, sqlError("42601", "improper qualified name (too many dotted names): "+text)
	}
	if len(parts) == 3 {
		if parts[0].Text != r.types.database {
			return 0, sqlError("0A000", fmt.Sprintf(`cross-database references are not implemented: "%s"`, objectNameText(name)))
		}
		parts = parts[1:]
	}
	selection := namespaceSelection{path: r.path}
	missing := func() (uint32, error) { return 0, sqlError("42883", fmt.Sprintf(`function "%s" does not exist`, text)) }
	if len(parts) == 2 {
		namespace := r.types.auth.definitionLookup(ids.NameKey{ClassID: NamespaceClassID, Name: parts[0].Text})
		if parts[0].Text == "pg_temp" {
			namespace = r.types.temporary
		}
		if namespace == (ids.ObjectAddress{}) {
			return missing()
		}
		if err := r.types.auth.CanUse(r.types.auth.engine.domain.Ref(r.types.auth.catalog.Epoch(), namespace)); err != nil {
			return 0, err
		}
		if r.types.qualified != nil && !slices.Contains(*r.types.qualified, namespace) {
			*r.types.qualified = append(*r.types.qualified, namespace)
		}
		selection = namespaceSelection{qualified: namespace}
	}
	selected, count := selectRoutine(r.types.auth.catalog, selection, parts[len(parts)-1].Text, exact)
	if count == 0 {
		return missing()
	}
	if count > 1 {
		return 0, sqlError("42725", fmt.Sprintf(`more than one function named "%s"`, text))
	}
	if r.operands != nil {
		if owner := routineDescriptorOwner(r.types.auth.catalog, selected.id); owner != nil && owner.address.ObjectID != selected.id {
			ref := r.types.auth.engine.domain.Ref(r.types.auth.catalog.Epoch(), owner.address)
			for _, operand := range *r.operands {
				if operand.ref == ref {
					return uint32(selected.id), nil
				}
			}
			*r.operands = append(*r.operands, expressionDependency{ref: ref, revision: owner.revision})
		}
	}
	return uint32(selected.id), nil
}

func (r capturedRoutineInput) RoutineParameter(slot int, empty bool) sqltype.RoutineResolver {
	r.types = r.types.RegtypeParameter(slot, empty).(capturedRegtypeInput)
	return r
}
func (r capturedRoutineInput) RoutineLiteral(position int) sqltype.RoutineResolver {
	r.types.position = parser.Position{Character: position}
	return r
}
