package core_test

import (
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const preparedFoldPoint = "SELECT (point '0.10000000000000002,1')::text AS p"

func preparedFoldExecute(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, d *core.Description) {
	t.Helper()
	r, err := sequencePreparedExecute(e, s, ast, d)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != nil {
		r.Rows.Close()
	}
}

func TestPreparedFoldInvalidation(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	// Operator arrays are lookup-only identities, not concrete input types.
	// A supported composite exercises compound operator operand collection.
	enginetest.SessionSQL(t, s, "CREATE TYPE fold_operator_operand AS (o regoperator)")
	// Collection, not merely projection, keeps operand-only and executable
	// purposes distinct and unions a genuine executable use at the same address.
	for _, operand := range []string{
		"'+(integer,integer)'::regoperator",
		`'("+(integer,integer)")'::fold_operator_operand`,
		"'abs(integer)'::regprocedure",
		`'{"abs(integer)"}'::regprocedure[]`,
	} {
		_, operandDescription := sequencePreparedDescription(t, e, s, "SELECT "+operand)
		for _, dep := range operandDescription.Analysis.Dependencies {
			if dep.Ref.Address.ClassID == ids.TypeClassID && dep.Ref.Address.ObjectID == ids.OID(sqltype.Int4) {
				if dep.Purpose != catalog.OperandDependency {
					t.Fatal("operand type acquired executable purpose", operand)
				}
			}
		}
		for _, projection := range []string{operand + ",7::integer", "7::integer," + operand} {
			_, description := sequencePreparedDescription(t, e, s, "SELECT "+projection)
			executableSeen := false
			for _, dep := range description.Analysis.Dependencies {
				if dep.Ref.Address.ClassID == ids.TypeClassID && dep.Ref.Address.ObjectID == ids.OID(sqltype.Int4) {
					executableSeen = dep.Purpose == catalog.DefinitionDependency
				}
			}
			if !executableSeen {
				t.Fatal("dependency-purpose collection lost executable use", projection)
			}
		}
		operandSeen := false
		for _, dep := range operandDescription.Analysis.Dependencies {
			if dep.Ref.Address.ClassID == ids.TypeClassID && dep.Ref.Address.ObjectID == ids.OID(sqltype.Int4) {
				operandSeen = true
			}
		}
		if !operandSeen {
			t.Fatal("dependency-purpose collection lost operand use", operand)
		}
	}
	enginetest.SessionSQL(t, s, "CREATE TABLE fold_operand(v int)")
	enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
	ast, d := sequencePreparedDescription(t, e, s, preparedFoldPoint)
	preparedFoldExecute(t, e, s, ast, &d)
	original := snapshotFoldDescription(d)
	preparedFoldEntry(t, d, sqltype.Text, "(0.10000000000000002,1)")
	table := primaryKeyUnitRelation(t, e, "fold_operand")
	ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), table.Address())
	// One genuine catalog address has two possible purposes. The analysis
	// manifest always retains it; only its executable projection differs.
	incidental := *d.Analysis
	incidental.Dependencies = append(append([]catalog.ExpressionDependency(nil), d.Analysis.Dependencies...), catalog.ExpressionDependency{Ref: ref, Revision: table.Revision(), Purpose: catalog.OperandDependency})
	d.Analysis, d.Folds.Analysis = &incidental, &incidental
	enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
	enginetest.SessionSQL(t, s, "ALTER TABLE fold_operand ADD unused int")
	if preparedRebindValid(t, e, s, d.Analysis) {
		t.Fatal("incidental dependency was suppressed from analysis")
	}
	preparedFoldExecute(t, e, s, ast, &d)
	if d.Analysis == &incidental || &d.Folds.Current().Entries[0] != &original.Accepted.Entries[0] || d.Folds.Current().Generation != original.Accepted.Generation {
		t.Fatal("incidental revision did not reanalyze while preserving the exact fold")
	}
	preparedFoldEntry(t, d, sqltype.Text, "(0.10000000000000002,1)")
	// A planning-only executable dependency at the exact same address retires
	// folds independently, without making the ordinary analysis invalid.
	table = primaryKeyUnitRelation(t, e, "fold_operand")
	planning := d.Folds.Begin()
	planning.Candidate.Dependencies = append(append([]exec.FoldDependency(nil), planning.Candidate.Dependencies...), exec.FoldDependency{Ref: ref, Descriptor: table, Stamp: table.Revision()})
	planning.Candidate.DependenciesPrivate = true
	acceptedPlanning, err := d.Folds.Accept(&planning)
	if err != nil {
		t.Fatal(err)
	}
	planningGeneration := acceptedPlanning.Generation
	enginetest.SessionSQL(t, s, "ALTER TABLE fold_operand ADD other int")
	if !preparedRebindValid(t, e, s, d.Analysis) {
		t.Fatal("planning-only dependency leaked into analysis lifetime")
	}
	preparedFoldExecute(t, e, s, ast, &d)
	if &d.Folds.Current().Entries[0] == &original.Accepted.Entries[0] || d.Folds.Current().Generation == planningGeneration {
		t.Fatal("same-address executable purpose failed to retire")
	}
	preparedFoldEntry(t, d, sqltype.Text, "(0.1,1)")
	// Root retirement is monotonic through a failed shape refresh and restored
	// environment. Old immutable groups stay readable, never publication-eligible.
	enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
	discardUnitApply(t, s, parser.DiscardPlans)
	preparedFoldExecute(t, e, s, ast, &d)
	before := snapshotFoldDescription(d)
	discardUnitApply(t, s, parser.DiscardPlans)
	saved := d.Results
	d.Results = nil
	if _, err := sequencePreparedExecute(e, s, ast, &d); err == nil {
		t.Fatal("missing signature failure")
	}
	d.Results = saved
	if foldRootsReusable(d.Folds) || d.Folds.Current().Generation <= before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("failed refresh resurrected or changed old group")
	}
	assertFoldReuseRetirement(t, d.Folds, false)
	enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
	preparedFoldExecute(t, e, s, ast, &d)
	preparedFoldEntry(t, d, sqltype.Text, "(0.1,1)")
	preparedFoldEntry(t, before, sqltype.Text, "(0.10000000000000002,1)")
	t.Run("RealLifetimeCauses", func(t *testing.T) {
		for _, cause := range []string{"relation", "path", "namespace", "native"} {
			t.Run(cause, func(t *testing.T) {
				engine := rootengine.New()
				owner := enginetest.StartSession(t, engine, nil)
				for _, q := range []string{"CREATE SCHEMA fold_a", "CREATE SCHEMA fold_b", "CREATE TABLE fold_a.t(v int)", "INSERT INTO fold_a.t VALUES(1)", "SET search_path TO fold_a,fold_b", "SET extra_float_digits=3"} {
					enginetest.SessionSQL(t, owner, q)
				}
				source := preparedFoldPoint
				if cause == "relation" {
					source += " FROM t"
				}
				statement, description := sequencePreparedDescription(t, engine, owner, source)
				preparedFoldExecute(t, engine, owner, statement, &description)
				old := snapshotFoldDescription(description)
				enginetest.SessionSQL(t, owner, "SET extra_float_digits=0")
				switch cause {
				case "relation":
					enginetest.SessionSQL(t, owner, "ALTER TABLE fold_a.t ADD spare int")
				case "path":
					enginetest.SessionSQL(t, owner, "SET search_path TO fold_b,fold_a")
				case "namespace":
					enginetest.SessionSQL(t, owner, "CREATE SCHEMA fold_callback")
				case "native":
					if err := rootengine.Reset(context.Background(), engine); err != nil {
						t.Fatal(err)
					}
					enginetest.SessionSQL(t, owner, "SET extra_float_digits=0")
				}
				preparedFoldExecute(t, engine, owner, statement, &description)
				if &old.Accepted.Entries[0] == &description.Folds.Current().Entries[0] || old.Accepted.Generation == description.Folds.Current().Generation {
					t.Fatal("real lifetime cause retained root")
				}
				preparedFoldEntry(t, description, sqltype.Text, "(0.1,1)")
				preparedFoldEntry(t, old, sqltype.Text, "(0.10000000000000002,1)")
			})
		}
	})
	t.Run("RepresentedRLS", func(t *testing.T) {
		engine := rootengine.New()
		owner := enginetest.StartSession(t, engine, nil)
		for _, q := range []string{"CREATE TABLE fold_rls(v int)", "INSERT INTO fold_rls VALUES(1)", "ALTER TABLE fold_rls ENABLE ROW LEVEL SECURITY", "SET extra_float_digits=3"} {
			enginetest.SessionSQL(t, owner, q)
		}
		statement, description := sequencePreparedDescription(t, engine, owner, preparedFoldPoint+" FROM fold_rls")
		preparedFoldExecute(t, engine, owner, statement, &description)
		if !description.Folds.RoleSensitive {
			t.Fatal("represented RLS environment was not collected")
		}
		old := snapshotFoldDescription(description)
		enginetest.SessionSQL(t, owner, "SET extra_float_digits=0")
		enginetest.SessionSQL(t, owner, "SET row_security=off")
		preparedFoldExecute(t, engine, owner, statement, &description)
		if &description.Folds.Current().Entries[0] == &old.Accepted.Entries[0] || description.Folds.Environment.RowSecurity {
			t.Fatal("represented row-security environment did not retire")
		}
		preparedFoldEntry(t, description, sqltype.Text, "(0.1,1)")
	})
	t.Run("SemanticProjection", func(t *testing.T) {
		engine := rootengine.New()
		owner := enginetest.StartSession(t, engine, nil)
		enginetest.SessionSQL(t, owner, "CREATE DOMAIN fold_semantic AS integer")
		enginetest.SessionSQL(t, owner, "CREATE CAST (xid AS real) WITHOUT FUNCTION")
		_, description := sequencePreparedDescription(t, engine, owner, "SELECT abs(-7::integer)")
		op, err := owner.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		engine.Mu.Lock()
		defer engine.Mu.Unlock()
		auth, err := engine.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := engine.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		baseline := description.Analysis
		if !baseline.FoldsValid(auth.Catalog, environment) {
			t.Fatal("initial semantic projection invalid")
		}
		var callable *catalog.Procedure
		for _, captured := range baseline.FoldCallables {
			callable = captured
			break
		}
		if callable == nil {
			t.Fatal("selected callable implementation was not collected")
		}
		t.Run("SupportPurpose", func(t *testing.T) {
			ref := environment.Domain.Ref(auth.Catalog.Epoch(), callable.Address())
			revised := *callable
			revised.ObjectRevision = callable.Revision() + 1
			if !exec.SameRoutineDeclaration(callable, &revised) {
				t.Fatal("support fixture changed the immutable routine declaration")
			}
			state := catalog.CloneState(auth.Catalog.State())
			state.SetDescriptor(revised.Address(), &revised)
			current := engine.Catalog.Read(state)
			if core.DefinitionStamp(&revised, environment) == core.DefinitionStamp(callable, environment) {
				t.Fatal("support fixture did not change the effective stamp")
			}
			for _, supportFirst := range []bool{false, true} {
				purposes := []catalog.DependencyPurpose{catalog.OperandDependency, catalog.DefinitionDependency, catalog.AggregateSupportDependency}
				if supportFirst {
					purposes = []catalog.DependencyPurpose{catalog.AggregateSupportDependency, catalog.DefinitionDependency, catalog.OperandDependency}
				}
				dependencies := make([]catalog.ExpressionDependency, len(purposes))
				for i, purpose := range purposes {
					dependencies[i] = catalog.ExpressionDependency{Ref: ref, Revision: callable.Revision(), Purpose: purpose}
				}
				collected, err := core.CollectAnalysis(auth.Catalog, environment, core.AnalysisInput{Binding: analyze.Binding{Dependencies: dependencies}})
				if err != nil {
					t.Fatal(err)
				}
				want := []catalog.DependencyPurpose{catalog.DefinitionDependency, catalog.AggregateSupportDependency}
				if supportFirst {
					want[0], want[1] = want[1], want[0]
				}
				if len(collected.Dependencies) != len(want) {
					t.Fatal("support and written lanes collapsed", collected.Dependencies)
				}
				for i, dependency := range collected.Dependencies {
					if dependency.Ref != ref || dependency.Revision != core.DefinitionStamp(callable, environment) || dependency.Purpose != want[i] {
						t.Fatal("capture identity, stamp or purpose order changed", collected.Dependencies)
					}
				}
				if !collected.Valid(auth.Catalog, environment) || !collected.FoldsValid(auth.Catalog, environment) {
					t.Fatal("unchanged support captures were invalid")
				}
				if collected.Valid(current, environment) || collected.FoldsValid(current, environment) {
					t.Fatal("same-declaration fallback concealed a changed support stamp")
				}
				written, err := core.CollectAnalysis(auth.Catalog, environment, core.AnalysisInput{Binding: analyze.Binding{Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: callable.Revision(), Purpose: catalog.DefinitionDependency}}}})
				if err != nil {
					t.Fatal(err)
				}
				if written.Valid(current, environment) || !written.FoldsValid(current, environment) {
					t.Fatal("written presentation-only change lost its distinct fold projection")
				}
			}
		})
		for _, semantic := range []bool{false, true} {
			revised := *callable
			revised.ObjectRevision++
			if semantic {
				revised.Volatility = expr.Volatile
			}
			state := catalog.CloneState(auth.Catalog.State())
			state.SetDescriptor(revised.Address(), &revised)
			current := auth
			current.Catalog = engine.Catalog.Read(state)
			valid := baseline.FoldsValid(current.Catalog, environment)
			if valid == semantic {
				t.Fatal("callable presentation and implementation revisions were conflated", semantic)
			}
			if baseline.Valid(current.Catalog, environment) {
				t.Fatal("callable analysis dependency disappeared")
			}
		}
		integer := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
		presentation := *integer
		// Builtin types may store zero for effective revision one. Increment
		// the public stamp, not the raw field, to exercise a real revision.
		presentation.ObjectRevision = integer.Revision() + 1
		state := catalog.CloneState(auth.Catalog.State())
		state.SetDescriptor(presentation.Address(), &presentation)
		current := auth
		current.Catalog = engine.Catalog.Read(state)
		if !baseline.FoldsValid(current.Catalog, environment) || baseline.Valid(current.Catalog, environment) {
			t.Fatal("unchanged codec revision conflated executable and analysis dependencies")
		}
		// A distinct executable codec must retire the same dependency even
		// when its catalog address and every presentation field are unchanged.
		codec := *integer.SQLType
		presentation.SQLType = &codec
		state = catalog.CloneState(auth.Catalog.State())
		state.SetDescriptor(presentation.Address(), &presentation)
		current.Catalog = engine.Catalog.Read(state)
		if baseline.FoldsValid(current.Catalog, environment) {
			t.Fatal("changed executable codec survived semantic projection")
		}
		addresses := auth.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: integer.Namespace(), Name: "fold_semantic"})
		// User domains live in public rather than pg_catalog.
		if len(addresses) == 0 {
			for _, dep := range engine.Catalog.Current().Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}) {
				addresses = auth.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: dep, Name: "fold_semantic"})
			}
		}
		if len(addresses) != 1 {
			t.Fatal("missing real domain dependency")
		}
		domain := auth.Catalog.MustType(addresses[0])
		domainAnalysis := *baseline
		domainAnalysis.Dependencies = append(append([]catalog.ExpressionDependency(nil), baseline.Dependencies...), catalog.ExpressionDependency{Ref: engine.Catalog.Domain.Ref(auth.Catalog.Epoch(), domain.Address()), Revision: domain.EffectiveDomainPlanStamp()})
		revisedDomain := *domain
		revisedDomain.ObjectRevision++
		revisedDomain.DomainPlanStamp = domain.EffectiveDomainPlanStamp() + 1
		state = catalog.CloneState(auth.Catalog.State())
		state.SetDescriptor(revisedDomain.Address(), &revisedDomain)
		current.Catalog = engine.Catalog.Read(state)
		if domainAnalysis.FoldsValid(current.Catalog, environment) {
			t.Fatal("same codec concealed changed domain semantics")
		}
		conversion, ok := auth.Catalog.Cast(sqltype.Xid, sqltype.Float4)
		if !ok {
			t.Fatal("missing represented declared coercion")
		}
		castAnalysis := *baseline
		castAnalysis.Casts = map[catalog.CastPair]*catalog.Cast{conversion.Pair(): conversion}
		revisedCast := *conversion
		revisedCast.ObjectRevision++
		state = catalog.CloneState(auth.Catalog.State())
		state.SetDescriptor(revisedCast.Address(), &revisedCast)
		current.Catalog = engine.Catalog.Read(state)
		if castAnalysis.FoldsValid(current.Catalog, environment) {
			t.Fatal("changed selected cast remained executable")
		}
	})
	t.Run("PrivateRollback", func(t *testing.T) {
		engine := rootengine.New()
		owner := enginetest.StartSession(t, engine, nil)
		for _, q := range []string{"SET extra_float_digits=3", "BEGIN", "SAVEPOINT before_source", "CREATE TEMP TABLE fold_private(v int)", "INSERT INTO fold_private VALUES(1)"} {
			enginetest.SessionSQL(t, owner, q)
		}
		statement, description := sequencePreparedDescription(t, engine, owner, preparedFoldPoint+" FROM pg_temp.fold_private")
		preparedFoldExecute(t, engine, owner, statement, &description)
		old := snapshotFoldDescription(description)
		enginetest.SessionSQL(t, owner, "ROLLBACK TO before_source")
		for _, q := range []string{"CREATE TEMP TABLE fold_private(v int)", "INSERT INTO fold_private VALUES(1)", "SET extra_float_digits=0"} {
			enginetest.SessionSQL(t, owner, q)
		}
		preparedFoldExecute(t, engine, owner, statement, &description)
		if &old.Accepted.Entries[0] == &description.Folds.Current().Entries[0] || old.Accepted.Generation == description.Folds.Current().Generation {
			t.Fatal("rolled-back private context certified old folds")
		}
		preparedFoldEntry(t, description, sqltype.Text, "(0.1,1)")
		preparedFoldEntry(t, old, sqltype.Text, "(0.10000000000000002,1)")
		enginetest.SessionSQL(t, owner, "ROLLBACK")
	})
}

func TestPreparedFoldPublication(t *testing.T) {
	t.Run("ExactAttemptDescription", func(t *testing.T) {
		for _, change := range []string{"ambient_description", "path_after_admission", "namespace_after_admission"} {
			t.Run(change, func(t *testing.T) {
				e := rootengine.New()
				s := enginetest.StartSession(t, e, nil)
				ast, d := sequencePreparedDescription(t, e, s, preparedFoldPoint)
				op, err := s.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(nil)
				if _, err = scope.Describe(ast); err != nil {
					t.Fatal(err)
				}
				scope.Expected = &d
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				environment, err := e.PreparedEnvironmentLocked(auth, true)
				if err != nil { e.Mu.Unlock(); t.Fatal(err) }
				attempt, err := core.BeginFoldPublication(&d, ast, environment, auth.Catalog, nil)
				if err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				switch change {
				case "ambient_description":
					other := d
					stale := *d.Analysis
					stale.Epoch++
					other.Analysis = &stale
					scope.Expected = &other
				case "path_after_admission":
					attempt.Environment.Path = sequence.Environment{}
				case "namespace_after_admission":
					attempt.Environment.Namespace++
				}
				err = attempt.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
				e.Mu.Unlock()
				if change == "ambient_description" {
					if err != nil || d.Folds.Analysis != d.Analysis || !attempt.Group.Sealed {
						t.Fatal("ambient description replaced exact publication owner", err)
					}
					assertFoldReuseRetirement(t, d.Folds, true)
				} else if !errors.Is(err, ids.ErrCatalogReplaced) || attempt.Group.Sealed {
					t.Fatal("post-admission environment change published folds", err)
				}
				if change != "ambient_description" {
					assertFoldReuseRetirement(t, d.Folds, false)
				}
			})
		}
	})
	t.Run("AnalysisBeforeImport", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE fold_import(v int)")
		ast, d := sequencePreparedDescription(t, e, s, "SELECT v FROM fold_import WHERE v=$1")
		before := d.Analysis
		enginetest.SessionSQL(t, s, "ALTER TABLE fold_import ADD spare int")
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &d
		failure := errors.New("import rejected")
		_, err = scope.Execute(ast, 1, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
			return failure
		})
		err = scope.Finish(err)
		if !errors.Is(err, failure) || d.Analysis == before || d.Folds.Current().Generation != 0 || d.Folds.Current().Entries != nil {
			t.Fatal("import failure conflated analysis/fold publications", err)
		}
		assertFoldReuseRetirement(t, d.Folds, false)
	})
	t.Run("PlanningBeforeExecution", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE fold_runtime(v int)", "INSERT INTO fold_runtime VALUES(0)", "SET extra_float_digits=3"} {
			enginetest.SessionSQL(t, s, q)
		}
		ast, d := sequencePreparedDescription(t, e, s, "SELECT (point '0.10000000000000002,1')::text AS p,1/v AS n FROM fold_runtime")
		_, err := sequencePreparedExecute(e, s, ast, &d)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || !foldRootsReusable(d.Folds) {
			t.Fatal("execution failure undid accepted fold planning", err)
		}
		before := snapshotFoldDescription(d)
		preparedFoldEntry(t, d, sqltype.Text, "(0.10000000000000002,1)")
		enginetest.SessionSQL(t, s, "UPDATE fold_runtime SET v=1")
		enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
		preparedFoldExecute(t, e, s, ast, &d)
		if &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
			t.Fatal("retry did not retain accepted pre-execution group")
		}
	})
	t.Run("FinalAuthority", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE USER fold_reader", "CREATE TABLE fold_authority(v int)", "INSERT INTO fold_authority VALUES(1)", "GRANT SELECT ON fold_authority TO fold_reader", "SET SESSION AUTHORIZATION fold_reader", "SET extra_float_digits=3"} {
			enginetest.SessionSQL(t, s, q)
		}
		ast, d := sequencePreparedDescription(t, e, s, preparedFoldPoint+" FROM fold_authority")
		preparedFoldExecute(t, e, s, ast, &d)
		before := snapshotFoldDescription(d)
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE SELECT ON fold_authority FROM fold_reader", "SET SESSION AUTHORIZATION fold_reader", "SET extra_float_digits=0", "DISCARD PLANS"} {
			enginetest.SessionSQL(t, s, q)
		}
		_, err := sequencePreparedExecute(e, s, ast, &d)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || foldRootsReusable(d.Folds) || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
			t.Fatal("final authority rejection published a private group", err)
		}
		assertFoldReuseRetirement(t, d.Folds, false)
		preparedFoldEntry(t, before, sqltype.Text, "(0.10000000000000002,1)")
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT SELECT ON fold_authority TO fold_reader", "SET SESSION AUTHORIZATION fold_reader"} {
			enginetest.SessionSQL(t, s, q)
		}
		preparedFoldExecute(t, e, s, ast, &d)
		preparedFoldEntry(t, d, sqltype.Text, "(0.1,1)")
	})
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	// Ordinary Go SELECT keeps bare arithmetic at row demand. An immutable
	// integral conversion genuinely demands its child during preflight.
	ast, failed := sequencePreparedDescription(t, e, s, "SELECT abs(-7),(1/0)::bigint")
	if _, err := sequencePreparedExecute(e, s, ast, &failed); err == nil || len(failed.Folds.Current().Entries) != 0 || failed.Folds.Current().Generation != 0 {
		t.Fatal("demanded fold published a partial group", err)
	}
	assertFoldReuseRetirement(t, failed.Folds, false)
	// All commands, not each auxiliary independently, own one planning group.
	enginetest.SessionSQL(t, s, "CREATE TABLE fold_effect(v int)")
	enginetest.SessionSQL(t, s, "INSERT INTO fold_effect VALUES(1)")
	ast, failed = sequencePreparedDescription(t, e, s, "WITH c AS (UPDATE fold_effect SET v=abs(-7) RETURNING v) SELECT (1/0)::bigint FROM c")
	if _, err := sequencePreparedExecute(e, s, ast, &failed); err == nil || len(failed.Folds.Current().Entries) != 0 || failed.Folds.Current().Generation != 0 {
		t.Fatal("auxiliary planning leaked a prefix", err)
	}
	assertFoldReuseRetirement(t, failed.Folds, false)
	enginetest.TransactionRows(t, s, "SELECT v FROM fold_effect", 1)
	ast, d := sequencePreparedDescription(t, e, s, preparedFoldPoint)
	preparedFoldExecute(t, e, s, ast, &d)
	before := snapshotFoldDescription(d)
	// Exercise the real final publisher with a private COW candidate. Retirement
	// and cancellation veto publication without mutating either generation's data.
	for _, cause := range []string{"cancellation", "retirement", "generation", "native"} {
		t.Run(cause, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = scope.Describe(ast); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			local := cloneFoldDescription(t, d)
			scope.Expected = &local
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal(err)
			}
			environment, err := e.PreparedEnvironmentLocked(auth, true)
			if err != nil { e.Mu.Unlock(); scope.Finish(err); t.Fatal(err) }
			attempt, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
			if err != nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal(err)
			}
			attempt.Group.Entries = append([]exec.FoldConstant(nil), attempt.Group.Entries...)
			attempt.Group.Private = true
			switch cause {
			case "cancellation":
				cancel()
			case "retirement":
				local.Retire()
			case "generation":
				local.Folds.Invalidate(true)
			case "native":
				stale := *local.Analysis
				stale.Epoch++
				local.Analysis = &stale
			}
			err = attempt.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
			e.Mu.Unlock()
			if cause == "cancellation" && !errors.Is(err, context.Canceled) || cause != "cancellation" && !errors.Is(err, ids.ErrCatalogReplaced) {
				scope.Finish(err)
				t.Fatal("final publication fence", err)
			}
			_ = scope.Finish(err)
			if &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
				t.Fatal("rejected candidate replaced immutable data")
			}
		})
	}
}
