package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// routineInquiry borrows only this statement's coherent inquiry snapshot.
// Neither target execution rights nor the target's current executable provider
// participate in declaration presentation.
type routineInquiry struct {
	stored.Deparser
	runtime EngineCatalogRuntime
}

func (s *StatementScope) deparseRoutine(c *exec.Context, signature functions.DeparseSignature, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	id, err := value.OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	view := s.Inquiry.CommentCatalog
	d, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(id)})
	if !ok || signature == functions.DeparseFunctionResult && d.RoutineKind == catalog.ProcedureRoutine {
		return sqltype.Null(result), nil
	}
	if signature == functions.DeparseFunction && d.RoutineKind == catalog.AggregateRoutine {
		return sqltype.Value{}, SQLError("42809", fmt.Sprintf("\"%s\" is an aggregate function", d.Name()))
	}
	in := s.deparseInputs()
	r := routineInquiry{Deparser: stored.Deparser{View: view, Path: in.Path, Output: in.Output, NonstandardStrings: in.NonstandardStrings, TemporaryOwner: in.TemporaryOwner, RoutineDeclaration: true}}
	r.runtime = EngineCatalogRuntime{Auth: s.Inquiry.deparseAuth, presentation: stored.Presentation{Deparse: true, Path: in.Path, Output: in.Output, NonstandardStrings: in.NonstandardStrings, TemporaryOwner: in.TemporaryOwner}}
	var text string
	switch signature {
	case functions.DeparseFunctionArguments, functions.DeparseFunctionIdentityArguments:
		text, err = r.arguments(c.Context, d, signature == functions.DeparseFunctionArguments)
	case functions.DeparseFunctionResult:
		text, err = r.result(c.Context, d)
	case functions.DeparseFunction:
		text, err = r.definition(c.Context, d)
	default:
		err = fmt.Errorf("stuntgres: invalid routine inquiry signature")
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, text), nil
}

func (r routineInquiry) typeName(id ids.OID) (string, error) {
	if _, ok := r.View.LookupType(sqltype.ID(id)); !ok {
		return "", fmt.Errorf("stuntgres: missing routine declaration type %d", id)
	}
	return regobj.FormatDeclarationType(r.View, r.Path, sqltype.ID(id), r.Identifier)
}

func (r routineInquiry) arguments(ctx context.Context, d *catalog.Procedure, defaults bool) (string, error) {
	names, err := pgcatalog.ProcedureNameProjection(r.View, d)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	written := 0
	var aggregate *catalog.AggregateDefinition
	if d.Aggregate != nil {
		aggregate = &d.Aggregate.Definition
	} else if d.RoutineKind == catalog.AggregateRoutine {
		for i := range pgcatalog.AggregateDefinitions {
			if pgcatalog.AggregateDefinitions[i].Function == d.Address().ObjectID {
				aggregate = &pgcatalog.AggregateDefinitions[i]
				break
			}
		}
		if aggregate == nil {
			return "", fmt.Errorf("stuntgres: missing aggregate declaration")
		}
	}
	ordered := aggregate != nil && aggregate.Kind != "n"
	err = pgcatalog.VisitProcedureFormals(ctx, d, sqltype.Value{}, func(f pgcatalog.ProcedureFormal) error {
		if f.Mode == "t" {
			return nil
		}
		boundary := ordered && written == int(aggregate.DirectArgs)
		if boundary {
			if written > 0 {
				out.WriteByte(' ')
			}
			out.WriteString("ORDER BY ")
		} else if written > 0 {
			out.WriteString(", ")
		}
		start := out.Len()
		switch f.Mode {
		case "i":
			if d.RoutineKind == catalog.ProcedureRoutine {
				out.WriteString("IN ")
			}
		case "o":
			out.WriteString("OUT ")
		case "b":
			out.WriteString("INOUT ")
		case "v":
			out.WriteString("VARIADIC ")
		default:
			return fmt.Errorf("stuntgres: invalid declaration argument mode")
		}
		if name := names.At(f.Index); name != "" {
			out.WriteString(r.Identifier(name) + " ")
		}
		typ, err := r.typeName(f.Type)
		if err != nil {
			return err
		}
		out.WriteString(typ)
		declarationEnd := out.Len()
		if defaults && f.HasDefault {
			text, err := r.runtime.RoutineDefault(ctx, r.View, d, f.Input)
			if err != nil {
				return err
			}
			out.WriteString(" DEFAULT " + text)
		}
		// Released ordered aggregates repeat the final variadic formal when
		// it is shared by the direct and aggregated argument lists.
		if ordered && f.Mode == "v" && written+1 == int(aggregate.DirectArgs) && f.Input+1 == int(pgcatalog.ProcedureArity(d)) {
			declaration := out.String()[start:declarationEnd]
			out.WriteString(" ORDER BY ")
			out.WriteString(declaration)
		}
		written++
		return nil
	})
	return out.String(), err
}

func (r routineInquiry) result(ctx context.Context, d *catalog.Procedure) (string, error) {
	if d.SQL != nil && d.SQL.TableResult {
		names, err := pgcatalog.ProcedureNameProjection(r.View, d)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		out.WriteString("TABLE(")
		written := 0
		err = pgcatalog.VisitProcedureFormals(ctx, d, sqltype.Value{}, func(f pgcatalog.ProcedureFormal) error {
			if f.Mode != "t" {
				return nil
			}
			if written != 0 {
				out.WriteString(", ")
			}
			typ, err := r.typeName(f.Type)
			if err != nil {
				return err
			}
			out.WriteString(r.Identifier(names.At(f.Index)) + " " + typ)
			written++
			return nil
		})
		out.WriteByte(')')
		return out.String(), err
	}
	text, err := r.typeName(d.Result)
	if err == nil && d.ReturnsSet() {
		text = "SETOF " + text
	}
	return text, err
}

func (r routineInquiry) definition(ctx context.Context, d *catalog.Procedure) (string, error) {
	if !d.Display.Valid() || r.View.Get(d.Address()) != d {
		return "", ids.ErrCatalogReplaced
	}
	namespace, ok := r.View.Namespace(d.Namespace())
	if !ok {
		return "", ids.ErrCatalogReplaced
	}
	args, err := r.arguments(ctx, d, true)
	if err != nil {
		return "", err
	}
	kind, delimiter := "FUNCTION", "$function"
	if d.RoutineKind == catalog.ProcedureRoutine {
		kind, delimiter = "PROCEDURE", "$procedure"
	}
	var out strings.Builder
	out.WriteString("CREATE OR REPLACE " + kind + " " + r.Identifier(namespace.Name()) + "." + r.Identifier(d.Name()) + "(" + args + ")\n")
	if d.RoutineKind != catalog.ProcedureRoutine {
		result, err := r.result(ctx, d)
		if err != nil {
			return "", err
		}
		out.WriteString(" RETURNS " + result + "\n")
	}
	if d.Display.TransformTypes.Len() > 0 {
		out.WriteString(" TRANSFORM ")
		for i := range d.Display.TransformTypes.Len() {
			if i != 0 {
				out.WriteString(", ")
			}
			typ, err := r.typeName(d.Display.TransformTypes.At(i))
			if err != nil {
				return "", err
			}
			out.WriteString("FOR TYPE " + typ)
		}
		out.WriteByte('\n')
	}
	var language string
	address := d.Language
	if address == (ids.ObjectAddress{}) {
		// Native declarations retain released identities, not mutable labels.
		switch d.Display.Language {
		case catalog.InternalRoutineLanguage:
			address = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 12}
		case catalog.CRoutineLanguage:
			address = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 13}
		case catalog.SQLRoutineLanguage:
			address = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 14}
		}
	}
	if address != (ids.ObjectAddress{}) {
		owned, ok := r.View.Get(address).(*catalog.Language)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing routine declaration language")
		}
		if err := (catalog.LanguageProvider{}).Validate(r.View, owned); err != nil {
			return "", err
		}
		language = owned.Name()
	} else if d.Display.Language != catalog.GoRoutineLanguage {
		return "", fmt.Errorf("stuntgres: missing routine declaration language")
	} else {
		language = strings.ToLower(d.Display.Language.String())
	}
	out.WriteString(" LANGUAGE " + r.Identifier(language) + "\n")
	var attributes strings.Builder
	if d.RoutineKind == catalog.WindowRoutine {
		attributes.WriteString(" WINDOW")
	}
	switch d.Volatility {
	case expr.Immutable:
		attributes.WriteString(" IMMUTABLE")
	case expr.Stable:
		attributes.WriteString(" STABLE")
	case expr.Volatile:
	default:
		return "", fmt.Errorf("stuntgres: invalid routine volatility")
	}
	parallel := d.Parallel
	if parallel == 0 {
		parallel = 's'
		if d.Builtin != nil {
			parallel = d.Builtin.Parallel
		}
		if d.Installed.Parallel != 0 {
			parallel = d.Installed.Parallel
		}
		if d.Foreign != nil {
			parallel = 'u'
		}
	}
	switch parallel {
	case 's':
		attributes.WriteString(" PARALLEL SAFE")
	case 'r':
		attributes.WriteString(" PARALLEL RESTRICTED")
	case 'u':
	default:
		return "", fmt.Errorf("stuntgres: invalid routine parallel declaration")
	}
	if d.Strict {
		attributes.WriteString(" STRICT")
	}
	if d.SecurityDefiner {
		attributes.WriteString(" SECURITY DEFINER")
	}
	if d.Display.Leakproof {
		attributes.WriteString(" LEAKPROOF")
	}
	defaultCost := float32(100)
	if d.Display.Language == catalog.InternalRoutineLanguage || d.Display.Language == catalog.CRoutineLanguage {
		defaultCost = 1
	}
	cost := d.CostEstimate()
	if cost != defaultCost {
		attributes.WriteString(" COST " + strconv.FormatFloat(float64(cost), 'g', -1, 32))
	}
	if rows := d.RowEstimate(); rows > 0 && rows != 1000 {
		attributes.WriteString(" ROWS " + strconv.FormatFloat(float64(rows), 'g', -1, 32))
	}
	if d.Display.Support != 0 {
		// A referenced support label can be a released lookup-only identity;
		// it does not manufacture a target descriptor or execution capability.
		support, ok := regobj.RoutineIdentityByOID(r.View, d.Display.Support)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing routine support identity")
		}
		name := r.Identifier(support.Name)
		selected, count := regobj.SelectExactRoutine(r.View, analyze.NamespaceSelection{Path: r.Path}, support.Name, support.Signature)
		if count != 1 || selected.ID != support.ID {
			ns, ok := r.View.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: support.Namespace})
			if !ok {
				return "", ids.ErrCatalogReplaced
			}
			name = r.Identifier(ns.Name()) + "." + name
		}
		attributes.WriteString(" SUPPORT " + name)
	}
	if attributes.Len() > 0 {
		out.WriteString(attributes.String() + "\n")
	}
	for i := range d.Configuration.Len() {
		name, value, ok := strings.Cut(d.Configuration.At(i), "=")
		if !ok {
			return "", fmt.Errorf("stuntgres: invalid routine configuration")
		}
		out.WriteString(" SET " + r.Identifier(name) + " TO ")
		if parser.SettingQuotesIdentifiers(name) {
			list, err := settings.ParseIdentifierList(name, value)
			if err != nil {
				return "", err
			}
			if len(list) == 0 {
				out.WriteString(r.StringLiteral(""))
			}
			for j, item := range list {
				if j != 0 {
					out.WriteString(", ")
				}
				out.WriteString(r.StringLiteral(item))
			}
		} else {
			out.WriteString(r.StringLiteral(value))
		}
		out.WriteByte('\n')
	}
	if d.SQL != nil && d.SQL.Standard {
		program, err := stored.RoutineProgramOf(d)
		if err != nil {
			return "", err
		}
		body, err := program.DefinitionSQL(ctx, r.View, d, r.runtime.presentation)
		if err != nil {
			return "", err
		}
		out.WriteString(body)
	} else if d.DeclarationBody != nil {
		body, err := stored.RoutineDeclarationSQL(ctx, r.View, d, r.runtime.presentation)
		if err != nil {
			return "", err
		}
		out.WriteString(body)
	} else {
		source := d.Display.Source
		for strings.Contains(source, delimiter) {
			delimiter += "x"
		}
		delimiter += "$"
		out.WriteString("AS ")
		if d.Display.HasBinary {
			out.WriteString(r.StringLiteral(d.Display.Binary) + ", ")
		}
		out.WriteString(delimiter + source + delimiter + "\n")
	}
	return out.String(), ctx.Err()
}
