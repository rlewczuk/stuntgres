package enginetests_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func pgTrgmOwnerProjection(t *testing.T, e *core.Engine, s *session.Session) *plan.ProjectNode {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	query := enginetest.BindSelect(t, e, op, `SELECT show_trgm(a),similarity(a,b),word_similarity(a,b),strict_word_similarity(a,b),similarity_op(a,b),word_similarity_op(a,b),word_similarity_commutator_op(a,b),strict_word_similarity_op(a,b),strict_word_similarity_commutator_op(a,b),similarity_dist(a,b),word_similarity_dist_op(a,b),word_similarity_dist_commutator_op(a,b),strict_word_similarity_dist_op(a,b),strict_word_similarity_dist_commutator_op(a,b),a % b,a <% b,a %> b,a <<% b,a %>> b,a <-> b,a <<-> b,a <->> b,a <<<-> b,a <->>> b,set_limit(0.25::real),show_limit() FROM pgtrgm_owner`)
	project, ok := query.Root.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != 26 {
		t.Fatal("missing complete actual pg_trgm bound projection")
	}
	return project
}

func pgTrgmOwnerCall(t *testing.T, node expr.Expr) functions.PgTrgm {
	t.Helper()
	if selected, ok := node.(plan.SelectedOperatorExpr); ok {
		if selected.RoutineReference != nil || selected.Operator == nil || selected.Ref.Address != selected.Operator.Address() {
			t.Fatal("operator wrapper duplicated routine authority")
		}
		node = selected.Provider
	}
	call, ok := node.(functions.PgTrgm)
	if !ok {
		t.Fatalf("missing genuine pg_trgm provider: %T", node)
	}
	return call
}

func pgTrgmOwnerInstall(t *testing.T, e *core.Engine) *session.Session {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION pg_trgm")
	enginetest.SessionSQL(t, s, "CREATE TABLE pgtrgm_owner(a text,b text)")
	return s
}

func TestPgTrgmExpressionOwnership(t *testing.T) {
	e := engine.New()
	s := pgTrgmOwnerInstall(t, e)
	bound := pgTrgmOwnerProjection(t, e, s)
	view := e.CatalogRead()
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	for _, node := range bound.Expressions {
		call := pgTrgmOwnerCall(t, node)
		if call.Occurrence == 0 || call.Ref.Domain() != view.Owner().Domain || call.Ref.Epoch != view.Epoch() {
			t.Fatal("missing source occurrence or actual catalog provenance")
		}
		if err := call.ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		member := functions.PgTrgmMembers[call.Member]
		result, _ := view.LookupType(sqltype.ID(member.Result))
		if call.Usage.Type != result || len(call.Args) != member.Arity || call.StrictNull() != expr.StrictAfter || call.Properties().Volatility != member.Volatility {
			t.Fatal("bound member lost result generation, arity, strictness or volatility")
		}
		if member.Arity == 0 {
			if call.Formal != nil {
				t.Fatal("zero-argument getter retained a fake formal dependency")
			}
		} else if formal, _ := view.LookupType(member.Formal); call.Formal != formal {
			t.Fatal("bound member lost actual formal generation")
		}
		if call.Member >= 14 && call.PlanningFold() != expr.FoldNever {
			t.Fatal("live threshold API admitted constant folding")
		}
		children := call.Children()
		for _, reconstruct := range []func(expr.Info, []expr.Expr) (expr.Expr, error){call.Rebuild, call.Substitute} {
			rebuilt, err := reconstruct(call.Info, children)
			if err != nil {
				t.Fatal(err)
			}
			next := rebuilt.(functions.PgTrgm)
			if next.Info != call.Info || next.Formal != call.Formal || next.Ref != call.Ref || next.Member != call.Member || !reflect.DeepEqual(next.Args, call.Args) || !call.SameGroup(next, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
				t.Fatal("reconstruction changed occurrence, declaration, formal, properties or ordered operands")
			}
		}
		for _, kind := range []expr.CaptureKind{expr.CaptureFunction, expr.CaptureOperator} {
			captured, ok, err := call.Capture(kind)
			if err != nil || !ok || captured.Count != len(call.Args) || !reflect.DeepEqual(captured.Args, call.Args) {
				t.Fatal("capture lost ordered arguments", err)
			}
		}
		retagged, changed, err := stored.RetagTypes(view, node, identity)
		if err != nil || changed || !reflect.DeepEqual(retagged, node) {
			t.Fatal("identity retag changed actual generations or declaration", changed, err)
		}
		remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !reflect.DeepEqual(remapped, node) {
			t.Fatal("identity remap changed source", err)
		}
		spec, err := call.Render()
		if err != nil || spec.Name != call.Name || spec.Namespace != call.Namespace || len(spec.Args) != len(call.Args) || !spec.Replay.QuoteNamespace {
			t.Fatal("qualified rendering lost source identity", err)
		}
		for _, arg := range spec.Args {
			if arg.Transport != expr.TransportCast || arg.TransportType != call.Formal.Usage() {
				t.Fatal("qualified rendering lost actual member formal")
			}
		}
		for _, mutate := range []func(*functions.PgTrgm){
			func(n *functions.PgTrgm) { n.Member = 255 },
			func(n *functions.PgTrgm) {
				if n.Formal == nil {
					n.Formal, _ = view.LookupType(sqltype.Float4)
				} else {
					d := *n.Formal
					n.Formal = &d
				}
			},
			func(n *functions.PgTrgm) { d := *n.Usage.Type; n.Usage.Type = &d },
			func(n *functions.PgTrgm) {
				if len(n.Args) == 0 {
					n.Args = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: n.Usage}}}
				} else {
					n.Args = nil
				}
			},
			func(n *functions.PgTrgm) { n.Ref.Epoch++ },
			func(n *functions.PgTrgm) { n.Ref.Address.SubID = 1 },
			func(n *functions.PgTrgm) {
				d := *n.RoutineReference.(*catalog.Procedure)
				d.Display.Source = "forged_pgtrgm_source"
				n.RoutineReference = &d
			},
		} {
			bad := call
			mutate(&bad)
			if _, _, err := stored.RetagTypes(view, bad, identity); err == nil {
				t.Fatal("invalid member/formal/result/arity/reference/declaration accepted")
			}
			if !reflect.DeepEqual(call, pgTrgmOwnerCall(t, node)) {
				t.Fatal("rejected reconstruction mutated immutable source")
			}
		}
	}

	pgTrgmLeafOwnership(t, bound)

	// Force genuine immutable work at the established conditional frontier, then
	// inspect the real accepted owner, not a parallel cache or observation hook.
	ast, description := enginetest.PreparedDescription(t, e, s, `SELECT COALESCE(show_trgm('cat'),'{}'::text[]),COALESCE(similarity('a','aa'),0::real)`)
	enginetest.PreparedFoldExecute(t, e, s, ast, &description)
	saved := enginetest.SnapshotFoldDescription(description)
	array := enginetest.PreparedFoldEntry(t, saved, 1009, `{"  c"," ca","at ",cat}`)
	score := enginetest.PreparedFoldEntry(t, saved, 700, "0.25")
	for _, entry := range []exec.FoldConstant{array, score} {
		actual, ok := view.LookupType(entry.Result.Type.ID())
		procedure, genuine := entry.Callable.(*catalog.Procedure)
		if !ok || actual != entry.Result.Type || entry.Value.Type() != actual || !genuine || procedure.Implementation != extension.StockPgTrgm || !procedure.Origin.Valid(procedure.Address()) || len(entry.Inputs) == 0 {
			t.Fatal("accepted fold lost genuine routine, concrete generation or owned inputs")
		}
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &description)
	if !enginetest.FoldBackingReusable(description.Folds) || !enginetest.SamePublishedFold(array, enginetest.PreparedFoldEntry(t, description, 1009, `{"  c"," ca","at ",cat}`)) {
		t.Fatal("second use replaced accepted immutable backing")
	}
	clone := enginetest.CloneFoldDescription(t, description)
	before := enginetest.SnapshotFoldDescription(clone)
	publication, err := core.BeginFoldPublication(&clone, ast, clone.Folds.Environment, view, nil)
	if err != nil || publication == nil {
		t.Fatal("missing actual fold publication candidate", err)
	}
	stale := clone.Folds.Environment
	stale.Epoch++
	if err := publication.Accept(context.Background(), view, stale, clone.Folds.Environment.Epoch); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("stale-generation publication accepted", err)
	}
	if !enginetest.SamePublishedFold(enginetest.PreparedFoldEntry(t, before, 1009, `{"  c"," ca","at ",cat}`), enginetest.PreparedFoldEntry(t, clone, 1009, `{"  c"," ca","at ",cat}`)) || clone.Folds == description.Folds || !enginetest.SamePublishedFold(array, enginetest.PreparedFoldEntry(t, saved, 1009, `{"  c"," ca","at ",cat}`)) {
		t.Fatal("rejected publication or independent owner mutated accepted source payload")
	}
	pgTrgmInitializationOwnership(t, e)
	pgTrgmThresholdOwnership(t, e, s)
	pgTrgmThresholdPublicationOwnership(t, e)
}

func TestPgTrgmProviderIdentity(t *testing.T) {
	e := engine.New()
	s := pgTrgmOwnerInstall(t, e)
	bound := pgTrgmOwnerProjection(t, e, s)
	view := e.CatalogRead()
	for _, node := range bound.Expressions {
		call := pgTrgmOwnerCall(t, node)
		p := call.RoutineReference.(*catalog.Procedure)
		if err := extension.StockPgTrgm.ValidateProcedure(view, p); err != nil {
			t.Fatal("genuine released declaration rejected", err)
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Origin = nil },
			func(p *catalog.Procedure) { o := *p.Origin; o.Declaration = extension.PgTrgmMemberCount; p.Origin = &o },
			func(p *catalog.Procedure) {
				installation := *p.Origin.Installation
				installation.Version = "1.5"
				o := *p.Origin
				o.Installation = &installation
				p.Origin = &o
			},
			func(p *catalog.Procedure) { p.Display.Source = "forged_pgtrgm_source" },
			func(p *catalog.Procedure) { p.Strict = false },
			func(p *catalog.Procedure) { p.Installed.Parallel = 'r' },
		} {
			bad := *p
			mutate(&bad)
			candidate := catalog.CloneState(view.State())
			candidate.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockPgTrgm.ValidateProcedure(e.Catalog.Read(candidate), &bad); err == nil {
				t.Fatal("forged origin/release/slot/source/strictness/parallel declaration accepted")
			}
		}
		if selected, ok := node.(plan.SelectedOperatorExpr); ok {
			if err := catalog.ValidateInstalledClassOrigin(view, selected.Operator); err != nil {
				t.Fatal("genuine operator graph rejected", err)
			}
			bad := *selected.Operator
			bad.Function = view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_trgm"})).Installation.Members[0].ObjectID
			candidate := catalog.CloneState(view.State())
			candidate.SetDescriptor(bad.Address(), &bad)
			if err := catalog.ValidateInstalledClassOrigin(e.Catalog.Read(candidate), &bad); err == nil {
				t.Fatal("operator acquired another sealed member's authority")
			}
		}
	}
	foreign := engine.New()
	fs := pgTrgmOwnerInstall(t, foreign)
	other := pgTrgmOwnerProjection(t, foreign, fs)
	for i, node := range bound.Expressions {
		old, next := pgTrgmOwnerCall(t, node), pgTrgmOwnerCall(t, other.Expressions[i])
		if old.Ref.Address != next.Ref.Address || old.Ref.Domain() == next.Ref.Domain() {
			t.Fatal("equal-OID independent-root witness was not established")
		}
		if err := extension.StockPgTrgm.ValidateProcedure(foreign.CatalogRead(), old.RoutineReference.(*catalog.Procedure)); err == nil {
			t.Fatal("foreign same-OID procedure pointer accepted")
		}
		if _, _, err := stored.RetagTypes(foreign.CatalogRead(), old, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err == nil {
			t.Fatal("foreign same-OID expression authority accepted")
		}
	}
	original := pgTrgmOwnerCall(t, bound.Expressions[1]).RoutineReference.(*catalog.Procedure)
	enginetest.SessionSQL(t, s, "ALTER FUNCTION similarity(text,text) RENAME TO genuine_similarity")
	enginetest.SessionSQL(t, s, "CREATE SCHEMA pgtrgm_moved")
	enginetest.SessionSQL(t, s, "ALTER FUNCTION genuine_similarity(text,text) SET SCHEMA pgtrgm_moved")
	enginetest.SessionSQL(t, s, "ALTER EXTENSION pg_trgm DROP FUNCTION pgtrgm_moved.genuine_similarity(text,text)")
	current := e.CatalogRead().MustProcedure(original.Address())
	if current.Extension != (ids.ObjectAddress{}) || current.Origin.Installation != original.Origin.Installation || current.Display.Source != "similarity" {
		t.Fatal("lawful rename/move/detach replaced immutable released code origin")
	}
	if err := extension.StockPgTrgm.ValidateProcedure(e.CatalogRead(), current); err != nil {
		t.Fatal("detached genuine original rejected", err)
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(e.CatalogRead(), original); err != nil {
		t.Fatal("captured original lost lawful current declaration", err)
	}
}

// Genuine binder recipes run with the real initialized execution context and
// literal-node prologue. Only typed threshold input is substituted here; public
// settings/admission behavior uses the separate shared reference fixture.
type pgTrgmLeafContext struct {
	*exec.Context
	threshold    float64
	thresholdErr error
	trace        *[]string
}

func (c *pgTrgmLeafContext) PgTrgmThreshold(string) (float64, error) {
	if c.trace != nil {
		*c.trace = append(*c.trace, "threshold")
	}
	return c.threshold, c.thresholdErr
}

type pgTrgmLeafOperand struct {
	expr.Info
	value sqltype.Value
	err   error
	trace *[]string
	label string
}

func (e pgTrgmLeafOperand) Eval(expr.Context) (sqltype.Value, error) {
	*e.trace = append(*e.trace, e.label)
	return e.value, e.err
}

// The real leaf runtime consumes this context's existing Err checkpoints.
// The budget admits extraction and a first complete matching extent, but not
// the old full-search path. Cancellation is real and has no wall-clock race.
type pgTrgmLeafWorkContext struct {
	context.Context
	cancel        context.CancelFunc
	checks, limit int
}

func (c *pgTrgmLeafWorkContext) Err() error {
	c.checks++
	if c.checks >= c.limit {
		c.cancel()
	}
	return c.Context.Err()
}
func pgTrgmLeafOwnership(t *testing.T, bound *plan.ProjectNode) {
	t.Helper()
	call := func(slot int, left, right string) functions.PgTrgm {
		n := pgTrgmOwnerCall(t, bound.Expressions[slot])
		values := []string{left, right}
		n.Args = make([]expr.Expr, len(n.Args))
		for i := range n.Args {
			n.Args[i] = plan.LiteralExpr{Info: expr.Info{Usage: n.Formal.Usage()}, Value: sqltype.String(n.Formal, values[i])}
		}
		return n
	}
	ctx := &pgTrgmLeafContext{Context: &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}}
	for i, score := range []float32{1.0 / 4, 1.0 / 2, 1.0 / 3, 1.0 / 4, 1.0 / 4} {
		n := call(4+i, "a", "aa")
		for j, threshold := range []float64{float64(score), math.Nextafter(float64(score), math.Inf(1)), math.Nextafter(float64(score), math.Inf(-1))} {
			ctx.threshold = threshold
			value, err := n.Eval(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got, err := value.Boolean()
			if err != nil || got != (j != 1) {
				t.Fatal("predicate narrowed the typed float64 threshold or lost inclusive float4 equality", i, j, err)
			}
		}
	}
	// Both named backers and actual operator providers must dispatch to the
	// bounded predicate path, while their distance counterparts keep full work.
	for _, slot := range []int{5, 6, 7, 8, 15, 16, 17, 18} {
		n := call(slot, "a", strings.Repeat("a ", 4096))
		member := functions.PgTrgmMembers[n.Member]
		if member.Reverse {
			n.Args[0], n.Args[1] = n.Args[1], n.Args[0]
		}
		parent, cancel := context.WithCancel(t.Context())
		work := &pgTrgmLeafWorkContext{Context: parent, cancel: cancel, limit: 128}
		leaf := &pgTrgmLeafContext{Context: &exec.Context{EvalStatement: exec.EvalStatement{Context: work}}, threshold: 1}
		value, err := n.Eval(leaf)
		contextErr := parent.Err()
		cancel()
		got, boolErr := value.Boolean()
		if err != nil || boolErr != nil || !got || contextErr != nil || work.checks >= work.limit {
			t.Fatal("actual bound predicate retained full-maximum traversal", slot, work.checks, err, boolErr, contextErr)
		}
		distance := call(slot+5, "a", strings.Repeat("a ", 4096))
		if member.Reverse {
			distance.Args[0], distance.Args[1] = distance.Args[1], distance.Args[0]
		}
		parent, cancel = context.WithCancel(t.Context())
		work = &pgTrgmLeafWorkContext{Context: parent, cancel: cancel, limit: 128}
		leaf = &pgTrgmLeafContext{Context: &exec.Context{EvalStatement: exec.EvalStatement{Context: work}}, threshold: 1}
		_, err = distance.Eval(leaf)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("distance unexpectedly used the predicate's bounded traversal", slot, err)
		}
	}
	for _, slot := range []int{4, 5, 6, 7, 8} {
		for _, tc := range []struct {
			null, operandFailure, thresholdFailure bool
		}{
			{}, {null: true}, {null: true, operandFailure: true},
			{operandFailure: true}, {thresholdFailure: true},
		} {
			n := call(slot, "a", "aa")
			var trace []string
			left := sqltype.String(n.Formal, "a")
			if tc.null {
				left = sqltype.Null(n.Formal)
			}
			operandErr, thresholdErr := errors.New("demanded right"), errors.New("live threshold")
			var rightErr, liveErr error
			if tc.operandFailure {
				rightErr = operandErr
			}
			if tc.thresholdFailure {
				liveErr = thresholdErr
			}
			n.Args = []expr.Expr{
				pgTrgmLeafOperand{Info: expr.Info{Usage: n.Formal.Usage()}, value: left, trace: &trace, label: "left"},
				pgTrgmLeafOperand{Info: expr.Info{Usage: n.Formal.Usage()}, value: sqltype.String(n.Formal, "aa"), err: rightErr, trace: &trace, label: "right"},
			}
			leaf := &pgTrgmLeafContext{
				Context:   &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}},
				threshold: 0, thresholdErr: liveErr, trace: &trace,
			}
			value, err := n.Eval(leaf)
			wantTrace := []string{"left", "right"}
			switch {
			case tc.operandFailure:
				if !errors.Is(err, operandErr) {
					t.Fatal("predicate hid a demanded operand failure", slot, err)
				}
			case tc.null:
				if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
					t.Fatal("predicate lost typed strict NULL", slot, err)
				}
			default:
				wantTrace = append(wantTrace, "threshold")
				if tc.thresholdFailure {
					if !errors.Is(err, thresholdErr) {
						t.Fatal("predicate hid live threshold failure", slot, err)
					}
				} else if got, boolErr := value.Boolean(); err != nil || boolErr != nil || !got {
					t.Fatal("non-NULL zero-threshold predicate failed", slot, err, boolErr)
				}
			}
			if !reflect.DeepEqual(trace, wantTrace) {
				t.Fatal("threshold read bypassed NULL/demand or repeated/reordered operands", slot, trace, wantTrace)
			}
		}
	}
	for i, want := range []string{"0.75", "0.5", "0.6666666", "0.75", "0.75"} {
		n := call(9+i, "a", "aa")
		value, err := n.Eval(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Type().Output(context.Background(), value)
		if err != nil || got != want {
			t.Fatal("distance did not subtract the already-rounded float4 score", i, got, want, err)
		}
	}
	word := call(10, "word", "two words")
	value, err := word.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := value.Type().Output(context.Background(), value); err != nil || output != "0.19999999" {
		t.Fatal("distance used algebraically different double subtraction", output, err)
	}
	show := call(0, "cat", "")
	array, err := show.Eval(ctx)
	if err != nil || array.Type() != show.Usage.Type || array.ArrayRank() != 1 || array.ArrayLen() != 4 || array.ArrayDimension(0).Lower != 1 {
		t.Fatal("trigram datum lost actual result generation or array shape", err)
	}
	for i, want := range []string{"  c", " ca", "at ", "cat"} {
		element := array.ArrayElement(i)
		got, err := element.Text()
		if err != nil || element.Type() != show.Formal || got != want {
			t.Fatal("trigram datum lost actual TEXT elements or canonical order", i, err)
		}
	}
	dimensions := array.ArrayDimensions()
	dimensions[0].Lower = 77
	if array.ArrayDimension(0).Lower != 1 {
		t.Fatal("trigram array dimensions alias caller storage")
	}
	empty := call(0, "", "")
	emptyArray, err := empty.Eval(ctx)
	if err != nil || emptyArray.Type() != show.Usage.Type || emptyArray.ArrayRank() != 0 || emptyArray.ArrayLen() != 0 {
		t.Fatal("empty trigram array is not zero-dimensional owned TEXT[]", err)
	}
	if output, err := array.Type().Output(context.Background(), array); err != nil || output != `{"  c"," ca","at ",cat}` {
		t.Fatal("later invocation changed retained trigram array", output, err)
	}
	// Kernel leaf substitutes cover the original text members only. The live
	// REAL/zero-argument APIs use actual statement hosts below.
	for slot := range bound.Expressions[:14] {
		for _, null := range []bool{false, true} {
			n := call(slot, "cat", "cat")
			if null {
				n.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: n.Formal.Usage()}, Value: sqltype.Null(n.Formal)}
			}
			admitted, set := n.SourceAdmission()
			if !admitted || set {
				t.Fatal("scalar function source was rejected or became an SRF", slot)
			}
			cursor, err := n.OpenSource(ctx)
			if err != nil {
				t.Fatal("opening actual scalar function source", slot, err)
			}
			if cursor.Type != n.Usage.Type || cursor.State != nil || cursor.Release != nil || cursor.Values != nil {
				cursor.Close()
				t.Fatal("scalar cursor retained foreign generation, recipe or runtime state", slot)
			}
			value, present, err := cursor.Next(t.Context())
			if err != nil || !present || value.Type() != n.Usage.Type || value.IsNull() != null {
				cursor.Close()
				t.Fatal("scalar source did not emit exactly one correctly typed value", slot, null, err)
			}
			eof, present, err := cursor.Next(t.Context())
			if err != nil || present || !eof.IsNull() || eof.Type() != n.Usage.Type || !cursor.Exhausted || cursor.Value.Type() != nil {
				cursor.Close()
				t.Fatal("scalar source lost typed EOF or retained its consumed payload", slot, null, err)
			}
			cursor.Close()
		}
	}
	for _, failure := range []bool{false, true} {
		var trace []string
		n := call(1, "a", "aa")
		sibling := errors.New("required right operand")
		var rightError error
		if failure {
			rightError = sibling
		}
		n.Args = []expr.Expr{
			pgTrgmLeafOperand{Info: expr.Info{Usage: n.Formal.Usage()}, value: sqltype.Null(n.Formal), trace: &trace, label: "left"},
			pgTrgmLeafOperand{Info: expr.Info{Usage: n.Formal.Usage()}, value: sqltype.String(n.Formal, "aa"), err: rightError, trace: &trace, label: "right"},
		}
		result, err := n.Eval(ctx)
		if !reflect.DeepEqual(trace, []string{"left", "right"}) || failure && !errors.Is(err, sibling) || !failure && (err != nil || !result.IsNull() || result.Type() != n.Usage.Type) {
			t.Fatal("strict NULL suppressed, repeated or reordered a demanded sibling", trace, err)
		}
	}
}

func pgTrgmInitializationOwnership(t *testing.T, e *core.Engine) {
	t.Helper()
	loaded := func(e *core.Engine, s *session.Session) bool {
		s.Settings.Mu.Lock()
		defer s.Settings.Mu.Unlock()
		_, active := s.Settings.EffectiveDefinitionLocked(e.Settings, "pg_trgm.similarity_threshold")
		return active
	}
	fresh := enginetest.StartSession(t, e, nil)
	if loaded(e, fresh) {
		t.Fatal("another physical session's installation initialized this owner")
	}
	enginetest.SessionSQL(t, fresh, "CREATE EXTENSION IF NOT EXISTS pg_trgm")
	if loaded(e, fresh) {
		t.Fatal("skipped CREATE initialized physical settings")
	}
	nullAST, nullDescription := enginetest.PreparedDescription(t, e, fresh, "SELECT similarity(NULL::text,'a')")
	if loaded(e, fresh) {
		t.Fatal("Prepare/Describe initialized physical settings")
	}
	enginetest.PreparedFoldExecute(t, e, fresh, nullAST, &nullDescription)
	if loaded(e, fresh) {
		t.Fatal("erased strict literal-NULL initialized physical settings")
	}
	residualAST, residualDescription := enginetest.PreparedDescription(t, e, fresh, "SELECT similarity(a,b) FROM pgtrgm_owner")
	if loaded(e, fresh) {
		t.Fatal("row-dependent description initialized physical settings")
	}
	enginetest.PreparedFoldExecute(t, e, fresh, residualAST, &residualDescription)
	if !loaded(e, fresh) {
		t.Fatal("zero-row residual expression initialization never reached the physical owner")
	}
	foldSession := enginetest.StartSession(t, e, nil)
	foldAST, foldDescription := enginetest.PreparedDescription(t, e, foldSession, "SELECT similarity('a','aa')")
	if loaded(e, foldSession) {
		t.Fatal("literal description performed the released C fold")
	}
	enginetest.PreparedFoldExecute(t, e, foldSession, foldAST, &foldDescription)
	if !loaded(e, foldSession) {
		t.Fatal("actual immutable execution/fold did not initialize its physical owner")
	}
	executeError := func(e *core.Engine, s *session.Session, query, state string) {
		t.Helper()
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = enginetest.ExecuteFrame(e, op, enginetest.Statement(t, query), nil)
		op.Done()
		enginetest.State(t, err, state)
	}
	setterEngine := engine.New()
	setterCollision := enginetest.StartSession(t, setterEngine, nil)
	enginetest.SessionSQL(t, setterCollision, "CREATE FUNCTION set_limit(real) RETURNS real LANGUAGE SQL AS 'SELECT $1'")
	executeError(setterEngine, setterCollision, "CREATE EXTENSION pg_trgm", "42723")
	if loaded(setterEngine, setterCollision) || setterEngine.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_trgm"}) != (ids.ObjectAddress{}) {
		t.Fatal("duplicate first setter reached C validation or published the failed catalog stage")
	}
	collisionEngine := engine.New()
	collision := enginetest.StartSession(t, collisionEngine, nil)
	enginetest.SessionSQL(t, collision, "CREATE FUNCTION show_trgm(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'")
	executeError(collisionEngine, collision, "CREATE EXTENSION pg_trgm", "42723")
	if !loaded(collisionEngine, collision) || collisionEngine.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_trgm"}) != (ids.ObjectAddress{}) {
		t.Fatal("later declaration collision failed to retain physical loading independently of atomic catalog rollback")
	}
	earlyEngine := engine.New()
	early := enginetest.StartSession(t, earlyEngine, nil)
	executeError(earlyEngine, early, "CREATE EXTENSION pg_trgm SCHEMA pgtrgm_missing", "3F000")
	if loaded(earlyEngine, early) {
		t.Fatal("early schema rejection reached the C validation boundary")
	}
}

func pgTrgmThresholdOwnership(t *testing.T, e *core.Engine, s *session.Session) {
	t.Helper()
	ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM show_limit()")
	setAST, setDescription := enginetest.PreparedDescription(t, e, s, "SELECT * FROM set_limit(0.25::real)")
	read := func(result core.Result, want float32) {
		t.Helper()
		defer result.Rows.Close()
		var row [1]sqltype.Value
		present, err := result.Rows.Next(t.Context(), row[:])
		real, _ := e.CatalogRead().LookupType(sqltype.Float4)
		if err != nil || !present || row[0].Type() != real || row[0].IsNull() {
			t.Fatal("live threshold result lost actual REAL generation", present, err)
		}
		got, err := row[0].Float()
		if err != nil || got != float64(want) {
			t.Fatal("prepared API retained threshold/session input instead of live typed value", got, want, err)
		}
		if more, err := result.Rows.Next(t.Context(), row[:]); err != nil || more {
			t.Fatal("threshold scalar source did not emit exactly once", more, err)
		}
	}
	execute := func(s *session.Session, setter bool, want float32) {
		t.Helper()
		var result core.Result
		var err error
		if setter {
			result, err = enginetest.PreparedExecute(e, s, setAST, &setDescription)
		} else {
			result, err = enginetest.PreparedExecute(e, s, ast, &description)
		}
		if err != nil {
			t.Fatal(err)
		}
		read(result, want)
	}
	execute(s, false, .3)
	execute(s, true, .25)
	execute(s, false, .25)
	other := enginetest.StartSession(t, e, nil)
	execute(other, false, .3)
	execute(other, true, .25)
	enginetest.SessionSQL(t, s, "SET pg_trgm.similarity_threshold='0.75'")
	execute(s, false, .75)
	execute(other, false, .25)
	for _, d := range []*core.Description{&description, &setDescription} {
		for _, entry := range d.Folds.Current().Entries {
			if p, ok := entry.Callable.(*catalog.Procedure); ok && p.Implementation == extension.StockPgTrgm {
				t.Fatal("live API published a retained constant or session authority")
			}
		}
	}
}

func pgTrgmThresholdPublicationOwnership(t *testing.T, e *core.Engine) {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn)
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("trgm_threshold_owned", "SELECT COALESCE(similarity('a','aa'),0::real),set_limit(0.25::real),show_limit()", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("trgm_threshold_owned")
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("trgm_threshold_portal", "trgm_threshold_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	binding := registry.Binding("trgm_threshold_portal")
	if definition == nil || binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Folds == nil {
		t.Fatal("missing actual accepted threshold definition and portal graph")
	}
	graph, folds := binding.Resource.Root, binding.Folds
	setter := pgTrgmOwnerCall(t, graph.Expressions[1])
	getter := pgTrgmOwnerCall(t, graph.Expressions[2])
	real, _ := e.CatalogRead().LookupType(sqltype.Float4)
	for i, call := range []functions.PgTrgm{setter, getter} {
		p := call.RoutineReference.(*catalog.Procedure)
		volatility, parallel, arity := expr.Volatile, byte('u'), 1
		if i == 1 {
			volatility, parallel, arity = expr.Stable, 's', 0
		}
		if call.Usage.Type != real || len(call.Args) != arity || call.Properties().Volatility != volatility || !p.Strict || p.Installed.Parallel != parallel || call.PlanningFold() != expr.FoldNever || i == 0 && call.Formal != real || i == 1 && call.Formal != nil {
			t.Fatal("accepted graph lost exact live API signature, generation or declaration")
		}
	}
	owner := definition.Description.Folds
	accepted := owner.Current()
	if len(accepted.Entries) == 0 {
		t.Fatal("immutable companion did not publish real owned fold backing")
	}
	for _, entry := range accepted.Entries {
		if p, ok := entry.Callable.(*catalog.Procedure); ok && (p.Display.Source == "set_limit" || p.Display.Source == "show_limit") {
			t.Fatal("binding captured a live threshold API as an immutable value")
		}
	}
	// Binding the demanded setter must not execute it, even while its immutable
	// companion activates the actual owner and publishes a retained constant.
	s.Settings.Mu.Lock()
	threshold, err := s.Settings.PgTrgmThresholdLocked(e.Settings, "pg_trgm.similarity_threshold")
	s.Settings.Mu.Unlock()
	if err != nil || threshold != float64(float32(.3)) {
		t.Fatal("parse/bind mutated the live owner", threshold, err)
	}
	publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, owner.Environment, e.CatalogRead(), nil)
	if err != nil || publication == nil {
		t.Fatal("missing private publication for actual API-bearing definition", err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	err = publication.Accept(canceled, e.CatalogRead(), owner.Environment, e.CatalogRead().Epoch())
	current := owner.Current()
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(current, accepted) || binding.Resource.Root != graph || binding.Folds != folds || !reflect.DeepEqual(pgTrgmOwnerCall(t, graph.Expressions[1]), setter) || !reflect.DeepEqual(pgTrgmOwnerCall(t, graph.Expressions[2]), getter) {
		t.Fatal("rejected publication changed accepted API graph, generation or backing", err)
	}
	op, _, err := s.AcquireCommand(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
