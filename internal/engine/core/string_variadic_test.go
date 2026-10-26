package core_test

import (
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestStringVariadicRetainedGeneration(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	ast, description := sequencePreparedDescription(t, e, s, "SELECT quote_ident('foo')")
	if description.Folds.Current().Entries != nil {
		t.Fatal("Describe published Execute-only quotes")
	}
	result, err := sequencePreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if len(description.Folds.Current().Entries) != 1 || description.Folds.Environment.Discard != s.DiscardPlansGeneration || description.PlanOwner != s.SessionToken {
		t.Fatal("missing owner-qualified capture")
	}
	original := snapshotFoldDescription(description)
	result, err = sequencePreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if &original.Accepted.Entries[0] != &description.Folds.Current().Entries[0] {
		t.Fatal("stable hit copied private group")
	}
	discardUnitApply(t, s, parser.DiscardPlans)
	if &original.Accepted.Entries[0] != &description.Folds.Current().Entries[0] || description.Folds.Environment.Discard != original.Environment.Discard {
		t.Fatal("eager discard mutation")
	}
	result, err = sequencePreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if &original.Accepted.Entries[0] == &description.Folds.Current().Entries[0] || description.Folds.Environment.Discard != s.DiscardPlansGeneration {
		t.Fatal("discard did not recollect quotes")
	}
	if !samePublishedFold(original.Accepted.Entries[0], description.Folds.Current().Entries[0]) {
		t.Fatal("source capture changed during refresh")
	}

	bad, failed := sequencePreparedDescription(t, e, s, "SELECT quote_ident('foo'),quote_literal(1/0)")
	_, err = sequencePreparedExecute(e, s, bad, &failed)
	if err == nil || failed.Folds.Current().Entries != nil {
		t.Fatal("failed demanded fold published partial group", err)
	}
	before := snapshotFoldDescription(description)
	discardUnitApply(t, s, parser.DiscardPlans)
	savedResults := description.Results
	description.Results = nil
	_, err = sequencePreparedExecute(e, s, ast, &description)
	description.Results = savedResults
	if err == nil || description.Folds.Environment.Discard != before.Environment.Discard || &description.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("signature failure published private group", err)
	}


	t.Run("LoweredViewLayersAndLocalChecks", func(t *testing.T) {
		engine := rootengine.New()
		session := enginetest.StartSession(t, engine, nil)
		// Equal-length source/view names place both quote calls at the same
		// offset in frame 1. The two definitions must never share a capture.
		for _, query := range []string{
			"CREATE TABLE bv(v text)",
			"INSERT INTO bv VALUES('seed')",
			"CREATE VIEW iv AS SELECT v FROM bv WHERE v<>quote_ident('inval') WITH LOCAL CHECK OPTION",
			"CREATE VIEW ov AS SELECT v FROM iv WHERE v<>quote_ident('outvl') WITH LOCAL CHECK OPTION",
		} {
			enginetest.SessionSQL(t, session, query)
		}
		for _, query := range []string{
			"INSERT INTO ov VALUES('inval')",
			"INSERT INTO ov VALUES('outvl')",
			"UPDATE ov SET v='inval' WHERE v='seed'",
			"UPDATE ov SET v='outvl' WHERE v='seed'",
		} {
			ast, description := sequencePreparedDescription(t, engine, session, query)
			_, err := sequencePreparedExecute(engine, session, ast, &description)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "44000" {
				t.Fatalf("%s: local check lost its layer: %v", query, err)
			}
			if len(description.Folds.Current().Entries) != 2 {
				t.Fatalf("%s: missing layer captures: %+v", query, description.Folds.Current().Entries)
			}
			a, b := description.Folds.Current().Entries[0], description.Folds.Current().Entries[1]
			av, _ := a.Value.Text()
			bv, _ := b.Value.Text()
			if a.Offset != b.Offset || a.Frame != b.Frame || a.Origin.Owner == b.Origin.Owner || av == bv || (av != "inval" && av != "outvl") || (bv != "inval" && bv != "outvl") {
				t.Fatalf("lowered equal-offset view calls collided: %+v / %+v", a, b)
			}
		}
		ast, description := sequencePreparedDescription(t, engine, session, "INSERT INTO ov VALUES('valid')")
		if _, err := sequencePreparedExecute(engine, session, ast, &description); err != nil {
			t.Fatal("valid local-check row rejected", err)
		}
		published := description.Folds.Current().Entries
		enginetest.SessionSQL(t, session, "SET quote_all_identifiers=on")
		enginetest.SessionSQL(t, session, "ALTER TABLE bv ADD COLUMN spare integer")
		if _, err := sequencePreparedExecute(engine, session, ast, &description); err != nil {
			t.Fatal("stored-only analysis replan failed", err)
		}
		if len(description.Folds.Current().Entries) != 2 || &description.Folds.Current().Entries[0] != &published[0] {
			t.Fatal("analysis-only replan copied or refreshed unchanged view captures")
		}
		enginetest.SessionSQL(t, session, "CREATE VIEW nv AS SELECT v FROM bv WHERE concat(quote_nullable(v),'')=quote_nullable(v) WITH LOCAL CHECK OPTION")
		nestedAST, nestedDescription := sequencePreparedDescription(t, engine, session, "UPDATE nv SET v='nested' WHERE v='seed'")
		result, err := sequencePreparedExecute(engine, session, nestedAST, &nestedDescription)
		if err != nil || result.Count != 1 || len(nestedDescription.Folds.Current().Entries) != 0 {
			t.Fatal("nested row-dependent quotes lost column lowering or were retained", result.Count, err)
		}
		for _, name := range []string{"iv", "ov", "nv"} {
			view := primaryKeyUnitRelation(t, engine, name)
			_, _, predicates, reason := stored.ViewWriteShape(view)
			if reason != "" {
				t.Fatal(reason)
			}
			for _, predicate := range predicates {
				plan.WalkExpression(predicate, func(expression expr.Expr) {
					if quote, ok := expression.(functions.StringVariadic); ok && exec.FoldOriginOf(quote) != (exec.FoldOrigin{}) {
						t.Fatal("execution lowering stamped the stored source tree")
					}
				})
			}
		}
	})

	t.Run("AnalysisReplanRetiresOnlyRoot", func(t *testing.T) {
		engine := rootengine.New()
		session := enginetest.StartSession(t, engine, nil)
		enginetest.SessionSQL(t, session, "CREATE TABLE sv_replan(v text,w text DEFAULT quote_ident('stored'))")
		ast, description := sequencePreparedDescription(t, engine, session, "INSERT INTO sv_replan(v) VALUES(quote_ident('root'))")
		if _, err := sequencePreparedExecute(engine, session, ast, &description); err != nil {
			t.Fatal(err)
		}
		published := description.Folds.Current().Entries
		enginetest.SessionSQL(t, session, "SET quote_all_identifiers=on")
		enginetest.SessionSQL(t, session, "ALTER TABLE sv_replan ADD COLUMN spare integer")
		if _, err := sequencePreparedExecute(engine, session, ast, &description); err != nil {
			t.Fatal(err)
		}
		if len(published) != 2 || len(description.Folds.Current().Entries) != 2 {
			t.Fatal("analysis replan lost root or default capture")
		}
		for _, capture := range description.Folds.Current().Entries {
			want := "stored"
			if capture.Origin.Owner == (ids.ObjectRef{}) {
				want = `"root"`
			}
			got, _ := capture.Value.Text()
			if got != want {
				t.Fatalf("analysis replan capture = %q, want %q", got, want)
			}
		}
		for _, capture := range published {
			got, _ := capture.Value.Text()
			if got != "root" && got != "stored" {
				t.Fatal("analysis replan mutated published source captures")
			}
		}
	})

	t.Run("StalePublishedGroupDemandFailureRetry", func(t *testing.T) {
		engine := rootengine.New()
		session := enginetest.StartSession(t, engine, nil)
		enginetest.SessionSQL(t, session, "SET quote_all_identifiers=off")
		enginetest.SessionSQL(t, session, "SET bytea_output=hex")
		enginetest.SessionSQL(t, session, `CREATE TABLE sv_retry(v text,z timestamptz,a text DEFAULT quote_ident('stored'),b text DEFAULT quote_literal('\x00015c'::bytea))`)
		ast, description := sequencePreparedDescription(t, engine, session, "INSERT INTO sv_retry(v,z) VALUES(quote_ident('root'),'now'::timestamptz)")
		result, err := sequencePreparedExecute(engine, session, ast, &description)
		if err != nil || result.Count != 1 {
			t.Fatal("initial capture execution", result.Count, err)
		}
		before := snapshotFoldDescription(description)
		if len(before.Accepted.Entries) != 3 || len(before.Temporals) == 0 || before.Analysis == nil {
			t.Fatal("missing initial quote/temporal/analysis publication")
		}
		source := append([]exec.FoldConstant(nil), before.Accepted.Entries...)
		beforeRelation := primaryKeyUnitRelation(t, engine, "sv_retry")
		aDefinition := beforeRelation.ColumnMeta.Defaults[beforeRelation.Attributes[2]].OID
		bDefinition := beforeRelation.ColumnMeta.Defaults[beforeRelation.Attributes[3]].OID
		initial := map[string]bool{"root": false, "stored": false, `E'\\x00015c'`: false}
		for _, capture := range source {
			value, err := capture.Value.Text()
			seen, known := initial[value]
			if err != nil || !known || seen {
				t.Fatal("unexpected initial retained value", value, err)
			}
			initial[value] = true
		}
		discardUnitApply(t, session, parser.DiscardPlans)
		enginetest.SessionSQL(t, session, "ALTER TABLE sv_retry ALTER COLUMN b SET DEFAULT quote_literal(1/0)")
		result, err = sequencePreparedExecute(engine, session, ast, &description)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}) || result.Rows != nil {
			t.Fatal("demanded fold failure", result, err)
		}
		if description.PlanOwner != session.SessionToken || description.PlanGeneration != session.DiscardPlansGeneration || description.PlanGeneration == before.PlanGeneration || description.Analysis == before.Analysis || len(description.Temporals) == 0 || &description.Temporals[0] == &before.Temporals[0] {
			t.Fatal("failure did not occur after temporal/analysis generation advance")
		}
		if description.Folds.Environment.Discard != before.Environment.Discard || len(description.Folds.Current().Entries) != len(source) || &description.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
			t.Fatal("failed private quote refresh replaced publication")
		}
		for i, capture := range source {
			if !samePublishedFold(before.Accepted.Entries[i], capture) || !samePublishedFold(description.Folds.Current().Entries[i], capture) {
				t.Fatal("failed refresh mutated source capture", i)
			}
		}
		rows := enginetest.SessionSQL(t, session, "SELECT v,a,b FROM sv_retry")
		values := enginetest.Tuples(rows)
		rows.Rows.Close()
		if len(values) != 1 || values[0][0] != "root" || values[0][1] != "stored" || values[0][2] != `E'\\x00015c'` {
			t.Fatal("failed demanded fold changed committed rows", values)
		}
		enginetest.SessionSQL(t, session, "SET quote_all_identifiers=on")
		enginetest.SessionSQL(t, session, "SET bytea_output=escape")
		enginetest.SessionSQL(t, session, `ALTER TABLE sv_retry ALTER COLUMN b SET DEFAULT quote_literal('\x00015c'::bytea)`)
		result, err = sequencePreparedExecute(engine, session, ast, &description)
		if err != nil || result.Count != 1 {
			t.Fatal("successful stale retry", result.Count, err)
		}
		if description.Folds.Environment.Discard != session.DiscardPlansGeneration || description.PlanGeneration != session.DiscardPlansGeneration || description.PlanOwner != session.SessionToken || len(description.Folds.Current().Entries) != 3 || &description.Folds.Current().Entries[0] == &before.Accepted.Entries[0] {
			t.Fatal("retry did not publish fresh owner-qualified group and stamps")
		}
		relation := primaryKeyUnitRelation(t, engine, "sv_retry")
		if relation.ColumnMeta.Defaults[relation.Attributes[2]].OID != aDefinition || relation.ColumnMeta.Defaults[relation.Attributes[3]].OID == bDefinition {
			t.Fatal("retry did not preserve a and replace b DEFAULT identity")
		}
		catalog := engine.CatalogRead()
		ref := engine.Catalog.Domain.Ref(catalog.Epoch(), relation.Address())
		seen := map[string]bool{}
		for _, capture := range description.Folds.Current().Entries {
			value, err := capture.Value.Text()
			if err != nil {
				t.Fatal(err)
			}
			want := `"root"`
			if capture.Origin.Owner != (ids.ObjectRef{}) {
				if capture.Origin.Owner != ref {
					t.Fatal("retry adopted foreign stored origin")
				}
				switch capture.Origin.Attribute {
				case relation.Attributes[2]:
					want = `"stored"`
				case relation.Attributes[3]:
					want = `E'\\000\\001\\\\'`
				default:
					t.Fatal("unexpected retry DEFAULT attribute")
				}
				if capture.Origin.Definition != relation.ColumnMeta.Defaults[capture.Origin.Attribute].OID {
					t.Fatal("retry retained obsolete DEFAULT definition")
				}
			}
			if value != want || seen[want] {
				t.Fatalf("retry capture = %q, want distinct %q", value, want)
			}
			seen[want] = true
		}
		for i, capture := range source {
			if !samePublishedFold(before.Accepted.Entries[i], capture) {
				t.Fatal("successful retry mutated source publication", i)
			}
		}
	})

	t.Run("MergeDefaultOrigins", func(t *testing.T) {
		engine := rootengine.New()
		session := enginetest.StartSession(t, engine, nil)
		enginetest.SessionSQL(t, session, "CREATE TABLE sv_merge_origin(id integer,a text DEFAULT quote_ident('a'),b text DEFAULT quote_ident('b'),r text)")
		ast, description := sequencePreparedDescription(t, engine, session, "MERGE INTO sv_merge_origin t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id,r) VALUES(s.id,quote_ident('root'))")
		result, err := sequencePreparedExecute(engine, session, ast, &description)
		if err != nil || result.Count != 1 || len(description.Folds.Current().Entries) != 3 {
			t.Fatal("MERGE did not execute demanded defaults", result.Count, description.Folds.Current().Entries, err)
		}
		relation := primaryKeyUnitRelation(t, engine, "sv_merge_origin")
		catalog := engine.CatalogRead()
		ref := engine.Catalog.Domain.Ref(catalog.Epoch(), relation.Address())
		seen := map[string]bool{}
		for _, capture := range description.Folds.Current().Entries {
			value, err := capture.Value.Text()
			if err != nil {
				t.Fatal(err)
			}
			want := "root"
			if capture.Origin.Owner != (ids.ObjectRef{}) {
				if capture.Origin.Owner != ref || capture.Origin.Definition != relation.ColumnMeta.Defaults[capture.Origin.Attribute].OID {
					t.Fatal("MERGE default lost physical origin", capture.Origin)
				}
				switch capture.Origin.Attribute {
				case relation.Attributes[1]:
					want = "a"
				case relation.Attributes[2]:
					want = "b"
				default:
					t.Fatal("MERGE captured unrelated default")
				}
			}
			if value != want || seen[want] {
				t.Fatal("MERGE origins collided", value, want)
			}
			seen[want] = true
		}
		for _, attribute := range relation.Attributes[1:3] {
			node := relation.ColumnMeta.Defaults[attribute].Expression.(functions.StringVariadic)
			if exec.FoldOriginOf(node) != (exec.FoldOrigin{}) {
				t.Fatal("MERGE stamped catalog DEFAULT source")
			}
		}
	})

	t.Run("ParameterizedMergeOperationLocal", func(t *testing.T) {
		engine := rootengine.New()
		session := enginetest.StartSession(t, engine, nil)
		enginetest.SessionSQL(t, session, "CREATE TABLE sv_local_merge(id integer,a text DEFAULT quote_ident('a'),b text DEFAULT quote_ident('b'),r text)")
		ast, description := sequencePreparedDescription(t, engine, session, "MERGE INTO sv_local_merge t USING (VALUES($1::integer)) s(id) ON t.id=$1::integer WHEN NOT MATCHED THEN INSERT(id,r) VALUES($1::integer,quote_ident('root'))")
		peerAST, peerDescription := sequencePreparedDescription(t, engine, session, "MERGE INTO sv_local_merge t USING (VALUES($1::integer)) s(id) ON t.id=$1::integer WHEN NOT MATCHED THEN INSERT(id,r) VALUES($1::integer,quote_ident('root'))")
		stamp := description.Folds.Environment.Discard
		var previous []exec.FoldConstant
		for id := int64(1); id <= 12; id++ {
			on := id%2 == 1
			setting := "off"
			if on {
				setting = "on"
			}
			if id == 9 || id == 11 {
				enginetest.SessionSQL(t, session, "BEGIN")
				if id == 11 {
					enginetest.SessionSQL(t, session, "SAVEPOINT sv_local")
				}
				enginetest.SessionSQL(t, session, "SET LOCAL quote_all_identifiers="+setting)
				discardUnitApply(t, session, parser.DiscardPlans)
			} else {
				if id == 10 {
					enginetest.SessionSQL(t, session, "ROLLBACK")
				}
				if id == 12 {
					enginetest.SessionSQL(t, session, "ROLLBACK TO SAVEPOINT sv_local")
				}
				enginetest.SessionSQL(t, session, "SET quote_all_identifiers="+setting)
			}
			if id == 7 {
				discardUnitApply(t, session, parser.DiscardPlans)
			}
			func() {
				op, err := session.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := engine.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				owner, statement := &description, ast
				if id%2 == 0 {
					owner, statement = &peerDescription, peerAST
				}
				scope.Expected = owner
				result, err := scope.Execute(statement, 1, func(ctx context.Context, p analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
					value, err := sqltype.Integer(p.Types[0].Type, id)
					frame[0] = value
					return err
				})
				if err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				group := scope.FoldCaptures
				if result.Count != 1 || group == nil || !group.Sealed || len(group.Entries) != 3 || description.Folds.Current().Entries != nil || peerDescription.Folds.Current().Entries != nil || description.Folds.Environment.Discard != stamp || peerDescription.Folds.Environment.Discard != stamp {
					scope.Finish(errors.New("unexpected quote publication"))
					t.Fatal("parameterized MERGE retained/shared quotes", result.Count, group, description.Folds.Current().Entries)
				}
				if previous != nil && &previous[0] == &group.Entries[0] {
					t.Fatal("parameterized execution borrowed prior backing")
				}
				relation := primaryKeyUnitRelation(t, engine, "sv_local_merge")
				catalog := engine.CatalogRead()
				ref := engine.Catalog.Domain.Ref(catalog.Epoch(), relation.Address())
				seen := map[string]bool{}
				for _, capture := range group.Entries {
					want := "root"
					if capture.Origin.Owner != (ids.ObjectRef{}) {
						if capture.Origin.Owner != ref || capture.Origin.Definition != relation.ColumnMeta.Defaults[capture.Origin.Attribute].OID {
							t.Fatal("operation-local default origin lost")
						}
						switch capture.Origin.Attribute {
						case relation.Attributes[1]:
							want = "a"
						case relation.Attributes[2]:
							want = "b"
						default:
							t.Fatal("unexpected operation-local default")
						}
					}
					if on {
						want = `"` + want + `"`
					}
					value, err := capture.Value.Text()
					if err != nil || value != want || seen[want] {
						t.Fatal("parameterized group did not use current settings", value, want, err)
					}
					seen[want] = true
				}
				for _, capture := range previous {
					value, err := capture.Value.Text()
					if err != nil || on && strings.Contains(value, `"`) || !on && !strings.Contains(value, `"`) {
						t.Fatal("fresh execution mutated previous group", value, err)
					}
				}
				previous = group.Entries
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			}()
		}
		enginetest.SessionSQL(t, session, "ROLLBACK")
	})

	t.Run("ParameterizedViewOperationLocal", func(t *testing.T) {
		engine := rootengine.New()
		session := enginetest.StartSession(t, engine, nil)
		enginetest.SessionSQL(t, session, "CREATE TABLE sv_local_view_base(id integer,r text)")
		enginetest.SessionSQL(t, session, "CREATE VIEW sv_local_view AS SELECT id,r FROM sv_local_view_base WHERE r<>quote_ident('blocked') WITH LOCAL CHECK OPTION")
		ast, description := sequencePreparedDescription(t, engine, session, "INSERT INTO sv_local_view VALUES($1::integer,quote_ident('root'))")
		stamp := description.Folds.Environment.Discard
		view := primaryKeyUnitRelation(t, engine, "sv_local_view")
		catalog := engine.CatalogRead()
		origin := exec.FoldOrigin{Owner: engine.Catalog.Domain.Ref(catalog.Epoch(), view.Address()), Definition: stored.ViewOf(view).RuleOID(), Revision: stored.ViewOf(view).DefinitionRevision()}
		var previous []exec.FoldConstant
		for id := int64(1); id <= 2; id++ {
			setting, root, blocked := "off", "root", "blocked"
			if id == 1 {
				setting, root, blocked = "on", `"root"`, `"blocked"`
			}
			enginetest.SessionSQL(t, session, "SET quote_all_identifiers="+setting)
			func() {
				op, err := session.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := engine.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				scope.Expected = &description
				result, err := scope.Execute(ast, 1, func(ctx context.Context, p analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
					value, err := sqltype.Integer(p.Types[0].Type, id)
					frame[0] = value
					return err
				})
				if err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				group := scope.FoldCaptures
				if result.Count != 1 || group == nil || !group.Sealed || len(group.Entries) != 2 || description.Folds.Current().Entries != nil || description.Folds.Environment.Discard != stamp {
					scope.Finish(errors.New("unexpected view quote publication"))
					t.Fatal("parameterized view retained quote group", group)
				}
				if previous != nil && &previous[0] == &group.Entries[0] {
					t.Fatal("view borrowed prior parameterized group")
				}
				seenRoot, seenView := false, false
				for _, capture := range group.Entries {
					value, err := capture.Value.Text()
					if err != nil {
						t.Fatal(err)
					}
					if capture.Origin.Owner == (ids.ObjectRef{}) {
						seenRoot = value == root
					} else {
						seenView = value == blocked && capture.Origin == origin
					}
				}
				if !seenRoot || !seenView {
					t.Fatal("operation-local root/view origins lost current settings")
				}
				for _, capture := range previous {
					value, _ := capture.Value.Text()
					if !strings.Contains(value, `"`) {
						t.Fatal("view mutated previous on-setting group")
					}
				}
				previous = group.Entries
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			}()
		}
	})
}

