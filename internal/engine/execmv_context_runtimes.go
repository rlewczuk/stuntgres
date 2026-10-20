package engine

import (
	"crypto/fips140"
	"crypto/rand"
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"time"
)

// The ACL SQL functions borrow the executing statement's authority through
// this capability. Each inquiry holds e.mu only for its own catalog and role
// reads and caches nothing on the expression or statement.
var _ functions.ACLRuntime = (*EvalContext)(nil)

func (c *EvalContext) ACLAuthority() *sqltype.ACLAuthority {
	host := c.LiveHost()
	if host == nil {
		return nil
	}
	return host.ACLAuthority()
}

func (c *EvalContext) ObjectACL(address ids.ObjectAddress, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: object ACL inquiry lacks statement authority")
	}
	return host.ObjectACL(c, address, result)
}

func (c *EvalContext) RoleMembership(actor *functions.AccessRole, target functions.AccessRole, privilege string) (bool, error) {
	host := c.LiveHost()
	if host == nil {
		return false, fmt.Errorf("stuntgres: access inquiry lacks statement authority")
	}
	return host.RoleMembership(c, actor, target, privilege)
}

func (c *EvalContext) RowSecurityActive(relation functions.AccessRelation) (bool, error) {
	host := c.LiveHost()
	if host == nil {
		return false, fmt.Errorf("stuntgres: access inquiry lacks statement authority")
	}
	return host.RowSecurityActive(c, relation)
}

var _ functions.AdvisoryRuntime = (*EvalContext)(nil)

// Advisory resolves the current statement owner, never a prepared owner.
func (c *EvalContext) Advisory(action functions.AdvisoryAction, mode functions.AdvisoryMode, lifetime functions.AdvisoryLifetime, first, second int64, pair bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, errors.New("stuntgres: missing advisory session scope")
	}
	return host.Advisory(c, action, mode, lifetime, first, second, pair, result)
}

var _ functions.BlockingRuntime = (*EvalContext)(nil)

func (c *EvalContext) BlockingInquiry(kind functions.BlockingKind, pid int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: blocking inquiry lacks statement authority")
	}
	return host.BlockingInquiry(kind, pid, result)
}

var _ functions.CatalogInquiryRuntime = (*EvalContext)(nil)

func (c *EvalContext) CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing catalog role-name snapshot")
	}
	return host.CatalogUserName(oid, result)
}

func (c *EvalContext) CommentInquiry(kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing comment snapshot")
	}
	return host.CommentInquiry(c, kind, values, provenance, result)
}

// Imported parameters remain Execute-owned. This predicate inspects structure
// and availability only, never evaluates a child again or freezes its value.
func OverlapsExecutionConstant(node expr.Expr, c *EvalContext) bool {
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

func (c *EvalContext) DeparseInquiry(signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing deparse catalog snapshot")
	}
	return host.DeparseInquiry(c, signature, values, result)
}

// plannedVolatility inspects the physical surviving tree. Analysis properties
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
		return children(SubscriptChildren(n, &storage)...)
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
	case plan.InstalledSoundexExpr:
		return children(n.Args...)
	case plan.InstalledMetaphoneExpr:
		return children(n.Args...)
	case plan.InstalledLevenshteinExpr:
		return children(n.Args...)
	case plan.InstalledUUIDOsspExpr:
		return n.Procedure.Volatility
	case plan.InstalledCryptoExpr:
		return merge(n.Procedure.Volatility, children(n.Args...))
	case plan.InstalledTextExpr:
		return merge(n.Procedure.Volatility, children(n.Arg))
	case plan.CaseExpr:
		v := children(n.Test, n.Otherwise)
		for _, arm := range n.Arms {
			v = merge(v, children(arm.Condition, arm.Result))
			if n.Test != nil {
				v = session(v, arm.TestPlan.SessionDependent() || arm.Equality.SessionDependent())
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
func (c *EvalContext) EvalInstalledText(n plan.InstalledTextExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if !preparation.ValidInstalledText(n) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed function expression")
	}
	return n.InstalledText.Eval(c)
}

// OpenExtensionPaths lists every ordered version pair of the named extension
// with its update path (NULL when no path exists).
func (c *EvalContext) OpenExtensionPaths(n plan.ExtensionPathsExpr) ([]sqltype.Value, error) {
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

func (c *EvalContext) IndexProperty(kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error) {
	host := c.LiveHost()
	if host == nil {
		return false, false, fmt.Errorf("stuntgres: missing index property catalog snapshot")
	}
	return host.IndexProperty(kind, oid, ordinalValue, property)
}

func (c *EvalContext) LookupObjectAddress(values [3]sqltype.Value) (ids.ObjectAddress, error) {
	host := c.LiveHost()
	if host == nil {
		return ids.ObjectAddress{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return host.LookupObjectAddress(c, values)
}

func (c *EvalContext) IdentifyObject(values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return host.IdentifyObject(c, values, result)
}

func (c *EvalContext) IdentifyObjectAddress(values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return host.IdentifyObjectAddress(c, values, result)
}

func (c *EvalContext) DescribeObject(values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return host.DescribeObject(c, values, result)
}

func (c *EvalContext) FunctionPgcryptoEntropy(bytes []byte) error {
	if err := c.FunctionContext().Err(); err != nil {
		return err
	}
	_, err := rand.Read(bytes)
	return err
}

func (c *EvalContext) FunctionPgcryptoFIPSMode() bool { return fips140.Enabled() }

func (c *EvalContext) FunctionRandomOwner() (expr.RandomOwner, error) {
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
func (c *EvalContext) FunctionRegexExecution() *sqltype.PGRegexExecution {
	if host := c.LiveHost(); host != nil {
		return host.FunctionRegexExecution()
	}
	return &c.PGRegex
}

var _ functions.SequenceRuntime = (*EvalContext)(nil)

// SequenceAuthority installs the evaluation frame (cancellation and any
// active domain-check input) and requires the current statement's authority
// before a sequence call demands its arguments.
func (c *EvalContext) SequenceAuthority() error {
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
func (c *EvalContext) Sequence(request functions.SequenceRequest) (int64, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: sequence call lacks statement authority")
	}
	return host.Sequence(request)
}

type identitySnapshot struct {
	Database, Current, Session, System sqltype.Value
	CurrentMissing, SessionMissing     authorization.PrincipalRef
	CurrentPrincipal                   authorization.PrincipalRef
}

func (c *EvalContext) IdentityValue(kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: identity lacks statement authority")
	}
	return host.IdentityValue(c, kind, result)
}

var _ functions.SessionInquiryRuntime = (*EvalContext)(nil)

func (c *EvalContext) CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: current_schemas lacks statement authority")
	}
	return host.CurrentSchemas(implicit, result)
}

func (c *EvalContext) MyTempNamespace() (uint32, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: namespace inquiry lacks statement authority")
	}
	return host.MyTempNamespace()
}

func (c *EvalContext) OtherTempNamespace(oid uint32) (bool, error) {
	host := c.LiveHost()
	if host == nil {
		return false, fmt.Errorf("stuntgres: namespace inquiry lacks statement authority")
	}
	return host.OtherTempNamespace(oid)
}

var _ functions.TransactionInquiryRuntime = (*EvalContext)(nil)

func (c *EvalContext) CheckTransactionInquiryContext() error { return EvaluationContext(c) }

func (c *EvalContext) RequireTransactionIdentityScope() error {
	if c.LiveHost() == nil {
		return fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return nil
}

func (c *EvalContext) TransactionAge(xid uint32) (int32, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.TransactionAge(xid)
}

func (c *EvalContext) MultiTransactionAge(xid uint32) (int32, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.MultiTransactionAge(xid)
}

func (c *EvalContext) CurrentTransactionID(assign bool) (uint64, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.CurrentTransactionID(assign)
}

func (c *EvalContext) TransactionStatus(full uint64) (string, bool, error) {
	host := c.LiveHost()
	if host == nil {
		return "", false, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.TransactionStatus(full)
}

func (c *EvalContext) TransactionMembers(xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: MultiXact lacks statement scope")
	}
	return host.TransactionMembers(xid, result)
}

func (c *EvalContext) CommitTimestamp(xid uint32) (time.Time, uint32, bool, error) {
	host := c.LiveHost()
	if host == nil {
		return time.Time{}, 0, false, fmt.Errorf("stuntgres: missing commit timestamp scope")
	}
	return host.CommitTimestamp(xid)
}

func (c *EvalContext) LastCommittedTransaction() (uint32, time.Time, uint32, bool, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, time.Time{}, 0, false, fmt.Errorf("stuntgres: missing commit timestamp scope")
	}
	return host.LastCommittedTransaction()
}

func (c *EvalContext) FunctionStatementTime() (time.Time, error) {
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

func (c *EvalContext) FunctionWallTime() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func (c *EvalContext) FunctionTimeOfDayZone() *sqltype.Zone {
	return c.Input.Zone
}

func (c *EvalContext) FunctionSleepClock(usage sqltype.Usage) (sqltype.Value, error) {
	input := c.Input
	input.TransactionStart = c.FunctionWallTime()
	return sqltype.CurrentDateTime(usage, input)
}

func (c *EvalContext) FunctionSleepArgumentPresent(arg expr.Expr) bool { return arg != nil }

func (c *EvalContext) FunctionUUIDReady() error {
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing UUID session scope")
	}
	return host.FunctionUUIDReady()
}

func (c *EvalContext) FunctionUUIDTime() int64 { return c.LiveHost().FunctionUUIDTime() }

func (c *EvalContext) FunctionUUIDEntropy(bytes []byte) error { _, err := rand.Read(bytes); return err }

func (c *EvalContext) VisibilityInquiry(kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing statement authority")
	}
	return host.VisibilityInquiry(kind, value, result)
}
