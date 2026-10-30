package exec

import (
	"crypto/fips140"
	"crypto/rand"
	"errors"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"time"
)

var _ functions.InputValidityRuntime = (*Context)(nil)

func (c *Context) CheckTypeInput(value, typeName string) (*sqlstate.Error, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing type-input authority")
	}
	return host.CheckTypeInput(c, value, typeName)
}

var _ functions.SessionActivityRuntime = (*Context)(nil)

func (c *Context) SessionActivityValue(kind functions.SessionActivityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: session activity lacks statement authority")
	}
	return host.SessionActivityValue(kind, result)
}

var _ functions.TriggerDepthRuntime = (*Context)(nil)

func (c *Context) TriggerDepthValue(result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: trigger depth lacks statement authority")
	}
	return host.TriggerDepthValue(result)
}

var _ functions.EventTriggerRewriteRuntime = (*Context)(nil)

func (c *Context) EventTriggerRewriteValue(oid bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, functions.EventTriggerRewriteContextError(oid)
	}
	return host.EventTriggerRewriteValue(oid, result)
}

// The ACL SQL functions borrow the executing statement's authority through
// this capability. Each inquiry holds e.mu only for its own catalog and role
// reads and caches nothing on the expression or statement.
var _ functions.ACLRuntime = (*Context)(nil)

func (c *Context) ACLAuthority() *sqltype.ACLAuthority {
	host := c.LiveHost()
	if host == nil {
		return nil
	}
	return host.ACLAuthority()
}

func (c *Context) ObjectACL(address ids.ObjectAddress, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: object ACL inquiry lacks statement authority")
	}
	return host.ObjectACL(c, address, result)
}

func (c *Context) RoleMembership(actor *functions.AccessRole, target functions.AccessRole, privilege string) (bool, error) {
	host := c.LiveHost()
	if host == nil {
		return false, fmt.Errorf("stuntgres: access inquiry lacks statement authority")
	}
	return host.RoleMembership(c, actor, target, privilege)
}

func (c *Context) RowSecurityActive(relation functions.AccessRelation) (bool, error) {
	host := c.LiveHost()
	if host == nil {
		return false, fmt.Errorf("stuntgres: access inquiry lacks statement authority")
	}
	return host.RowSecurityActive(c, relation)
}

func (c *Context) ObjectPrivilege(kind functions.AccessKind, actor *functions.AccessRole, object functions.AccessRole, privilege string) (bool, bool, error) {
	host := c.LiveHost()
	if host == nil {
		return false, false, fmt.Errorf("stuntgres: access inquiry lacks statement authority")
	}
	return host.ObjectPrivilege(c, kind, actor, object, privilege)
}

func (c *Context) RelationPrivilege(kind functions.AccessKind, actor *functions.AccessRole, relation functions.AccessRelation, column functions.AccessColumn, privilege string) (bool, bool, error) {
	host := c.LiveHost()
	if host == nil {
		return false, false, fmt.Errorf("stuntgres: access inquiry lacks statement authority")
	}
	return host.RelationPrivilege(c, kind, actor, relation, column, privilege)
}

var _ functions.AdvisoryRuntime = (*Context)(nil)

// Advisory resolves the current statement owner, never a prepared owner.
func (c *Context) Advisory(action functions.AdvisoryAction, mode functions.AdvisoryMode, lifetime functions.AdvisoryLifetime, first, second int64, pair bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, errors.New("stuntgres: missing advisory session scope")
	}
	return host.Advisory(c, action, mode, lifetime, first, second, pair, result)
}

var _ functions.SettingRuntime = (*Context)(nil)

func (c *Context) InvokeSetting(name string, inputs []sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, errors.New("stuntgres: setting call lacks statement authority")
	}
	return host.InvokeSetting(name, inputs, result)
}

var _ functions.BlockingRuntime = (*Context)(nil)

func (c *Context) BlockingInquiry(kind functions.BlockingKind, pid int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: blocking inquiry lacks statement authority")
	}
	return host.BlockingInquiry(kind, pid, result)
}

var _ functions.CatalogInquiryRuntime = (*Context)(nil)

func (c *Context) FormatType(oid uint32, typmod int32, specified bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing type inquiry snapshot")
	}
	return host.FormatType(oid, typmod, specified, result)
}

func (c *Context) BaseType(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing type inquiry snapshot")
	}
	return host.BaseType(oid, result)
}

func (c *Context) CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing catalog role-name snapshot")
	}
	return host.CatalogUserName(oid, result)
}

func (c *Context) TablespaceLocation(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing tablespace inquiry snapshot")
	}
	return host.TablespaceLocation(oid, result)
}

func (c *Context) TablespaceDatabase(oid uint32, result *sqltype.Descriptor) (sqltype.Value, bool, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing tablespace inquiry snapshot")
	}
	return host.TablespaceDatabase(oid, result)
}

func (c *Context) SizeInquiry(kind functions.SizeKind, arg sqltype.Value, fork string, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing size inquiry snapshot")
	}
	return host.SizeInquiry(kind, arg, fork, result)
}

func (c *Context) DatabaseFilesInquiry(kind functions.DatabaseFilesKind, arg, second sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil { return sqltype.Value{}, fmt.Errorf("stuntgres: missing database files inquiry snapshot") }
	return host.DatabaseFilesInquiry(kind, arg, second, result)
}

func (c *Context) GenericFileList(kind functions.GenericFileListKind, dirname string) error {
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing generic file listing host")
	}
	return host.GenericFileList(kind, dirname)
}

func (c *Context) LogicalDecoding(slot, options sqltype.Value) error {
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing logical decoding host")
	}
	return host.LogicalDecoding(slot, options)
}

func (c *Context) GenericFileRead(kind functions.GenericFileReadKind, filename string, length int64, hasLength bool) error {
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing generic file read host")
	}
	return host.GenericFileRead(kind, filename, length, hasLength)
}

func (c *Context) GenericFileTmpdir(oid uint32) error {
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing temporary file listing host")
	}
	return host.GenericFileTmpdir(oid)
}

func (c *Context) PartitionTree(oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing partition inquiry snapshot")
	}
	return host.PartitionTree(oid, result)
}

func (c *Context) PartitionAncestors(oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing partition inquiry snapshot")
	}
	return host.PartitionAncestors(oid, result)
}

func (c *Context) PartitionRoot(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing partition inquiry snapshot")
	}
	return host.PartitionRoot(oid, result)
}

func (c *Context) RelationIsPublishable(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing publication inquiry snapshot")
	}
	return host.RelationIsPublishable(oid, result)
}

func (c *Context) PublicationTables(names sqltype.Value, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing publication inquiry snapshot")
	}
	return host.PublicationTables(names, result)
}

func (c *Context) CommentInquiry(kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing comment snapshot")
	}
	return host.CommentInquiry(c, kind, values, provenance, result)
}

// Imported parameters remain Execute-owned. This predicate inspects structure
// and availability only, never evaluates a child again or freezes its value.
func OverlapsExecutionConstant(node expr.Expr, c *Context) bool {
	switch n := node.(type) {
	case plan.LiteralExpr:
		return true
	case plan.ParameterExpr:
		return n.Number > 0 && n.Number <= len(c.Parameters)
	case plan.ColumnExpr, plan.GroupExpr, plan.AggregateExpr, plan.GroupingExpr, plan.SubqueryExpr:
		return false
	}
	if PlannedVolatility(node) != expr.Immutable {
		return false
	}
	found, constant := false, true
	plan.ExpressionChildren(node, func(child expr.Expr) { found = true; constant = constant && OverlapsExecutionConstant(child, c) })
	return found && constant
}

func (c *Context) DeparseInquiry(signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing deparse catalog snapshot")
	}
	return host.DeparseInquiry(c, signature, values, result)
}

// PlannedVolatility inspects the physical surviving tree. Analysis properties
// deliberately retain dependence from eliminated CASE/COALESCE arms.
func PlannedVolatility(node expr.Expr) expr.Volatility {
	return PlannedPhysicalVolatility(node, false)
}

// COPY's supported nextval defaults are batch-safe, but their argument trees
// are still inspected. Other consumers retain ordinary physical volatility.
func PlannedPhysicalVolatility(node expr.Expr, copyNextval bool) expr.Volatility {
	return PlannedNodeVolatility(node, copyNextval, false)
}

func PlannedNodeVolatility(node expr.Expr, copyNextval, intrinsicOnly bool) expr.Volatility {
	if node == nil {
		return expr.Immutable
	}
	merge := func(a, b expr.Volatility) expr.Volatility {
		if b > a {
			return b
		}
		return a
	}
	children := func(args ...expr.Expr) expr.Volatility {
		if intrinsicOnly {
			return expr.Immutable
		}
		v := expr.Immutable
		for _, a := range args {
			v = merge(v, PlannedPhysicalVolatility(a, copyNextval))
		}
		return v
	}
	session := func(v expr.Volatility, dependent bool) expr.Volatility {
		if dependent {
			return merge(v, expr.Stable)
		}
		return v
	}
	switch n := node.(type) {
	case functions.SequenceCall:
		if copyNextval && n.Name == "nextval" {
			return children(n.Args...)
		}
		return n.Properties().Volatility
	case plan.AssignmentValueExpr:
		v := children(n.Base, n.Value)
		for target := n.Target; target != nil; target = target.Next {
			for _, sub := range target.Node.Subscripts {
				v = merge(v, children(sub.Lower, sub.Upper))
			}
			v = session(v, target.Reapply.SessionDependent())
		}
		return v
	case plan.RowExpr:
		return children(n.Fields...)
	case plan.FieldExpr:
		return children(n.Base)
	case plan.ArrayExpr:
		return children(n.Elements...)
	case plan.SubscriptExpr:
		var storage [13]expr.Expr
		return children(plan.SubscriptChildren(n, &storage)...)
	case plan.EarthCheckExpr:
		return children(n.Value)
	case plan.DomainValueExpr, plan.LiteralExpr, plan.ColumnExpr:
		return expr.Immutable
	case plan.CollationExpr:
		return children(n.Child)
	case plan.CoercionExpr:
		return session(children(n.Child), n.Plan.SessionDependent())
	case plan.RowComparisonExpr:
		v := expr.Immutable
		for _, pair := range n.Comparisons {
			if intrinsicOnly {
				v = merge(v, PlannedNodeVolatility(pair, false, true))
			} else {
				v = merge(v, PlannedVolatility(pair))
			}
		}
		return v
	case plan.ScalarArrayComparisonExpr:
		v := children(n.Left)
		if n.Array != nil {
			v = merge(v, children(n.Array))
		}
		v = merge(v, children(n.Items...))
		return session(v, n.Predicate.SessionDependent())
	case plan.ComparisonExpr:
		return session(children(n.Left, n.Right), n.Predicate.SessionDependent())
	case plan.SelectedOperatorExpr:
		return children(n.Provider)
	case plan.ArithmeticExpr:
		return session(children(n.Left, n.Right), n.Provider.SessionDependent())
	case plan.BitwiseExpr:
		return children(n.Left, n.Right)
	case plan.LikeExpr:
		return children(n.Left, n.Right)
	case plan.NullTestExpr:
		return children(n.Child)
	case plan.TruthTestExpr:
		return children(n.Child)
	case plan.LogicalExpr:
		return children(n.Left, n.Right)
	case plan.DistinctExpr:
		return session(children(n.Left, n.Right), n.Equality.SessionDependent())
	case plan.ConditionalExpr:
		return session(children(n.Args...), n.Comparison.SessionDependent())
	case plan.ExtensionPathsExpr:
		return merge(children(n.Arg), expr.Stable)
	case plan.InstalledEarthExpr:
		return merge(n.Procedure.Volatility, children(n.Args...))
	case plan.InstalledCubeExpr:
		return merge(n.Procedure.Volatility, children(n.Args...))
	case plan.InstalledSoundexExpr:
		return children(n.Args...)
	case plan.InstalledMetaphoneExpr:
		return children(n.Args...)
	case plan.InstalledLevenshteinExpr:
		return children(n.Args...)
	case plan.InstalledUUIDOsspExpr:
		return merge(n.Procedure.Volatility, children(n.Args...))
	case plan.InstalledCryptoExpr:
		return merge(n.Procedure.Volatility, children(n.Args...))
	case plan.InstalledEarthPointExpr:
		return merge(n.Procedure.Volatility, children(n.Args[:]...))
	case plan.InstalledCubeSpatialExpr:
		return merge(n.Procedure.Volatility, children(n.Args[:]...))
	case plan.InstalledTextExpr:
		return merge(n.Procedure.Volatility, children(n.Arg))
	case plan.CaseExpr:
		v := children(n.Test, n.Otherwise)
		for _, arm := range n.Arms {
			v = merge(v, children(arm.Condition, arm.Test, arm.Result))
			if n.Test != nil {
				v = session(v, arm.Equality.SessionDependent())
			}
		}
		return v
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence.
		intrinsic, ok := n.(expr.Intrinsic)
		if !ok {
			return n.Properties().Volatility
		}
		return merge(children(n.Children()...), intrinsic.IntrinsicVolatility())
	default:
		return node.Properties().Volatility
	}
}

// EvalInstalledText checks the captured provider, then runs the kernel.
func (c *Context) EvalInstalledText(n plan.InstalledTextExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if !preparation.ValidInstalledText(n) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed function expression")
	}
	if n.Function == "unaccent" {
		value, err := n.Arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
		input, err := value.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		host := c.LiveHost()
		if host == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: unaccent lacks statement authority")
		}
		return host.UnaccentText(n.Ref, input, n.Usage.Type)
	}
	return n.InstalledText.Eval(c)
}

// OpenExtensionPaths lists every ordered version pair of the named extension
// with its update path (NULL when no path exists).
func (c *Context) OpenExtensionPaths(n plan.ExtensionPathsExpr) ([]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	value, err := n.Arg.Eval(c)
	if err != nil {
		return nil, err
	}
	if value.IsNull() {
		return nil, nil
	}
	name, err := value.Text()
	if err != nil {
		return nil, err
	}
	if detail := catalog.ExtensionNameDetail(name, false); detail != "" {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid extension name: "%s"`, name), Detail: detail}
	}
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing extension paths snapshot")
	}
	return host.OpenExtensionPaths(c, n, name)
}

func (c *Context) IndexProperty(kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error) {
	host := c.LiveHost()
	if host == nil {
		return false, false, fmt.Errorf("stuntgres: missing index property catalog snapshot")
	}
	return host.IndexProperty(kind, oid, ordinalValue, property)
}

func (c *Context) LookupObjectAddress(values [3]sqltype.Value) (ids.ObjectAddress, error) {
	host := c.LiveHost()
	if host == nil {
		return ids.ObjectAddress{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return host.LookupObjectAddress(c, values)
}

func (c *Context) IdentifyObject(values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return host.IdentifyObject(c, values, result)
}

func (c *Context) IdentifyObjectAddress(values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return host.IdentifyObjectAddress(c, values, result)
}

var _ functions.EventTriggerDroppedRuntime = (*Context)(nil)

func (c *Context) EventTriggerDroppedObjects() ([][]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, functions.EventTriggerDroppedContextError()
	}
	return host.EventTriggerDroppedObjects()
}

func (c *Context) EventTriggerDDLCommands() ([][]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, functions.EventTriggerDDLCommandsContextError()
	}
	return host.EventTriggerDDLCommands()
}

func (c *Context) DescribeObject(values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return host.DescribeObject(c, values, result)
}

func (c *Context) FunctionPgcryptoEntropy(bytes []byte) error {
	if err := c.FunctionContext().Err(); err != nil {
		return err
	}
	_, err := rand.Read(bytes)
	return err
}

func (c *Context) FunctionPgcryptoFIPSMode() bool { return fips140.Enabled() }

// FunctionPgcryptoNotice borrows only the currently executing statement host.
func (c *Context) FunctionPgcryptoNotice(notice sqlstate.Error) {
	if host, ok := c.LiveHost().(interface{ PgcryptoNotice(sqlstate.Error) }); ok {
		host.PgcryptoNotice(notice)
	}
}

// FunctionJSONPathNotice borrows the current execution, not a prepared owner.
func (c *Context) FunctionJSONPathNotice(notice sqlstate.Error) {
	if host, ok := c.LiveHost().(interface{ JSONPathNotice(sqlstate.Error) }); ok {
		host.JSONPathNotice(notice)
	}
}

func (c *Context) FunctionRandomOwner() (expr.RandomOwner, error) {
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing random session scope")
	}
	return host.FunctionRandomOwner()
}

func RandomLiteralNull(arg expr.Expr) bool {
	switch e := arg.(type) {
	case plan.LiteralExpr:
		return e.Value.IsNull()
	case plan.CollationExpr:
		return RandomLiteralNull(e.Child)
	case plan.CoercionExpr:
		return RandomLiteralNull(e.Child)
	}
	return false
}

// FunctionRegexExecution is the expr.Context capability that lends regex
// nodes the statement's physical regex executor.
func (c *Context) FunctionRegexExecution() *sqltype.PGRegexExecution {
	if host := c.LiveHost(); host != nil {
		return host.FunctionRegexExecution()
	}
	return &c.PGRegex
}

var _ functions.SequenceRuntime = (*Context)(nil)

// SequenceAuthority installs the evaluation frame (cancellation and any
// active domain-check input) and requires the current statement's authority
// before a sequence call demands its arguments.
func (c *Context) SequenceAuthority() error {
	if err := EvaluationContext(c); err != nil {
		return err
	}
	if c.LiveHost() == nil {
		return fmt.Errorf("stuntgres: sequence call lacks statement authority")
	}
	return nil
}

// Sequence resolves the current statement owner, never a prepared owner. The
// provider is fenced against catalog replacement before and after its
// RowExclusive lock; ACL, read-only admission, the generator and the
// physical-session memory are read under Engine.mu after the lock is held.
func (c *Context) Sequence(request functions.SequenceRequest) (int64, error) {
	if InParallelOperation(c.Context) && (request.Name == "nextval" || request.Name == "setval") {
		return 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25000", Message: "cannot execute " + request.Name + "() during a parallel operation"}
	}
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: sequence call lacks statement authority")
	}
	return host.Sequence(request)
}

func (c *Context) IdentityValue(kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: identity lacks statement authority")
	}
	return host.IdentityValue(c, kind, result)
}

var _ functions.SessionInquiryRuntime = (*Context)(nil)

func (c *Context) CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: current_schemas lacks statement authority")
	}
	return host.CurrentSchemas(implicit, result)
}

func (c *Context) MyTempNamespace() (uint32, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: namespace inquiry lacks statement authority")
	}
	return host.MyTempNamespace()
}

func (c *Context) OtherTempNamespace(oid uint32) (bool, error) {
	host := c.LiveHost()
	if host == nil {
		return false, fmt.Errorf("stuntgres: namespace inquiry lacks statement authority")
	}
	return host.OtherTempNamespace(oid)
}

func (c *Context) ListeningChannels(result *sqltype.Descriptor) ([]sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: notification inquiry lacks statement authority")
	}
	return host.ListeningChannels(result)
}

func (c *Context) NotificationQueueUsage() (float64, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: notification inquiry lacks statement authority")
	}
	return host.NotificationQueueUsage()
}

var _ functions.TransactionInquiryRuntime = (*Context)(nil)

func (c *Context) CheckTransactionInquiryContext() error { return EvaluationContext(c) }

func (c *Context) RequireTransactionIdentityScope() error {
	if c.LiveHost() == nil {
		return fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return nil
}

func (c *Context) TransactionAge(xid uint32) (int32, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.TransactionAge(xid)
}

func (c *Context) MultiTransactionAge(xid uint32) (int32, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.MultiTransactionAge(xid)
}

func (c *Context) CurrentTransactionID(assign bool) (uint64, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.CurrentTransactionID(assign)
}

func (c *Context) CurrentSnapshot() (*sqltype.Snapshot, error) {
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: snapshot inquiry lacks statement scope")
	}
	return host.CurrentSnapshot()
}

func (c *Context) TransactionStatus(full uint64) (string, bool, error) {
	host := c.LiveHost()
	if host == nil {
		return "", false, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.TransactionStatus(full)
}

func (c *Context) TransactionMembers(xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: MultiXact lacks statement scope")
	}
	return host.TransactionMembers(xid, result)
}

func (c *Context) Pgrowlocks(name string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: pgrowlocks lacks statement scope")
	}
	return host.Pgrowlocks(name, result)
}

func (c *Context) OpenPgrowlocks(n plan.InstalledPgrowlocksExpr) (expr.SourceCursor, error) {
	if err := EvaluationContext(c); err != nil {
		return expr.SourceCursor{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: pgrowlocks lacks statement scope")
	}
	if err := host.ValidatePgrowlocks(n); err != nil {
		return expr.SourceCursor{}, err
	}
	return n.Pgrowlocks.OpenSource(c)
}

func (c *Context) OpenTablefuncNormal(n plan.InstalledTablefuncNormalExpr) (expr.SourceCursor, error) {
	if err := EvaluationContext(c); err != nil {
		return expr.SourceCursor{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: normal_rand lacks statement scope")
	}
	if err := host.ValidateTablefuncNormal(n); err != nil {
		return expr.SourceCursor{}, err
	}
	return n.TablefuncNormal.OpenSource(c)
}

func (c *Context) OpenConnectby(n plan.InstalledConnectbyExpr) (expr.SourceCursor, error) {
	if err := EvaluationContext(c); err != nil {
		return expr.SourceCursor{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: connectby lacks statement scope")
	}
	if err := host.ValidateConnectby(n); err != nil {
		return expr.SourceCursor{}, err
	}
	return n.Connectby.OpenSource(c)
}
func (c *Context) Connectby(request functions.ConnectbyRequest) ([]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: connectby lacks statement scope")
	}
	request.Input, request.Output = c.FunctionJSONBRecordInput(), c.Output
	return host.Connectby(request)
}

func (c *Context) TablefuncCrosstab(text string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: crosstab lacks statement scope")
	}
	return host.TablefuncCrosstab(text, result)
}

func (c *Context) OpenTablefuncCrosstab(n plan.InstalledTablefuncCrosstabExpr) (expr.SourceCursor, error) {
	if err := EvaluationContext(c); err != nil {
		return expr.SourceCursor{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: crosstab lacks statement scope")
	}
	if err := host.ValidateTablefuncCrosstab(n); err != nil {
		return expr.SourceCursor{}, err
	}
	return n.TablefuncCrosstab.OpenSource(c)
}

func (c *Context) VerifyHeapam(args [6]sqltype.Value, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: verify_heapam lacks statement scope")
	}
	return host.VerifyHeapam(args, result)
}

func (c *Context) OpenVerifyHeapam(n plan.InstalledVerifyHeapamExpr) (expr.SourceCursor, error) {
	if err := EvaluationContext(c); err != nil {
		return expr.SourceCursor{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: verify_heapam lacks statement scope")
	}
	if err := host.ValidateVerifyHeapam(n); err != nil {
		return expr.SourceCursor{}, err
	}
	return n.VerifyHeapam.OpenSource(c)
}

func (c *Context) CommitTimestamp(xid uint32) (time.Time, uint32, bool, error) {
	host := c.LiveHost()
	if host == nil {
		return time.Time{}, 0, false, fmt.Errorf("stuntgres: missing commit timestamp scope")
	}
	return host.CommitTimestamp(xid)
}

func (c *Context) LastCommittedTransaction() (uint32, time.Time, uint32, bool, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, time.Time{}, 0, false, fmt.Errorf("stuntgres: missing commit timestamp scope")
	}
	return host.LastCommittedTransaction()
}

func (c *Context) FunctionStatementTime() (time.Time, error) {
	host := c.LiveHost()
	if host == nil {
		return time.Time{}, fmt.Errorf("stuntgres: missing statement time scope")
	}
	// A held cursor keeps its declaring statement's clock.
	if c.Query != nil && c.Query.Source.Authority != nil && c.Query.Source.Authority.Expression != nil {
		return c.Query.Source.Authority.Expression.StatementStart, nil
	}
	return host.StatementStart(), nil
}

func (c *Context) FunctionWallTime() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func (c *Context) FunctionTimeOfDayZone() *sqltype.Zone {
	return c.Input.Zone
}

func (c *Context) FunctionSleepClock(usage sqltype.Usage) (sqltype.Value, error) {
	input := c.Input
	input.TransactionStart = c.FunctionWallTime()
	return sqltype.CurrentDateTime(usage, input)
}

func (c *Context) FunctionSleepArgumentPresent(arg expr.Expr) bool { return arg != nil }

func (c *Context) FunctionUUIDReady() error {
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing UUID session scope")
	}
	return host.FunctionUUIDReady()
}

func (c *Context) FunctionUUIDTime() int64 { return c.LiveHost().FunctionUUIDTime() }
func (c *Context) FunctionUUIDV1(multicast bool) ([16]byte, error) {
	return c.LiveHost().FunctionUUIDV1(multicast)
}

func (c *Context) FunctionUUIDEntropy(bytes []byte) error { _, err := rand.Read(bytes); return err }

func (c *Context) VisibilityInquiry(kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing statement authority")
	}
	return host.VisibilityInquiry(kind, value, result)
}

var _ functions.FTSQueryRuntime = (*Context)(nil)

func (c *Context) RewriteQuerySQL(query sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: ts_rewrite lacks statement authority")
	}
	return host.RewriteQuerySQL(query, text, result)
}

var _ functions.DictionaryRuntime = (*Context)(nil)

func (c *Context) LexizeDictionary(dictionary sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: ts_lexize lacks statement authority")
	}
	return host.LexizeDictionary(dictionary, text, result)
}

var _ functions.FTSConversionRuntime = (*Context)(nil)

func (c *Context) ConvertTextSearch(kind fts.ConversionKind, explicit bool, configuration sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: text search conversion lacks statement authority")
	}
	return host.ConvertTextSearch(c.Context, kind, explicit, configuration, text, result)
}

func (c *Context) ConvertJSONTextSearch(explicit bool, configuration, document, filter sqltype.Value, filtered bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: JSON text search conversion lacks statement authority")
	}
	return host.ConvertJSONTextSearch(c.Context, explicit, configuration, document, filter, filtered, result)
}

func (c *Context) ReplicationSlot(kind functions.ReplicationSlotKind, arity uint8, values [5]sqltype.Value) error {
	host := c.LiveHost()
	if host == nil { return fmt.Errorf("stuntgres: missing replication slot host") }
	return host.ReplicationSlot(kind, arity, values)
}

func (c *Context) AttributeStatistics(clear, variadic bool, values []sqltype.Value) (bool, error) {
	host := c.LiveHost()
	if host == nil { return false, fmt.Errorf("stuntgres: missing attribute statistics host") }
	return host.AttributeStatistics(clear, variadic, values)
}

func (c *Context) RelationStatistics(clear, variadic bool, values []sqltype.Value) (bool, error) {
	host := c.LiveHost()
	if host == nil { return false, fmt.Errorf("stuntgres: missing relation statistics host") }
	return host.RelationStatistics(clear, variadic, values)
}
