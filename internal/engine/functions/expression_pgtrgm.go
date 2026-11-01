package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// PgTrgmMembers is the bounded released callable inventory. Its index is the
// sealed installation slot, not a mutable SQL name or current membership index.
var PgTrgmMembers = [...]struct {
	Name       string
	Signature  string
	Result     ids.OID
	Volatility expr.Volatility
	Kernel     uint8 // ordinary, arbitrary extent, whole-word extent
	Reverse    bool
	Distance   bool
	Threshold  string
}{
	{"show_trgm", "25", 1009, expr.Immutable, 0, false, false, ""},
	{"similarity", "25,25", 700, expr.Immutable, 0, false, false, ""},
	{"word_similarity", "25,25", 700, expr.Immutable, 1, false, false, ""},
	{"strict_word_similarity", "25,25", 700, expr.Immutable, 2, false, false, ""},
	{"similarity_op", "25,25", 16, expr.Stable, 0, false, false, "pg_trgm.similarity_threshold"},
	{"word_similarity_op", "25,25", 16, expr.Stable, 1, false, false, "pg_trgm.word_similarity_threshold"},
	{"word_similarity_commutator_op", "25,25", 16, expr.Stable, 1, true, false, "pg_trgm.word_similarity_threshold"},
	{"strict_word_similarity_op", "25,25", 16, expr.Stable, 2, false, false, "pg_trgm.strict_word_similarity_threshold"},
	{"strict_word_similarity_commutator_op", "25,25", 16, expr.Stable, 2, true, false, "pg_trgm.strict_word_similarity_threshold"},
	{"similarity_dist", "25,25", 700, expr.Immutable, 0, false, true, ""},
	{"word_similarity_dist_op", "25,25", 700, expr.Immutable, 1, false, true, ""},
	{"word_similarity_dist_commutator_op", "25,25", 700, expr.Immutable, 1, true, true, ""},
	{"strict_word_similarity_dist_op", "25,25", 700, expr.Immutable, 2, false, true, ""},
	{"strict_word_similarity_dist_commutator_op", "25,25", 700, expr.Immutable, 2, true, true, ""},
}

// PgTrgmProcedureName admits released spellings to catalog discovery only.
// The selected declaration and its sealed member still authorize binding.
func PgTrgmProcedureName(name string) bool {
	for _, member := range PgTrgmMembers {
		if member.Name == name {
			return true
		}
	}
	return false
}

// PgTrgmRuntime borrows typed thresholds from the executing effective settings
// image. The recipe never retains settings, a session, or granted authority.
type PgTrgmRuntime interface {
	PgTrgmThreshold(string) (float64, error)
}

// PgTrgm retains an immutable operation and the actual installed declaration.
// Ordinary selected-call admission owns EXECUTE and reached C initialization.
type PgTrgm struct {
	expr.Info
	Args            []expr.Expr
	Formal          *sqltype.Descriptor
	Ref             ids.ObjectRef
	Member          uint8
	Namespace, Name string
}

func (e PgTrgm) Validate() error {
	if int(e.Member) >= len(PgTrgmMembers) || e.Formal == nil || e.Formal.ID() != sqltype.Text || e.Usage.Type == nil || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" {
		return fmt.Errorf("stuntgres: invalid installed pg_trgm call")
	}
	count := 2
	if e.Member == 0 {
		count = 1
		if !e.Usage.Type.IsArray() || e.Usage.Type.Element() != e.Formal {
			return fmt.Errorf("stuntgres: invalid pg_trgm array descriptor")
		}
	}
	if len(e.Args) != count || ids.OID(e.Usage.Type.ID()) != PgTrgmMembers[e.Member].Result {
		return fmt.Errorf("stuntgres: invalid pg_trgm signature")
	}
	for _, arg := range e.Args {
		if arg == nil || arg.ResultUsage().Type != e.Formal {
			return fmt.Errorf("stuntgres: invalid pg_trgm operand")
		}
	}
	return nil
}

func (e PgTrgm) Children() []expr.Expr { return e.Args }
func (e PgTrgm) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("PgTrgm", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, children
	return e, e.Validate()
}
func (e PgTrgm) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e PgTrgm) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	formal := env.Type(e.Formal)
	changed = changed || formal != e.Formal
	e.Formal = formal
	next, err := e.Rebuild(info, children)
	return next, changed, err
}

func (e PgTrgm) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	null := false
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	left, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Member == 0 {
		keys, err := PgTrgmTrigrams(ctx, left)
		if err != nil {
			return sqltype.Value{}, err
		}
		if len(keys) == 0 {
			return sqltype.OwnedArrayValue(e.Usage.Type, nil, nil)
		}
		elements := make([]sqltype.Value, len(keys))
		for i, key := range keys {
			bytes := [3]byte{byte(key >> 16), byte(key >> 8), byte(key)}
			elements[i] = sqltype.String(e.Formal, string(bytes[:]))
		}
		return sqltype.OwnedArrayValue(e.Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
	}
	right, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	member := PgTrgmMembers[e.Member]
	if member.Reverse {
		left, right = right, left
	}
	var threshold float64
	if member.Threshold != "" {
		runtime, ok := c.(PgTrgmRuntime)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: pg_trgm lacks live settings authority")
		}
		threshold, err = runtime.PgTrgmThreshold(member.Threshold)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	var score float32
	if member.Kernel == 0 {
		score, err = PgTrgmSimilarity(ctx, left, right)
	} else {
		score, err = pgTrgmWordSimilarity(ctx, left, right, member.Kernel == 2, member.Threshold != "", threshold)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if member.Threshold != "" {
		return sqltype.Boolean(e.Usage.Type, float64(score) >= threshold), nil
	}
	if member.Distance {
		score = 1 - score
	}
	return sqltype.Float(e.Usage.Type, float64(score))
}

// Scalar FROM calls emit one value, including typed NULL and an empty array.
// The invocation-private cursor owns only the evaluated datum, never the recipe.
func (e PgTrgm) SourceAdmission() (bool, bool) { return true, false }
func (e PgTrgm) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}

func (e PgTrgm) ValidateStoredProvider() error { return e.Validate() }
func (e PgTrgm) IntrinsicVolatility() expr.Volatility {
	if int(e.Member) >= len(PgTrgmMembers) {
		return expr.Volatile
	}
	return PgTrgmMembers[e.Member].Volatility
}
func (e PgTrgm) PlanningFold() expr.Fold {
	if e.IntrinsicVolatility() != expr.Immutable {
		return expr.FoldNever
	}
	return expr.FoldConstant
}
func (e PgTrgm) StrictNull() expr.Strict       { return expr.StrictAfter }
func (e PgTrgm) PreflightFold() expr.Preflight { return expr.PreflightNullOrEvaluate }
func (e PgTrgm) LooksLikeFunction() bool       { return true }
func (e PgTrgm) ClosureTransparent()           {}
func (e PgTrgm) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: e.Args, Count: len(e.Args)}, true, nil
}
func (e PgTrgm) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(PgTrgm)
	if !ok || e.Formal != o.Formal || e.Ref != o.Ref || e.Member != o.Member || e.RoutineReference != o.RoutineReference || len(e.Args) != len(o.Args) {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e PgTrgm) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, len(e.Args))
	for i, arg := range e.Args {
		args[i] = expr.RenderArg{Expr: arg, Transport: expr.TransportCast, TransportType: e.Formal.Usage()}
	}
	return expr.Rendering{Tree: "PgTrgm " + e.Namespace + "." + e.Name, Inputs: e.Args, Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: args, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
