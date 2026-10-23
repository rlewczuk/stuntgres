package regobj

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Catalog rendering resolves the captured namespace, including renamed schemas;
// the immutable codec never consults a mutable engine to format a domain name.
func TypeDeclaration(view catalog.Read, u sqltype.Usage) (string, error) {
	return TypeDeclarationAt(view, nil, u)
}

func TypeDeclarationAt(view catalog.Read, reader catalog.DefinitionReader, u sqltype.Usage) (string, error) {
	if u.Type.IsArray() {
		array, ok := catalog.DefinitionArrayAt(view, reader, u.Type.Element())
		if !ok || array != u.Type || !u.Valid() {
			return "", fmt.Errorf("stuntgres: invalid array declaration authority")
		}
		element := u
		element.Type = u.Type.Element()
		declaration, err := TypeDeclarationAt(view, reader, element)
		if err != nil {
			return "", err
		}
		return declaration + "[]", nil
	}
	if !u.Type.IsUserDefined() && !(u.Type.IsDomain() && u.Type.Concrete()) {
		return u.Declaration()
	}
	descriptor, ok := catalog.DescriptorAt(view, reader, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})
	if !ok || descriptor.(*catalog.Type).SQLType != u.Type || !u.Valid() {
		return "", fmt.Errorf("stuntgres: invalid domain declaration authority")
	}
	namespace, ok := catalog.DescriptorAt(view, reader, descriptor.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing domain namespace")
	}
	return catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(descriptor.Name()), nil
}

// Column declarations retain syntactic rank separately from array type identity.
// A copied rank-zero array uses its catalog name, without adding bracket syntax.
func ColumnDeclaration(view catalog.Read, column catalog.Column) (string, error) {
	declaration, err := TypeDeclaration(view, column.Usage)
	if err != nil || !column.Usage.Type.IsArray() {
		return declaration, err
	}
	if column.DeclaredDimensions > 0 {
		return declaration + strings.Repeat("[]", int(column.DeclaredDimensions)-1), nil
	}
	descriptor, ok := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(column.Usage.Type.ID())})
	if !ok {
		return "", fmt.Errorf("stuntgres: missing array declaration identity")
	}
	namespace, ok := view.Descriptor(descriptor.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing array declaration namespace")
	}
	alias := catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(descriptor.Name())
	if column.Usage.Typmod >= 0 {
		if column.Usage.Type.Element().ID() == sqltype.Interval {
			rangeMask, precision := column.Usage.Typmod>>16, column.Usage.Typmod&0xffff
			alias += "(" + strconv.FormatInt(int64(rangeMask), 10)
			if precision != sqltype.IntervalFullPrecision {
				alias += "," + strconv.FormatInt(int64(precision), 10)
			}
			alias += ")"
		} else {
			start, end := strings.IndexByte(declaration, '('), strings.LastIndexByte(declaration, ')')
			if start < 0 || end < start {
				return "", fmt.Errorf("stuntgres: missing array declaration modifier")
			}
			alias += declaration[start : end+1]
		}
	}
	return alias, nil
}

func Literal(ctx context.Context, view catalog.Read, n plan.LiteralExpr) (string, error) {
	return LiteralWithACL(ctx, view, n, ACLItemContextFormatter(ctx))
}

func LiteralWithACL(ctx context.Context, view catalog.Read, n plan.LiteralExpr, aclOutput sqltype.ACLItemFormatter) (string, error) {
	return LiteralWithACLAt(ctx, view, nil, n, aclOutput)
}

func LiteralWithACLAt(ctx context.Context, view catalog.Read, reader catalog.DefinitionReader, n plan.LiteralExpr, aclOutput sqltype.ACLItemFormatter) (string, error) {
	bounded, _ := ctx.Value(constructorBoundsKey{}).(bool)
	return literalWithACLAt(ctx, view, reader, n, aclOutput, bounded)
}

// Bounded input authority addresses this value only. Domain unwrapping keeps
// that address; container members always start without it.
func literalWithACLAt(ctx context.Context, view catalog.Read, reader catalog.DefinitionReader, n plan.LiteralExpr, aclOutput sqltype.ACLItemFormatter, bounded bool) (string, error) {
	u := n.ResultUsage()
	if n.Value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
	}
	if u.Type.IsEnum() || u.Type.IsQueryInt() || u.Type.IsCube() || u.Type.IsCitext() {
		declaration, err := TypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		literal, err := u.Type.SQLLiteral(ctx, n.Value)
		if err != nil {
			return "", err
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if u.Type.IsDomain() {
		declaration, err := TypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		value, err := n.Value.BaseValue()
		if err != nil {
			return "", err
		}
		n.Usage = u.BaseUsage()
		n.Value = value
		literal, err := literalWithACLAt(ctx, view, reader, n, aclOutput, bounded)
		if err != nil {
			return "", err
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if ExactFloat4Dump(ctx) && NeedsFloat4RawReplay(n.Value) || n.Value.RequiresCircleConstructorReplay() && (u.Type.IsArray() || u.Type.IsComposite()) {
		return constructorLiteral(ctx, view, reader, n, aclOutput, bounded)
	}
	if !n.Value.IsNull() && u.Type.ID() == sqltype.ACLItemArray && n.Value.ArrayRank() == 1 && n.Value.ArrayDimension(0) == (sqltype.ArrayDimension{Lower: 1, Length: 0}) {
		return `pg_catalog.acldefault('c'::"char",0::oid)`, nil
	}
	// Composite text cannot reconstruct empty ACL shape.
	if !n.Value.IsNull() && u.Type.IsComposite() && ContainsEmptyACL(n.Value) {
		declaration, err := TypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		parts := make([]string, n.Value.CompositeLen())
		for i := range parts {
			member := n.Value.CompositeField(i)
			literal := plan.LiteralExpr{Info: expr.Info{Usage: u.Type.CompositeMember(i).Usage}, Value: member}
			parts[i], err = literalWithACLAt(ctx, view, reader, literal, aclOutput, false)
			if err != nil {
				return "", err
			}
		}
		return "CAST(ROW(" + strings.Join(parts, ", ") + ") AS " + declaration + ")", nil
	}
	if !n.Value.IsNull() && u.Type.IsArray() && u.Type.ID() != sqltype.ACLItemArray && ContainsEmptyACL(n.Value) {
		return "", fmt.Errorf("stuntgres: dump cannot serialize rank-one empty ACL inside an array of composite carriers")
	}
	if sqltype.IsRoutineAlias(u.Type.ID()) && !n.Value.IsNull() {
		return RoutineLiteral(view, n.Value)
	}
	if sqltype.IsOperatorAlias(u.Type.ID()) && !n.Value.IsNull() {
		return OperatorLiteral(view, n.Value)
	}
	if u.Type.IsArray() || u.Type.IsComposite() {
		declaration, err := TypeDeclarationAt(view, reader, u)
		if err != nil {
			return "", err
		}
		if (u.Type.ContainsRegproc() || u.Type.ContainsOperatorAlias()) && !n.Value.IsNull() {
			return routineContainerLiteralAt(ctx, view, reader, n.Value, u, aclOutput)
		}
		literal := "NULL"
		if !n.Value.IsNull() {
			output := DumpAliasOutput{View: view}
			text, err := u.Type.OutputWithEnvironment(ctx, n.Value, sqltype.OutputEnvironment{Regclass: ClassOutput{View: view, definition: reader}, Regtype: output, Regcollation: output, Regnamespace: output, Regrole: RoleContextFormatter(ctx), Operator: DumpOperatorOutput{}, Regproc: RoutineOutput{View: view, qualified: true}, RegTS: DumpTextSearchOutput{View: view}, ACLItem: aclOutput})
			if err != nil {
				return "", err
			}
			literal = "'" + strings.ReplaceAll(text, "'", "''") + "'"
		}
		return "CAST(" + literal + " AS " + declaration + ")", nil
	}
	if u.Type.ID() == sqltype.ACLItem && !n.Value.IsNull() {
		text, err := u.Type.OutputWithEnvironment(ctx, n.Value, sqltype.OutputEnvironment{ACLItem: aclOutput})
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS ACLITEM)", nil
	}
	if u.Type.ID() == sqltype.Regrole && !n.Value.IsNull() {
		text, err := u.Type.OutputWithEnvironment(ctx, n.Value, sqltype.OutputEnvironment{Regrole: RoleContextFormatter(ctx)})
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS REGROLE)", nil
	}
	if sqltype.IsRegTS(u.Type.ID()) && !n.Value.IsNull() {
		id, err := n.Value.RegTSOID()
		if err != nil {
			return "", err
		}
		text := TextSearchSpelling(view, nil, id, u.Type.ID(), true)
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS " + u.Type.SQLName() + ")", nil
	}
	if u.Type.ID() == sqltype.Regtype && !n.Value.IsNull() {
		id, err := n.Value.TypeOID()
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(regtypeReference(view, id), "'", "''") + "' AS REGTYPE)", nil
	}
	if sqltype.IsRegObjectAlias(u.Type.ID()) && !n.Value.IsNull() {
		output := DumpAliasOutput{View: view}
		text, err := u.Type.OutputWithEnvironment(ctx, n.Value, sqltype.OutputEnvironment{Regcollation: output, Regnamespace: output})
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(text, "'", "''") + "' AS " + u.Type.SQLName() + ")", nil
	}
	if u.Type.ID() == sqltype.Regclass && !n.Value.IsNull() {
		text, err := n.Value.Text()
		if err != nil {
			return "", err
		}
		id, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return "", err
		}
		return "CAST('" + strings.ReplaceAll(regclassSpellingAt(view, reader, nil, ids.OID(id)), "'", "''") + "' AS REGCLASS)", nil
	}
	return ConstantLiteral(ctx, n)
}

// Serialize captured scalars through their owning type, without evaluating a
// stored expression or converting a temporal introducer into an unsupported cast.
func ConstantLiteral(ctx context.Context, n plan.LiteralExpr) (string, error) {
	u := n.ResultUsage()
	if n.Value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
	}
	if n.UnknownNull {
		if !n.Value.IsNull() || u.Type.ID() != sqltype.Text {
			return "", fmt.Errorf("stuntgres: invalid unknown NULL provenance")
		}
		return "NULL::unknown", nil
	}
	literal, err := u.Type.SQLLiteral(ctx, n.Value)
	// Captured typed geometry must retain its descriptor on every stored
	// route, including source-AST view dumps that do not visit provider Render.
	if err == nil && (u.Type.ID() == sqltype.Box || u.Type.ID() == sqltype.Circle || u.Type.ID() == sqltype.Path || u.Type.ID() == sqltype.Point || u.Type.ID() == sqltype.Line || u.Type.ID() == sqltype.Lseg || u.Type.ID() == sqltype.Polygon || sqltype.IsFTS(u.Type.ID())) {
		return "CAST(" + literal + " AS " + u.Type.SQLName() + ")", nil
	}
	if err == nil && (sqltype.IsOperatorAlias(u.Type.ID()) || sqltype.IsRegTS(u.Type.ID())) {
		return "CAST(" + literal + " AS " + u.Type.SQLName() + ")", nil
	}
	if err == nil && sqltype.IsTransactionIdentifier(u.Type.ID()) {
		return "CAST(" + literal + " AS " + u.Type.SQLName() + ")", nil
	}
	if err == nil && u.Type.ID() == sqltype.Oid {
		return "CAST(" + literal + " AS OID)", nil
	}
	if err == nil && u.Type.ID() == sqltype.JSON {
		return "CAST(" + literal + " AS JSON)", nil
	}
	if err == nil && u.Type.ID() == sqltype.JSONB {
		return "CAST(" + literal + " AS JSONB)", nil
	}
	if err == nil && u.Type.ID() == sqltype.JSONPath {
		return "CAST(" + literal + " AS JSONPATH)", nil
	}
	if err != nil || n.Value.IsNull() {
		return literal, err
	}
	switch u.Type.ID() {
	case sqltype.Interval:
		literalUsage := u
		if u.Typmod >= 0 && (u.Typmod>>16)&sqltype.IntervalSecond == 0 {
			// Non-SECOND precision needs a generic column declaration, not
			// invalid keyword literal syntax. The destination restores that
			// exact typmod; this already-adjusted scalar keeps its field mask.
			literalUsage.Typmod |= sqltype.IntervalFullPrecision
		}
		declaration, err := literalUsage.Declaration()
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(declaration, "INTERVAL(") {
			return declaration + strings.TrimPrefix(literal, "INTERVAL"), nil
		}
		return literal + strings.TrimPrefix(declaration, "INTERVAL"), nil
	case sqltype.Date, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Timetz:
		// The endpoint encoding can already carry its required precision.
		if strings.HasPrefix(literal, "TIMESTAMP") {
			return literal, nil
		}
		declaration, err := u.Declaration()
		if err != nil {
			return "", err
		}
		if u.Type.ID() == sqltype.Timetz && strings.HasPrefix(literal, "pg_catalog.timezone(") {
			return "CAST(" + literal + " AS " + declaration + ")", nil
		}
		return declaration + " " + literal, nil
	case sqltype.Numeric:
		text, err := u.Type.Output(ctx, n.Value)
		if err != nil {
			return "", err
		}
		if text == "NaN" || text == "Infinity" || text == "-Infinity" {
			return literal, nil
		}
		if !strings.ContainsAny(text, ".eE") {
			// Keep NUMERIC identity without adding a display-scale digit.
			text += "e0"
		}
		return text, nil
	}
	return literal, nil
}
