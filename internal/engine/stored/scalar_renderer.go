package stored

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// One recursive serializer validates persisted scalar trees without Eval.
// DEFAULT admits closed Immutable/Stable trees, selected UUID/time generation
// trees, genuine installed gen_salt recipes and fixed nextval leaves with
// coercion wrappers. CHECK also admits its owner row.
type ScalarRenderer struct {
	// A cooking-only reader preserves authentic catalog identity while resolving
	// exact preceding metadata effects; ordinary serialization leaves it nil.
	Definition catalog.DefinitionReader
	ACLOutput  sqltype.ACLItemFormatter
	// Only provider validation may discard identity-only text. Real SQL output
	// and construction labels require explicit captured role presentation.
	Validation     bool
	RoutineDefault bool // Declaration defaults admit ordinary volatile scalar trees.
	// Native domain-default logical replay emits the executable live row
	// shape; source catalog metadata retains every original written child.
	CurrentRowShape        bool
	Owner                  *catalog.Relation
	DomainValue            *sqltype.Usage
	Dependencies           *[]ids.ObjectRef
	DependencyPurposes     map[ids.ObjectRef]catalog.DependencyPurpose
	CommentSnapshot        *bool
	VisibilitySnapshot     *bool
	CatalogAliasReferences *bool
	Provenance             *catalog.Relation
	CapturedManifest       []ids.ObjectRef
	CapturedChecks         []catalog.ExpressionDependency
	// TYPE reparsing transports concrete literals privately, without reinferring
	// unknown input or changing ordinary DEFAULT/CHECK serialization.
	CheckLiterals *[]plan.LiteralExpr
	// Final CHECK replay may compare a retained REAL literal with a widened
	// column. Preserve its exact promoted value, not shortest float32 input.
	CheckReplayPrecision bool
	// The selected UUID shift's formal interval type must survive all-NULL
	// compositions. This context belongs only to its rendered child subtree.
	uuidShiftIntervalNulls bool
}

func DefaultSQL(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	return (ScalarRenderer{}).SQL(ctx, view, node)
}

func RenderColumnDefault(ctx context.Context, view catalog.Read, node expr.Expr) (string, []ids.ObjectRef, error) {
	var dependencies []ids.ObjectRef
	text, err := (ScalarRenderer{Dependencies: &dependencies}).SQL(ctx, view, node)
	return text, dependencies, err
}

func (r ScalarRenderer) SQL(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil {
		return "", fmt.Errorf("stuntgres: missing default node")
	}
	u, p := node.ResultUsage(), node.Properties()
	if u.Type == nil || !u.Valid() || !u.Type.Concrete() || r.Owner == nil && (r.DomainValue == nil && !r.RoutineDefault && p.Volatility > expr.Stable && !analyze.ClosedSequenceDefault(node) && !analyze.ClosedUUIDDefault(node) && !analyze.ClosedTransactionTimeDefault(node) && !analyze.ClosedPgcryptoDefault(node) && !analyze.ClosedSQLRoutineDefault(node) || p.RowDependent) || p.ParameterDependent || p.StageDependent || p.DomainDependent && r.DomainValue == nil {
		return "", fmt.Errorf("stuntgres: invalid stored default properties")
	}
	if err := validateUsageCollationAt(view, r.Definition, u); err != nil {
		return "", err
	}
	if u.Collation.ID != 0 {
		r.recordStoredCollation(view, view.Owner().Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(u.Collation.ID)}), u.Collation)
	}
	if !validExpressionUsageAt(view, r.Definition, u) {
		return "", fmt.Errorf("stuntgres: foreign default type")
	}
	if err := r.recordStoredRoutine(view, node); err != nil {
		return "", err
	}
	r.recordStoredReference(view, view.Owner().Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())}))
	switch n := node.(type) {
	case plan.EarthCheckExpr:
		return r.earthSQL(ctx, view, n)
	case plan.DomainValueExpr:
		if r.DomainValue == nil || *r.DomainValue != n.Usage {
			return "", fmt.Errorf("stuntgres: foreign stored domain VALUE")
		}
		return "VALUE", nil
	case plan.RowExpr:
		count := len(n.Fields)
		if r.CurrentRowShape && n.FieldProjection != nil {
			count = u.Type.BaseType().CompositeLen()
		}
		parts := make([]string, count)
		for i, field := range n.Fields {
			slot := i
			if r.CurrentRowShape && n.FieldProjection != nil {
				slot = n.FieldProjection[i].Slot
				if slot < 0 {
					continue
				}
			}
			text, err := r.SQL(ctx, view, field)
			if err != nil {
				return "", err
			}
			parts[slot] = text
		}
		text := "ROW(" + strings.Join(parts, ", ") + ")"
		if u.Type.ID() != 2249 {
			declaration, err := regobj.TypeDeclarationAt(view, r.Definition, u)
			if err != nil {
				return "", err
			}
			text = "(" + text + ")::" + declaration
		}
		return text, nil
	case plan.FieldExpr:
		text, err := r.SQL(ctx, view, n.Base)
		if err != nil {
			return "", err
		}
		composite := n.Base.ResultUsage().Type.BaseType()
		index, ok := composite.CompositeAttributeIndex(n.Attribute)
		if !ok {
			return "", fmt.Errorf("stuntgres: retired field in stored expression")
		}
		if composite.ID() != 2249 {
			descriptor, _ := catalog.DescriptorAt(view, r.Definition, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(composite.ID())})
			typ, valid := descriptor.(*catalog.Type)
			if !valid || typ.SQLType != composite || typ.OwningRelation == (ids.ObjectAddress{}) {
				return "", fmt.Errorf("stuntgres: invalid stored composite field type")
			}
			descriptor, _ = catalog.DescriptorAt(view, r.Definition, typ.OwningRelation)
			relation, valid := descriptor.(*catalog.Relation)
			if !valid || !CompositeAttributeProvider(typ, relation, n.Attribute) {
				return "", fmt.Errorf("stuntgres: invalid stored composite attribute")
			}
			address := relation.Address()
			address.SubID = n.Attribute
			r.recordStoredReference(view, view.Owner().Domain.Ref(view.Epoch(), address))
		}
		return "(" + text + ")." + catalog.Identifier(n.Base.ResultUsage().Type.BaseType().CompositeMember(index).Name), nil
	case plan.ArrayExpr:
		return r.arraySQL(ctx, view, n)
	case plan.SubscriptExpr:
		return r.subscriptSQL(ctx, view, n)
	case plan.TableOIDExpr:
		if n.Scope != 0 || n.Frame != 0 || n.QueryOccurrence != 0 || r.Owner == nil || !ReferenceEpochValid(view, r.Owner, n.Owner) || n.Owner.Address != r.Owner.Address() || u.Type.ID() != sqltype.Oid {
			return "", fmt.Errorf("stuntgres: invalid generated tableoid owner")
		}
		r.record(n.Owner)
		return "tableoid", nil
	case plan.ColumnExpr:
		if r.Owner == nil || !ReferenceEpochValid(view, r.Owner, n.Origin) || n.Origin.Address.ClassID != r.Owner.Address().ClassID || n.Origin.Address.ObjectID != r.Owner.Address().ObjectID || n.Scope != 1 || n.Frame != 1 || n.Slot < 0 || n.Slot >= len(r.Owner.Columns) || r.Owner.Attributes[n.Slot] != n.Origin.Address.SubID || u != r.Owner.Columns[n.Slot].Usage {
			return "", fmt.Errorf("stuntgres: invalid stored row origin")
		}
		r.record(n.Origin)
		return catalog.QuoteIdentifier(r.Owner.Columns[n.Slot].Name), nil
	case plan.InstalledAmcheckExpr:
		return r.ScalarFunctionSQL(ctx, view, node)
	case plan.RangeConstructorExpr, plan.SQLRoutineExpr, plan.ExtensionPathsExpr, plan.InstalledTextExpr, plan.InstalledUUIDOsspExpr, plan.InstalledCryptoExpr, plan.InstalledSoundexExpr, plan.InstalledMetaphoneExpr, plan.InstalledLevenshteinExpr, plan.InstalledCubeExpr:
		return r.ScalarFunctionSQL(ctx, view, node)
	case plan.InstalledCubeSpatialExpr, plan.InstalledEarthExpr, plan.InstalledEarthPointExpr:
		return r.ScalarFunctionSQL(ctx, view, node)
	case plan.InstalledTablefuncNormalExpr:
		return r.ScalarFunctionSQL(ctx, view, node)
	case plan.InstalledPgrowlocksExpr:
		return r.ScalarFunctionSQL(ctx, view, node)
	case plan.InstalledVerifyHeapamExpr:
		return r.ScalarFunctionSQL(ctx, view, node)
	case functions.ObjectACL:
		if r.Owner == nil {
			return "", fmt.Errorf("stuntgres: object ACL inquiries are unsupported in stored defaults")
		}
		return r.ScalarFunctionSQL(ctx, view, n)
	case functions.AccessInquiry:
		if r.Owner == nil {
			return "", fmt.Errorf("stuntgres: access inquiries are unsupported in stored defaults")
		}
		return r.ScalarFunctionSQL(ctx, view, n)
	case functions.SequenceCall:
		if r.Owner == nil && !analyze.ClosedSequenceDefault(n) {
			return "", fmt.Errorf("stuntgres: invalid stored sequence default")
		}
		args := n.Args
		want := 1
		switch n.Name {
		case "nextval", "currval":
		case "lastval":
			want = 0
		case "setval":
			want = 2
		default:
			return "", fmt.Errorf("stuntgres: invalid stored sequence function")
		}
		if len(args) != want && !(n.Name == "setval" && len(args) == 3) || u.Type.ID() != sqltype.Int8 {
			return "", fmt.Errorf("stuntgres: invalid stored sequence signature")
		}
		for i, arg := range args {
			id := sqltype.Regclass
			if i == 1 {
				id = sqltype.Int8
			}
			if i == 2 {
				id = sqltype.Bool
			}
			if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
				return "", fmt.Errorf("stuntgres: invalid stored sequence argument")
			}
		}
		if n.Ref != (ids.ObjectRef{}) {
			if len(args) == 0 {
				return "", fmt.Errorf("stuntgres: stored sequence identity lacks argument")
			}
			if _, err := r.SQL(ctx, view, args[0]); err != nil {
				return "", err
			}
			target, ok := view.Descriptor(n.Ref.Address)
			if !ok || !SequenceEpochValid(view, r.retainedConsumer(), n.Ref, n.Temporary) {
				return "", fmt.Errorf("stuntgres: invalid stored sequence reference")
			}
			sequence, ok := target.(*catalog.Relation)
			if !ok || sequence.RelationKind != catalog.SequenceRelation {
				return "", fmt.Errorf("stuntgres: invalid stored sequence target")
			}
			r.record(n.Ref)
			namespace, ok := view.Descriptor(sequence.Namespace())
			if !ok {
				return "", fmt.Errorf("stuntgres: missing stored sequence namespace")
			}
			name := catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(sequence.Name())
			parts := []string{"CAST('" + strings.ReplaceAll(name, "'", "''") + "' AS regclass)"}
			for _, arg := range args[1:] {
				text, err := r.SQL(ctx, view, arg)
				if err != nil {
					return "", err
				}
				parts = append(parts, text)
			}
			return "pg_catalog." + n.Name + "(" + strings.Join(parts, ", ") + ")", nil
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.Name, args)
	case plan.CaseExpr, plan.ConditionalExpr:
		return r.conditionalSQL(ctx, view, node)
	case plan.LiteralExpr:
		if err := analyze.ValidateRegroleStoredLiteral(n.Value); err != nil {
			return "", err
		}
		if n.RegclassRef != (ids.ObjectRef{}) {
			ref := n.RegclassRef
			target, ok := catalog.DescriptorAt(view, r.Definition, ref.Address)
			_, relationOK := target.(*catalog.Relation)
			if !ok || !relationOK || !ReferenceEpochValid(view, r.retainedConsumer(), ref) || ref.Address.SubID != 0 || u.Type.ID() != sqltype.Regclass || n.Value.IsNull() {
				return "", fmt.Errorf("stuntgres: invalid stored regclass reference")
			}
			r.recordPurpose(ref, catalog.OperandDependency)
		} else if n.Value.Type().ID() == sqltype.Regclass && !n.Value.IsNull() {
			// Actual scalar input datums retain relation dependencies; numeric
			// coercion trees are not materialized into these constants.
			text, err := n.Value.Text()
			if err != nil {
				return "", err
			}
			oid, err := strconv.ParseUint(text, 10, 32)
			if err != nil {
				return "", err
			}
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
			if _, exists := catalog.DescriptorAt(view, r.Definition, address); exists {
				r.recordPurpose(view.Owner().Domain.Ref(view.Epoch(), address), catalog.OperandDependency)
			}
		}
		if err := r.RecordCatalogAliasDependencies(view, n.Value); err != nil {
			return "", err
		}
		if err := r.RecordRoutineReferences(view, n.Value); err != nil {
			return "", err
		}
		if r.CatalogAliasReferences != nil && (n.Value.Type().ContainsRegtype() || n.Value.Type().ContainsRegObject() || n.Value.Type().ContainsRegTS()) && !n.Value.IsNull() {
			*r.CatalogAliasReferences = true
		}
		if r.CheckLiterals != nil {
			*r.CheckLiterals = append(*r.CheckLiterals, n)
			return fmt.Sprintf("$%d", len(*r.CheckLiterals)), nil
		}
		if r.CheckReplayPrecision && u.Type.ID() == sqltype.Float4 && !n.Value.IsNull() {
			value, err := n.Value.Float()
			if err != nil {
				return "", err
			}
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				return "'" + strconv.FormatFloat(value, 'g', -1, 64) + "'", nil
			}
		}
		if r.uuidShiftIntervalNulls && u.Type.ID() == sqltype.Interval && n.Value.IsNull() {
			declaration, err := u.Declaration()
			if err != nil {
				return "", err
			}
			return "CAST(NULL AS " + declaration + ")", nil
		}
		aclOutput := r.ACLOutput
		if r.Validation {
			aclOutput = regobj.ACLItemValidationOutput{Owner: view.Owner()}
		} else if aclOutput == nil {
			aclOutput = regobj.ACLItemContextFormatter(ctx)
		}
		return regobj.LiteralWithACLAt(ctx, view, r.Definition, n, aclOutput)
	case plan.CollationExpr:
		text, err := r.SQL(ctx, view, n.Child)
		if err != nil {
			return "", err
		}
		if name := typedef.CollationSQLName(view, n.Usage.Collation.ID); name != "" {
			return "(" + text + " COLLATE " + name + ")", nil
		}
		return "", fmt.Errorf("stuntgres: invalid default collation")
	case plan.DatumCastExpr:
		if err := n.ValidateStoredProvider(); err != nil {
			return "", err
		}
		child, err := r.SQL(ctx, view, n.Child)
		if err != nil {
			return "", err
		}
		// A literal's ordinary SQL may be an untyped quoted value or NULL.
		// Preserve the selected source codec instead of letting the enclosing
		// target cast reinterpret that text as its own input.
		if _, literal := n.Child.(plan.LiteralExpr); literal {
			source, err := regobj.TypeDeclarationAt(view, r.Definition, n.Child.ResultUsage())
			if err != nil {
				return "", err
			}
			child = "CAST(" + child + " AS " + source + ")"
		}
		if n.Mode != sqltype.Explicit {
			return child, nil
		}
		declaration, err := regobj.TypeDeclarationAt(view, r.Definition, u)
		if err != nil {
			return "", err
		}
		return "CAST(" + child + " AS " + declaration + ")", nil
	case plan.ArrayCastExpr:
		if err := n.ValidateStoredProvider(); err != nil {
			return "", err
		}
		child, err := r.SQL(ctx, view, n.Child)
		if err != nil {
			return "", err
		}
		if n.Mode != sqltype.Explicit {
			return child, nil
		}
		declaration, err := regobj.TypeDeclarationAt(view, r.Definition, u)
		if err != nil {
			return "", err
		}
		return "CAST(" + child + " AS " + declaration + ")", nil
	case plan.CoercionExpr:
		if n.Child == nil {
			return "", fmt.Errorf("stuntgres: missing default coercion child")
		}
		switch n.Mode {
		case sqltype.Implicit, sqltype.Assignment, sqltype.Explicit:
		default:
			return "", fmt.Errorf("stuntgres: invalid default coercion mode")
		}
		coercionPlan, err := n.Plan.Rebind(n.Child.ResultUsage(), u, n.Mode)
		if err != nil || !coercionPlan.Equal(n.Plan) {
			return "", fmt.Errorf("stuntgres: invalid default coercion")
		}
		if r.CommentSnapshot != nil && (coercionPlan.RequiresCatalogOutput() || (u.Type.ContainsRegrole() || u.Type.ContainsRegObject()) && coercionPlan.SessionDependent()) {
			*r.CommentSnapshot = true
		}
		if n.Plan.IsJSONReturning() {
			spec, supported, err := jsonReturningRendering(n.Child)
			if err != nil {
				return "", err
			}
			if supported {
				return r.renderingWithReturning(ctx, view, spec, u)
			}
		}
		child, err := r.SQL(ctx, view, n.Child)
		if err != nil || n.Mode != sqltype.Explicit && !sqlJSONResult(n.Child) {
			return child, err
		}
		// Identifier literals already render with their scalar authority. A
		// transparent relabel must not grow another CAST on each Restore.
		if r.CheckLiterals == nil && (sqltype.IsTransactionIdentifier(u.Type.ID()) || u.Type.ID() == sqltype.PgSnapshot) && n.Child.ResultUsage() == u {
			return child, nil
		}
		// Bare quoted TEXT becomes unknown on replay and can move a deferred
		// conversion into CREATE-time input decoding. Keep that child typed.
		if n.Child.ResultUsage().Type.ID() == sqltype.Text {
			inner, explicit := n.Child.(plan.CoercionExpr)
			if !explicit || inner.Mode != sqltype.Explicit {
				declaration, err := regobj.TypeDeclarationAt(view, r.Definition, n.Child.ResultUsage())
				if err != nil {
					return "", err
				}
				child = "CAST(" + child + " AS " + declaration + ")"
			}
		}
		declaration, err := regobj.TypeDeclarationAt(view, r.Definition, u)
		if err != nil {
			return "", err
		}
		return "CAST(" + child + " AS " + declaration + ")", nil
	case plan.BitwiseExpr:
		if u != u.Type.Usage() || n.Left == nil || n.Left.ResultUsage() != u {
			return "", fmt.Errorf("stuntgres: invalid default bitwise operands")
		}
		var rightType *sqltype.Descriptor
		if n.Right != nil {
			rightType = n.Right.ResultUsage().Type
			if rightType == nil || n.Right.ResultUsage() != rightType.Usage() {
				return "", fmt.Errorf("stuntgres: invalid default bitwise right usage")
			}
		}
		if u.Type.ID() == sqltype.Macaddr || u.Type.ID() == sqltype.Macaddr8 {
			provider, err := sqltype.BindMACBitwise(n.Operator, u.Type, rightType)
			if err != nil || provider != n.MAC || n.Provider != (sqltype.Bitwise{}) || n.Bits != (sqltype.BitStringBitwise{}) || n.Network != (sqltype.NetworkBitwise{}) {
				return "", fmt.Errorf("stuntgres: invalid default MAC bitwise binding")
			}
		} else if u.Type.ID() == sqltype.Inet {
			provider, err := sqltype.BindNetworkBitwise(n.Operator, u.Type, rightType)
			if err != nil || provider != n.Network || n.Provider != (sqltype.Bitwise{}) || n.Bits != (sqltype.BitStringBitwise{}) || n.MAC != (sqltype.MACBitwise{}) {
				return "", fmt.Errorf("stuntgres: invalid default bitwise binding")
			}
		} else if u.Type.ID() == sqltype.Bit {
			provider, err := sqltype.BindBitStringBitwise(n.Operator, u.Type, rightType)
			if err != nil || provider != n.Bits || n.Provider != (sqltype.Bitwise{}) || n.Network != (sqltype.NetworkBitwise{}) || n.MAC != (sqltype.MACBitwise{}) {
				return "", fmt.Errorf("stuntgres: invalid default bitwise binding")
			}
		} else {
			provider, err := sqltype.BindBitwise(n.Operator, u.Type, rightType)
			if err != nil || provider != n.Provider || n.Bits != (sqltype.BitStringBitwise{}) || n.Network != (sqltype.NetworkBitwise{}) || n.MAC != (sqltype.MACBitwise{}) {
				return "", fmt.Errorf("stuntgres: invalid default bitwise binding")
			}
		}
		operator := [...]string{"&", "|", "#", "~", "<<", ">>"}[n.Operator]
		if n.Namespace != "" {
			operator = "OPERATOR(" + catalog.QuoteIdentifier(n.Namespace) + "." + operator + ")"
		}
		operandSQL := func(child expr.Expr) (string, error) {
			text, err := r.SQL(ctx, view, child)
			if err != nil {
				return "", err
			}
			cu := child.ResultUsage()
			if literal, ok := child.(plan.LiteralExpr); ok && cu.Type.ID() != sqltype.Bit && (cu.Type.ID() != sqltype.Int4 || literal.Value.IsNull()) {
				declaration, err := regobj.TypeDeclaration(view, cu)
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			return text, nil
		}
		left, err := operandSQL(n.Left)
		if err != nil {
			return "", err
		}
		if n.Right == nil {
			return "(" + operator + " " + left + ")", nil
		}
		right, err := operandSQL(n.Right)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case plan.SelectedOperatorExpr:
		r.record(n.Ref)
		d, ok := view.Operator(n.Ref.Address)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing default operator")
		}
		namespace := view.Get(d.Namespace()).Name()
		op := "OPERATOR(" + catalog.QuoteIdentifier(namespace) + "." + d.Name() + ")"
		var operands []expr.Expr
		plan.ExpressionChildren(n.Provider, func(arg expr.Expr) {
			if arg != nil {
				operands = append(operands, arg)
			}
		})
		text := make([]string, len(operands))
		for i, arg := range operands {
			var err error
			text[i], err = r.SQL(ctx, view, arg)
			if err != nil {
				return "", err
			}
		}
		if d.Left == 0 && len(text) == 1 {
			return "(" + op + " " + text[0] + ")", nil
		}
		if d.Left != 0 && len(text) == 2 {
			return "(" + text[0] + " " + op + " " + text[1] + ")", nil
		}
		return "", fmt.Errorf("stuntgres: invalid default operator operands")
	case plan.ArithmeticExpr:
		if u != u.Type.Usage() || n.Left == nil {
			return "", fmt.Errorf("stuntgres: invalid default arithmetic operands")
		}
		var rightType *sqltype.Descriptor
		if n.Right != nil {
			rightType = n.Right.ResultUsage().Type
		}
		provider, err := sqltype.BindArithmetic(n.Operator, n.Left.ResultUsage().Type, rightType, u.Type)
		if err != nil || provider != n.Provider || provider.ResultType() != u.Type {
			return "", fmt.Errorf("stuntgres: invalid default arithmetic binding")
		}
		var operator string
		switch n.Operator {
		case sqltype.ArithmeticAdd:
			operator = "+"
		case sqltype.ArithmeticSubtract:
			operator = "-"
		case sqltype.ArithmeticMultiply:
			operator = "*"
		case sqltype.ArithmeticDivide:
			operator = "/"
		case sqltype.ArithmeticRemainder:
			operator = "%"
		case sqltype.ArithmeticPower:
			operator = "^"
		default:
			return "", fmt.Errorf("stuntgres: invalid default arithmetic operator")
		}
		if n.Namespace != "" {
			operator = "OPERATOR(" + catalog.QuoteIdentifier(n.Namespace) + "." + operator + ")"
		}
		operandSQL := func(child expr.Expr) (string, error) {
			text, err := r.SQL(ctx, view, child)
			if err != nil {
				return "", err
			}
			// Preserve each leaf's own width, not its parent's result width.
			cu := child.ResultUsage()
			if literal, ok := child.(plan.LiteralExpr); ok && (cu.Type.ID() != sqltype.Int4 || literal.Value.IsNull()) {
				declaration, err := regobj.TypeDeclaration(view, cu)
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			return text, nil
		}
		left, err := operandSQL(n.Left)
		if err != nil {
			return "", err
		}
		if n.Right == nil {
			return "(" + operator + " " + left + ")", nil
		}
		right, err := operandSQL(n.Right)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case plan.LikeExpr:
		left, err := r.SQL(ctx, view, n.Left)
		if err != nil {
			return "", err
		}
		right, err := r.SQL(ctx, view, n.Right)
		if err != nil {
			return "", err
		}
		operator := n.Operator
		if n.Namespace != "" {
			operator = "OPERATOR(" + catalog.Identifier(n.Namespace) + "." + operator + ")"
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case plan.RowComparisonExpr:
		for _, pair := range n.Comparisons {
			if _, err := r.SQL(ctx, view, pair); err != nil {
				return "", err
			}
		}
		return renderRowComparison(n, func(child expr.Expr) (string, error) { return r.SQL(ctx, view, child) }, n.Operator)
	case plan.ScalarArrayComparisonExpr:
		if u.Type.ID() != sqltype.Bool || n.Left == nil || (n.Array == nil) == (len(n.Items) == 0) {
			return "", fmt.Errorf("stuntgres: invalid scalar-array predicate")
		}
		operator, ok := analyze.ComparisonOperator(n.Operator)
		if !ok {
			return "", fmt.Errorf("stuntgres: invalid scalar-array operator")
		}
		ref, conflict := n.Predicate.InputCollation()
		predicate, err := sqltype.BindComparisonPredicate(operator, n.Left.ResultUsage().Type, n.ElementType(), u.Type, ref, conflict)
		if err != nil || !predicate.Equal(n.Predicate) {
			return "", fmt.Errorf("stuntgres: invalid scalar-array predicate binding")
		}
		return renderScalarArrayComparison(n, func(child expr.Expr) (string, error) { return r.SQL(ctx, view, child) }, catalog.QuoteIdentifier)
	case plan.ComparisonExpr:
		if u.Type.ID() != sqltype.Bool || n.Left == nil || n.Right == nil {
			return "", fmt.Errorf("stuntgres: invalid default predicate")
		}
		var operator sqltype.ComparisonOperator
		switch n.Operator {
		case "=":
			operator = sqltype.CompareEqual
		case "<>":
			operator = sqltype.CompareNotEqual
		case "<":
			operator = sqltype.CompareLess
		case "<=":
			operator = sqltype.CompareLessEqual
		case ">":
			operator = sqltype.CompareGreater
		case ">=":
			operator = sqltype.CompareGreaterEqual
		default:
			return "", fmt.Errorf("stuntgres: invalid default comparison")
		}
		ref, conflict := n.Predicate.InputCollation()
		predicate, err := sqltype.BindComparisonPredicate(operator, n.Left.ResultUsage().Type, n.Right.ResultUsage().Type, u.Type, ref, conflict)
		if err != nil || !predicate.Equal(n.Predicate) {
			return "", fmt.Errorf("stuntgres: invalid default predicate binding")
		}
		left, err := r.SQL(ctx, view, n.Left)
		if err != nil {
			return "", err
		}
		right, err := r.SQL(ctx, view, n.Right)
		if err != nil {
			return "", err
		}
		spelling := n.Operator
		if n.Namespace != "" {
			spelling = "OPERATOR(" + catalog.QuoteIdentifier(n.Namespace) + "." + spelling + ")"
		}
		return "(" + left + " " + spelling + " " + right + ")", nil
	case plan.NullTestExpr:
		if u.Type.ID() != sqltype.Bool || n.Child == nil {
			return "", fmt.Errorf("stuntgres: invalid default null test")
		}
		child, err := r.SQL(ctx, view, n.Child)
		if err != nil {
			return "", err
		}
		operator := "IS NULL"
		if n.Not {
			operator = "IS NOT NULL"
		}
		if n.Whole && n.Child.ResultUsage().Type.BaseType().IsComposite() {
			operator = "IS NOT DISTINCT FROM NULL"
			if n.Not {
				operator = "IS DISTINCT FROM NULL"
			}
		}
		return "(" + child + " " + operator + ")", nil
	case plan.TruthTestExpr:
		if u.Type.ID() != sqltype.Bool || n.Child == nil || n.Child.ResultUsage().Type != u.Type {
			return "", fmt.Errorf("stuntgres: invalid default truth test")
		}
		var truth string
		switch n.Truth {
		case plan.True:
			truth = "TRUE"
		case plan.False:
			truth = "FALSE"
		case plan.Unknown:
			truth = "UNKNOWN"
		default:
			return "", fmt.Errorf("stuntgres: invalid default truth value")
		}
		child, err := r.SQL(ctx, view, n.Child)
		if err != nil {
			return "", err
		}
		operator := "IS"
		if n.Not {
			operator = "IS NOT"
		}
		return "(" + child + " " + operator + " " + truth + ")", nil
	case plan.DistinctExpr:
		if u.Type.ID() != sqltype.Bool || n.Left == nil || n.Right == nil {
			return "", fmt.Errorf("stuntgres: invalid default distinct test")
		}
		ref, conflict := n.Equality.InputCollation()
		predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, n.Left.ResultUsage().Type, n.Right.ResultUsage().Type, u.Type, ref, conflict)
		if err != nil || !predicate.Equal(n.Equality) {
			return "", fmt.Errorf("stuntgres: invalid default distinct binding")
		}
		left, err := r.SQL(ctx, view, n.Left)
		if err != nil {
			return "", err
		}
		right, err := r.SQL(ctx, view, n.Right)
		if err != nil {
			return "", err
		}
		operator := "IS DISTINCT FROM"
		if n.Not {
			operator = "IS NOT DISTINCT FROM"
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case plan.LogicalExpr:
		if u.Type.ID() != sqltype.Bool || n.Left == nil || n.Left.ResultUsage().Type.ID() != sqltype.Bool || n.Op == parser.LogicalNot && n.Right != nil || n.Op != parser.LogicalNot && (n.Right == nil || n.Right.ResultUsage().Type.ID() != sqltype.Bool) {
			return "", fmt.Errorf("stuntgres: invalid default logical binding")
		}
		left, err := r.SQL(ctx, view, n.Left)
		if err != nil {
			return "", err
		}
		if n.Op == parser.LogicalNot {
			return "(NOT " + left + ")", nil
		}
		right, err := r.SQL(ctx, view, n.Right)
		if err != nil {
			return "", err
		}
		operator := "AND"
		if n.Op == parser.LogicalOr {
			operator = "OR"
		} else if n.Op != parser.LogicalAnd {
			return "", fmt.Errorf("stuntgres: invalid default logical operator")
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case expr.Renderable:
		// Function nodes describe their stored form; engine wrappers that embed
		// one keep their explicit cases above.
		return r.ScalarFunctionSQL(ctx, view, node)
	default:
		return "", fmt.Errorf("stuntgres: unsupported stored default node")
	}
}

func (r ScalarRenderer) functionSQL(ctx context.Context, view catalog.Read, name string, args []expr.Expr) (string, error) {
	parts := make([]string, len(args))
	for i, arg := range args {
		text, err := r.SQL(ctx, view, arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}

func (r ScalarRenderer) record(ref ids.ObjectRef) {
	r.recordPurpose(ref, catalog.DefinitionDependency)
}

func (r ScalarRenderer) recordPurpose(ref ids.ObjectRef, purpose catalog.DependencyPurpose) {
	if r.DependencyPurposes != nil {
		if old, exists := r.DependencyPurposes[ref]; !exists || old == catalog.OperandDependency {
			r.DependencyPurposes[ref] = purpose
		}
	}
	if r.Dependencies == nil {
		return
	}
	for _, old := range *r.Dependencies {
		if old == ref {
			return
		}
	}
	*r.Dependencies = append(*r.Dependencies, ref)
}

func (r ScalarRenderer) RecordRoutineReferences(view catalog.Read, value sqltype.Value) error {
	return regobj.WalkRoutineReferencesAt(view, r.Definition, value, func(address ids.ObjectAddress) error {
		r.recordPurpose(view.Owner().Domain.Ref(view.Epoch(), address), catalog.OperandDependency)
		return nil
	})
}

func (r ScalarRenderer) RecordCatalogAliasDependencies(view catalog.Read, value sqltype.Value) error {
	record := func(address ids.ObjectAddress) error {
		r.recordPurpose(view.Owner().Domain.Ref(view.Epoch(), address), catalog.OperandDependency)
		return nil
	}
	if err := catalog.WalkAliasDependenciesAt(view, r.Definition, value, record); err != nil {
		return err
	}
	if err := regobj.WalkOperatorReferencesAt(view, r.Definition, value, record); err != nil {
		return err
	}
	return regobj.WalkTextSearchReferencesAt(view, r.Definition, value, record)
}

func (r ScalarRenderer) RecordCatalogAliasReferences(view catalog.Read, value sqltype.Value) error {
	record := func(address ids.ObjectAddress) error {
		r.recordPurpose(view.Owner().Domain.Ref(view.Epoch(), address), catalog.OperandDependency)
		return nil
	}
	if err := catalog.WalkAliasReferencesAt(view, r.Definition, value, record); err != nil {
		return err
	}
	if err := regobj.WalkOperatorReferencesAt(view, r.Definition, value, record); err != nil {
		return err
	}
	return regobj.WalkTextSearchReferencesAt(view, r.Definition, value, record)
}
