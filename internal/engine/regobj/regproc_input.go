package regobj

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// RoutineInput uses the same immutable catalog and namespace authority
// as type input. Its function path excludes temporary namespaces.
type RoutineInput struct {
	types    TypeInput
	path     []ids.ObjectAddress
	operands *[]catalog.ExpressionDependency
}

// NewRoutineInput returns the regproc and regprocedure resolver: argument
// types resolve through types, routine names on path.
func NewRoutineInput(types TypeInput, path []ids.ObjectAddress) RoutineInput {
	return RoutineInput{types: types, path: path}
}

func (r RoutineInput) ResolveRegproc(ctx context.Context, text string) (uint32, error) {
	return r.resolveRoutine(ctx, text, false)
}
func (r RoutineInput) ResolveRegprocedure(ctx context.Context, text string) (uint32, error) {
	return r.resolveRoutine(ctx, text, true)
}

func (r RoutineInput) resolveRoutine(ctx context.Context, text string, signatureQualified bool) (uint32, error) {
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
	a := r.types.capture.Authority
	if numeric {
		oid, _ := catalog.DefinitionTypeAt(a.View.Catalog, a.Definitions, sqltype.Oid)
		value, err := oid.Input(ctx, text, -1)
		if err != nil {
			return 0, err
		}
		return value.OID()
	}
	var name parser.ObjectName
	var err error
	var signature catalog.ProcedureSignature
	var exact *catalog.ProcedureSignature
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
		signature = catalog.ProcedureSignature(encoded.String())
		exact = &signature
	} else {
		name, err = parser.ParseRegclassName(text)
		if err != nil {
			err = sqlstate.SoftInputError(sqlError("42602", "invalid name syntax"))
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
		if parts[0].Text != r.types.capture.Database {
			return 0, sqlError("0A000", fmt.Sprintf(`cross-database references are not implemented: "%s"`, catalog.ObjectNameText(name)))
		}
		parts = parts[1:]
	}
	selection := analyze.NamespaceSelection{Path: r.path}
	missing := func() (uint32, error) {
		return 0, sqlstate.SoftInputError(sqlError("42883", fmt.Sprintf(`function "%s" does not exist`, text)))
	}
	if len(parts) == 2 {
		namespace := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.NamespaceClassID, Name: parts[0].Text})
		if parts[0].Text == "pg_temp" {
			namespace = r.types.capture.Temporary
		}
		if namespace == (ids.ObjectAddress{}) {
			return missing()
		}
		if err := a.View.CanUse(a.Environment, a.View.Ref(namespace)); err != nil {
			return 0, err
		}
		if r.types.qualified != nil && !slices.Contains(*r.types.qualified, namespace) {
			*r.types.qualified = append(*r.types.qualified, namespace)
		}
		selection = analyze.NamespaceSelection{Qualified: namespace}
	}
	selected, count := SelectRoutine(a.View.Catalog, selection, parts[len(parts)-1].Text, exact)
	if count == 0 {
		return missing()
	}
	if count > 1 {
		return 0, sqlstate.SoftInputError(sqlError("42725", fmt.Sprintf(`more than one function named "%s"`, text)))
	}
	if r.operands != nil {
		if owner := RoutineDescriptorOwner(a.View.Catalog, selected.ID); owner != nil && owner.Address().ObjectID != selected.ID {
			ref := a.View.Ref(owner.Address())
			for _, operand := range *r.operands {
				if operand.Ref == ref {
					return uint32(selected.ID), nil
				}
			}
			*r.operands = append(*r.operands, catalog.ExpressionDependency{Ref: ref, Revision: owner.Revision()})
		}
	}
	return uint32(selected.ID), nil
}

func (r RoutineInput) RoutineParameter(slot int, empty bool) sqltype.RoutineResolver {
	r.types = r.types.RegtypeParameter(slot, empty).(TypeInput)
	return r
}
func (r RoutineInput) RoutineLiteral(position int) sqltype.RoutineResolver {
	r.types.position = parser.Position{Character: position}
	return r
}
