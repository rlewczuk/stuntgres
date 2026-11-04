package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/typecmds"
	"stuntgres/internal/sqltype"
)

func sepgsqlSupportEngine(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	for _, query := range []string{"CREATE EXTENSION hstore", "CREATE EXTENSION ltree", "CREATE EXTENSION btree_gin"} {
		enginetest.Run(t, e, query)
	}
	runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{
		ID: "actual-support-owners", Version: "1", Contexts: []string{"A", "R", "T", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "P",
		Rules: []sepgsql.Rule{
			{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "R", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "R", Target: "S", Class: "db_procedure", Deny: []string{"execute"}},
			{Source: "R", Target: "U", Class: "db_procedure", Deny: []string{"entrypoint"}},
			{Source: "T", Target: "*", Class: "*", Allow: []string{"*"}},
		}, TrustedTransitions: []sepgsql.TrustedTransition{{Source: "R", Procedure: "S", Result: "T"}, {Source: "R", Procedure: "U", Result: "T"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(runtime, true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func sepgsqlSupportEvaluation(t *testing.T, e *core.Engine) (*exec.Context, func() string) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := scope.RoutineAuthorizationLocked()
	if err == nil {
		scope.RoutineAuth = auth
		err = scope.CaptureIdentityLocked(auth)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	if err := exec.BeginRoutineInitialization(evaluation); err != nil {
		t.Fatal(err)
	}
	return evaluation, func() string { return op.Session.Sepgsql.Current() }
}

func sepgsqlSupportType(t *testing.T, view catalog.Read, name string) *sqltype.Descriptor {
	t.Helper()
	address, ok := view.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if !ok {
		t.Fatal("missing genuine installed type", name)
	}
	typ, ok := view.Type(address)
	if !ok {
		t.Fatal("missing genuine installed type descriptor", name)
	}
	return typ.SQLType
}

func TestSepgsqlSimulationSupport(t *testing.T) {
	t.Run("actual_comparator_generations_and_alias", func(t *testing.T) {
		e := sepgsqlSupportEngine(t)
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION hstore_cmp(hstore,hstore) IS 'S'")
		enginetest.Run(t, e, "CREATE EXTENSION citext")
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.text(boolean) IS 'U'")
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION public.citext(boolean) IS 'U'")
		evaluation, subject := sepgsqlSupportEvaluation(t, e)
		typ := sepgsqlSupportType(t, evaluation.Catalog, "hstore")
		class, ok, err := evaluation.Catalog.DefaultIndexClass(403, typ)
		if err != nil || !ok {
			t.Fatal(err)
		}
		comparison, err := routine.BindIndexOrdering(evaluation.Catalog, typ.Usage(), class)
		if err != nil {
			t.Fatal(err)
		}
		a, err := typ.InputWithEnvironment(evaluation.Context, "a=>1", -1, evaluation.FunctionInput())
		if err != nil {
			t.Fatal(err)
		}
		b, err := typ.InputWithEnvironment(evaluation.Context, "b=>2", -1, evaluation.FunctionInput())
		if err != nil {
			t.Fatal(err)
		}
		if sign, err := comparison.Compare(evaluation.Context, a, b, evaluation.FunctionInput()); err != nil || sign >= 0 {
			t.Fatal("real comparator inherited SQL execute denial", sign, err)
		}
		if subject() != "R" {
			t.Fatal("actual comparator leaked its trusted subject")
		}
		callback, err := catalog.CaptureIndexRoutineCallback(evaluation.Catalog, comparison.Support(), 1)
		if err != nil {
			t.Fatal(err)
		}
		aliasOp, aliasDone := enginetest.RoleOperation(t, e, "postgres")
		t.Cleanup(aliasDone)
		enginetest.RoleExecute(t, e, aliasOp, "SELECT sepgsql_setcon('R')")
		enginetest.RoleError(t, e, aliasOp, "SELECT public.citext(true)::text", "42501", "SELinux: security policy violation", "")
		for _, witness := range []struct {
			query string
			want  [][]driver.Value
		}{
			{"SELECT pg_catalog.text(true)", [][]driver.Value{{"true"}}},
			{"SELECT pg_catalog.text(NULL::boolean)", [][]driver.Value{{nil}}},
			{"SELECT public.citext(NULL::boolean)::text", [][]driver.Value{{nil}}},
		} {
			result := enginetest.RoleExecute(t, e, aliasOp, witness.query)
			if got := enginetest.Tuples(result); !reflect.DeepEqual(got, witness.want) {
				t.Fatal(witness.query, got, witness.want)
			}
		}
		if aliasOp.Session.Sepgsql.Current() != "R" {
			t.Fatal("original or installed INTERNAL alias leaked its trusted subject")
		}
		for _, field := range []string{"class", "family", "member", "routine"} {
			wrong := comparison.Support()
			switch field {
			case "class":
				wrong.ClassRevision++
			case "family":
				wrong.FamilyRevision++
			case "member":
				wrong.Revision++
			case "routine":
				wrong.RoutineRevision++
			}
			if _, err := catalog.CaptureIndexRoutineCallback(evaluation.Catalog, wrong, 1); err == nil {
				t.Fatal("accepted foreign selected generation", field)
			}
		}
		var original, alias *catalog.Procedure
		for _, descriptor := range evaluation.Catalog.Descriptors() {
			if p, ok := descriptor.(*catalog.Procedure); ok && p.Signature == "16" {
				if p.Name() == "citext" && p.Namespace().ObjectID == ids.PublicNamespaceOID {
					alias = p
				}
				if p.Name() == "text" && p.Namespace().ObjectID == ids.PGCatalogNamespaceOID {
					original = p
				}
			}
		}
		if original == nil || alias == nil || original == alias || !routine.FmgrBuiltin(original) || routine.FmgrBuiltin(alias) || alias.Origin == nil || alias.Display.Language != catalog.InternalRoutineLanguage || alias.Display.Source != "booltext" {
			t.Fatal("genuine installed INTERNAL alias was confused with original fmgrtab identity", original, alias)
		}
		enginetest.Run(t, e, "ALTER FUNCTION hstore_cmp(hstore,hstore) RENAME TO cb_hstore_cmp")
		if err := callback.Validate(e.CatalogRead()); err == nil {
			t.Fatal("old actual callback adopted renamed routine generation")
		}
		current := e.CatalogRead()
		class, ok, err = current.DefaultIndexClass(403, typ)
		if err != nil || !ok {
			t.Fatal(err)
		}
		fresh, err := routine.BindIndexOrdering(current, typ.Usage(), class)
		if err != nil {
			t.Fatal("rename lost original installed ordering", err)
		}
		if _, err := catalog.CaptureIndexRoutineCallback(current, fresh.Support(), 1); err != nil {
			t.Fatal("fresh renamed callback failed", err)
		}
	})
	t.Run("actual_gin_roles_emitter_and_nested_leaf", func(t *testing.T) {
		e := sepgsqlSupportEngine(t)
		for _, query := range []string{
			"CREATE TABLE cb_scalar(v integer)", "CREATE INDEX cb_scalar_i ON cb_scalar USING gin(v)",
			"CREATE TABLE cb_array(v ltree[])", "CREATE INDEX cb_array_i ON cb_array USING gin(v)",
			"SECURITY LABEL ON FUNCTION gin_extract_value_int4(integer,internal) IS 'S'",
			"SECURITY LABEL ON FUNCTION gin_extract_query_int4(integer,internal,smallint,internal,internal) IS 'U'",
			"SECURITY LABEL ON FUNCTION ltree_cmp(ltree,ltree) IS 'S'",
		} {
			enginetest.Run(t, e, query)
		}
		evaluation, subject := sepgsqlSupportEvaluation(t, e)
		lookup := func(name string) *catalog.Relation {
			address, ok := evaluation.Catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
			if !ok {
				t.Fatal("missing genuine GIN relation", name)
			}
			relation, ok := evaluation.Catalog.Relation(address)
			if !ok {
				t.Fatal(name)
			}
			return relation
		}
		bind := func(table, name string) sqltype.GinBinding {
			idx := lookup(name)
			bindings, err := index.BindGinIndex(evaluation.Catalog, lookup(table), idx.SQLIndex)
			if err != nil || len(bindings) != 1 {
				t.Fatal(err)
			}
			return bindings[0]
		}
		scalar := bind("cb_scalar", "cb_scalar_i")
		value, err := sqltype.Integer(scalar.Usage.Type, 7)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		emit := func(token sqltype.GinToken) error {
			calls++
			if subject() != "R" || token.Kind != sqltype.GinOrdinary || !sqltype.SameDatum(token.Element, value) {
				t.Fatal("extractor subject or scalar shape escaped to emitter", subject(), token)
			}
			return nil
		}
		root := e.Storage.IndexRoots[lookup("cb_scalar_i").Address()].Gin
		_, candidateErr := index.GinScalarCandidates(evaluation.Context, root, 0, value, 3, evaluation.FunctionInput())
		enginetest.State(t, candidateErr, "42501")
		if err := scalar.ExtractWithEnvironment(evaluation.Context, value, emit, evaluation.FunctionInput()); err != nil || calls != 1 {
			t.Fatal("actual value role did not run", calls, err)
		}
		enginetest.State(t, scalar.ExtractQueryWithEnvironment(evaluation.Context, value, emit, evaluation.FunctionInput()), "42501")
		if subject() != "R" || calls != 1 {
			t.Fatal("denied query extraction ran emitter or leaked subject")
		}
		if err := scalar.ExtractQueryWithEnvironment(evaluation.Context, sqltype.Null(scalar.Usage.Type), func(token sqltype.GinToken) error {
			if token.Kind != sqltype.GinNullItem || subject() != "R" {
				t.Fatal("NULL marker invoked query callback")
			}
			return nil
		}, evaluation.FunctionInput()); err != nil {
			t.Fatal(err)
		}
		array := bind("cb_array", "cb_array_i")
		if array.Class.Support(1) != (sqltype.IndexSupport{}) {
			t.Fatal("array GIN invented an explicit comparison support member")
		}
		comparison, ok := array.ElementComparison()
		if !ok {
			t.Fatal("array GIN lost its default element comparator")
		}
		comparators := 0
		if err := comparison.VisitIndexSupports(func(support sqltype.IndexSupport, slot uint16) error {
			callback, err := catalog.CaptureIndexRoutineCallback(evaluation.Catalog, support, slot)
			if err != nil {
				return err
			}
			if slot != 1 || callback.Class.Method != catalog.BtreeMethod.OID() || callback.Helper.Name() != "ltree_cmp" {
				t.Fatal("array GIN selected a synthetic or non-element comparator", callback)
			}
			comparators++
			return nil
		}); err != nil || comparators != 1 {
			t.Fatal("array GIN lost genuine selected ltree ordering", comparators, err)
		}
		v, err := array.Usage.Type.InputWithEnvironment(evaluation.Context, `{a,b,NULL}`, -1, evaluation.FunctionInput())
		if err != nil {
			t.Fatal(err)
		}
		var tokens []sqltype.GinToken
		if err := array.ExtractWithEnvironment(evaluation.Context, v, func(token sqltype.GinToken) error {
			if subject() != "R" {
				t.Fatal("original array extractor leaked subject")
			}
			tokens = append(tokens, token)
			return nil
		}, evaluation.FunctionInput()); err != nil {
			t.Fatal(err)
		}
		if len(tokens) != 3 {
			t.Fatal("genuine array extraction shape", tokens)
		}
		if sign, err := array.Compare(evaluation.Context, tokens[0], tokens[1], evaluation.FunctionInput()); err != nil || sign >= 0 {
			t.Fatal("actual nested ltree comparison", sign, err)
		}
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION ltree_cmp(ltree,ltree) IS 'U'")
		_, err = array.Compare(evaluation.Context, tokens[0], tokens[1], evaluation.FunctionInput())
		enginetest.State(t, err, "42501")
		if subject() != "R" {
			t.Fatal("denied nested array comparator failed to unwind")
		}
		if sign, err := array.Compare(evaluation.Context, tokens[2], tokens[2], evaluation.FunctionInput()); err != nil || sign != 0 {
			t.Fatal("NULL marker invented an element callback", sign, err)
		}
		if subject() != "R" {
			t.Fatal("nested array comparator failed to unwind")
		}
		canceled, cancel := context.WithCancel(evaluation.Context)
		cancel()
		if err := scalar.ExtractWithEnvironment(canceled, value, emit, evaluation.FunctionInput()); err != context.Canceled {
			t.Fatal("canceled extraction entered body", err)
		}
		if _, err := array.Compare(canceled, tokens[0], tokens[1], evaluation.FunctionInput()); err != context.Canceled {
			t.Fatal("canceled array comparison entered body", err)
		}
		if _, err := array.Compare(evaluation.Context, tokens[0], sqltype.GinToken{Element: value}, evaluation.FunctionInput()); err == nil {
			t.Fatal("array comparison accepted a foreign element carrier")
		}
		if subject() != "R" {
			t.Fatal("invalid array comparison leaked subject")
		}
	})
	t.Run("actual_hash_family_equality", func(t *testing.T) {
		e := sepgsqlSupportEngine(t)
		for _, query := range []string{"CREATE TABLE cb_equality(v hstore)", "CREATE INDEX cb_equality_i ON cb_equality USING hash(v)", "SECURITY LABEL ON FUNCTION hstore_eq(hstore,hstore) IS 'S'"} {
			enginetest.Run(t, e, query)
		}
		evaluation, subject := sepgsqlSupportEvaluation(t, e)
		var parent, idx *catalog.Relation
		for _, descriptor := range evaluation.Catalog.Descriptors() {
			if relation, ok := descriptor.(*catalog.Relation); ok {
				switch relation.Name() {
				case "cb_equality":
					parent = relation
				case "cb_equality_i":
					idx = relation
				}
			}
		}
		if parent == nil || idx == nil {
			t.Fatal("missing actual hash index owner")
		}
		binding, err := index.BindHashIndex(evaluation.Catalog, parent, idx.SQLIndex)
		if err != nil {
			t.Fatal(err)
		}
		a, err := binding.Usage.Type.InputWithEnvironment(evaluation.Context, "a=>1", -1, evaluation.FunctionInput())
		if err != nil {
			t.Fatal(err)
		}
		if same, err := binding.Provider.EqualWithEnvironment(evaluation.Context, a, a, evaluation.FunctionInput()); err != nil || !same {
			t.Fatal("direct family equality inherited SQL execute denial", same, err)
		}
		if subject() != "R" {
			t.Fatal("selected hash equality failed to unwind")
		}
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION hstore_eq(hstore,hstore) IS 'U'")
		_, err = binding.Provider.EqualWithEnvironment(evaluation.Context, a, a, evaluation.FunctionInput())
		enginetest.State(t, err, "42501")
		if subject() != "R" {
			t.Fatal("denied hash equality leaked subject")
		}
	})
	for _, outcome := range []string{"commit", "rollback", "cancel", "stale_parent", "stale_role"} {
		t.Run("generated_constructor_journal_"+outcome, func(t *testing.T) {
			e := sepgsqlSupportEngine(t)
			enginetest.Run(t, e, "SECURITY LABEL ON SCHEMA public IS 'P'")
			before := e.CatalogRead()
			parentKey := catalog.SecurityLabelKey{Object: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Provider: "selinux"}
			parentLabel := before.State().SecurityLabel(parentKey)
			if parentLabel == nil || parentLabel.Label != "P" {
				t.Fatal("missing actual parent creation label")
			}
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			ctx, cancel := context.WithCancel(op.Context())
			defer cancel()
			op.Ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if !scope.Finished {
					_ = scope.Finish(context.Canceled)
				}
			}()
			statement := enginetest.Statement(t, "CREATE TYPE cb_journal_range AS RANGE (subtype=ltree)")
			if err := scope.Gate(statement); err != nil {
				t.Fatal(err)
			}
			created, err := typecmds.ExecuteEnumDDL(e, scope, statement, 0)
			if err != nil {
				t.Fatal(err)
			}
			journal := scope.Catalog
			if journal == nil || journal.LabelReads[parentKey] != parentLabel {
				t.Fatal("generated cohort lost its actual parent-label observation")
			}
			e.Mu.Lock()
			private := e.PrivateCatalogLocked(journal)
			e.Mu.Unlock()
			typ := sepgsqlSupportType(t, private, "cb_journal_range")
			if created.Created.Address != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}) ||
				created.Created.Domain() != before.Domain() || created.Created.Epoch != before.Epoch() {
				t.Fatal("generated cohort changed its actual created type identity")
			}
			seen := make(map[catalog.RangeConstructorForm]bool)
			var procedures []*catalog.Procedure
			for _, descriptor := range private.Descriptors() {
				procedure, ok := descriptor.(*catalog.Procedure)
				if !ok || procedure.RangeConstructor == nil || procedure.RangeConstructor.Type.RangeType() != typ {
					continue
				}
				if err := catalog.ValidateRangeConstructor(private, procedure); err != nil {
					t.Fatal(err)
				}
				form := procedure.RangeConstructor.Form
				if form < catalog.RangeConstructorTwo || form > catalog.MultirangeConstructorMany || seen[form] {
					t.Fatal("invalid or repeated generated constructor form", form)
				}
				seen[form] = true
				procedures = append(procedures, procedure)
				key := catalog.SecurityLabelKey{Object: procedure.Address(), Provider: "selinux"}
				label := journal.SecurityLabels[key]
				original, observed := journal.LabelOriginals[key]
				if label == nil || label.Label != "P" || label.PolicyID != "actual-support-owners" || label.PolicyVersion != "1" ||
					private.State().SecurityLabel(key) != label || !observed || original != nil ||
					before.Get(procedure.Address()) != nil || before.State().SecurityLabel(key) != nil {
					t.Fatal("generated label lost real journal ownership or changed retained read", form)
				}
			}
			if len(procedures) != 5 || e.CatalogRead().State() != before.State() {
				t.Fatal("generated cohort published before its actual final fence", len(procedures))
			}
			rollback := errors.New("discard generated constructor cohort")
			var primary error
			switch outcome {
			case "rollback":
				primary = rollback
			case "cancel":
				cancel()
			case "stale_parent":
				enginetest.Run(t, e, "SECURITY LABEL ON SCHEMA public IS 'S'")
			case "stale_role":
				enginetest.Run(t, e, "ALTER ROLE postgres NOINHERIT")
			}
			publishedBefore := e.CatalogRead()
			err = scope.Finish(primary)
			if outcome == "commit" {
				if err != nil {
					t.Fatal("genuine generated cohort failed publication", err)
				}
			} else {
				if err == nil || outcome == "rollback" && !errors.Is(err, rollback) || outcome == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("generated cohort lost its actual terminal failure", outcome, err)
				}
				if e.CatalogRead().State() != publishedBefore.State() {
					t.Fatal("failed generated cohort published a catalog prefix", outcome)
				}
			}
			current := e.CatalogRead()
			for _, procedure := range procedures {
				key := catalog.SecurityLabelKey{Object: procedure.Address(), Provider: "selinux"}
				if before.Get(procedure.Address()) != nil || before.State().SecurityLabel(key) != nil ||
					private.Get(procedure.Address()) != procedure || private.State().SecurityLabel(key) != journal.SecurityLabels[key] {
					t.Fatal("finalization mutated retained generated cohort reads", outcome)
				}
				if outcome == "commit" {
					if current.Get(procedure.Address()) == nil || current.State().SecurityLabel(key) != journal.SecurityLabels[key] {
						t.Fatal("successful generated cohort lost a real constructor or label")
					}
				} else if current.Get(procedure.Address()) != nil || current.State().SecurityLabel(key) != nil {
					t.Fatal("failed generated cohort exposed a constructor or label", outcome)
				}
			}
		})
	}
	t.Run("actual_mixed_index_publication", func(t *testing.T) {
		e := sepgsqlSupportEngine(t)
		for _, query := range []string{
			"CREATE TYPE cb_range AS RANGE (subtype=ltree)",
			"CREATE TABLE cb_rows(id integer,v hstore,a ltree[],r cb_range)",
			"CREATE INDEX cb_btree ON cb_rows(v)",
			"CREATE INDEX cb_hash ON cb_rows USING hash(v)",
			"CREATE INDEX cb_array ON cb_rows USING gin(a)",
			"SECURITY LABEL ON FUNCTION hstore_cmp(hstore,hstore) IS 'S'",
			"SECURITY LABEL ON FUNCTION hstore_hash(hstore) IS 'S'",
			"SECURITY LABEL ON FUNCTION ltree_cmp(ltree,ltree) IS 'S'",
		} {
			enginetest.Run(t, e, query)
		}
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		enginetest.RoleExecute(t, e, op, "INSERT INTO cb_rows VALUES(1,'a=>1',ARRAY['a'::ltree,NULL],cb_range('a','z','[]')),(2,'a=>1',ARRAY['a'::ltree,NULL],cb_range('a','z','[]')),(3,'b=>2',ARRAY['b'::ltree],cb_range('b','z','[]')),(4,NULL,NULL,NULL)")
		for _, witness := range []struct {
			query string
			want  [][]driver.Value
		}{
			{"SELECT count(*) FROM cb_rows", [][]driver.Value{{int64(4)}}},
			{"SELECT count(*) FROM cb_rows WHERE r @> 'c'::ltree", [][]driver.Value{{int64(3)}}},
			{"SELECT sepgsql_getcon()", [][]driver.Value{{"R"}}},
		} {
			result := enginetest.RoleExecute(t, e, op, witness.query)
			if got := enginetest.Tuples(result); !reflect.DeepEqual(got, witness.want) {
				t.Fatal(witness.query, got, witness.want)
			}
		}
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION ltree_cmp(ltree,ltree) IS 'U'")
		const insert = "INSERT INTO cb_rows VALUES(5,NULL,ARRAY['a'::ltree,'b'::ltree],NULL)"
		enginetest.RoleError(t, e, op, insert, "42501", "SELinux: security policy violation", "")
		if got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT count(*) FROM cb_rows")); !reflect.DeepEqual(got, [][]driver.Value{{int64(4)}}) {
			t.Fatal("denied nested comparator published a partial INSERT", got)
		}
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("denied index publication leaked its trusted subject")
		}
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION ltree_cmp(ltree,ltree) IS 'S'")
		enginetest.RoleExecute(t, e, op, insert)
		if got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT count(*) FROM cb_rows")); !reflect.DeepEqual(got, [][]driver.Value{{int64(5)}}) {
			t.Fatal("granted index publication retry lost rows", got)
		}
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("successful index publication leaked its trusted subject")
		}
	})
	t.Run("actual_internal_publication_and_client_guard", func(t *testing.T) {
		e := sepgsqlSupportEngine(t)
		for _, query := range []string{
			"CREATE TABLE cb_publish(v hstore,a ltree[])",
			"INSERT INTO cb_publish VALUES('a=>1',ARRAY['a'::ltree]),('b=>2',ARRAY['b'::ltree])",
			"CREATE INDEX cb_publish_btree ON cb_publish(v)",
			"CREATE INDEX cb_publish_hash ON cb_publish USING hash(v)",
			"CREATE INDEX cb_publish_gin ON cb_publish USING gin(a)",
			"SECURITY LABEL ON FUNCTION hstore_cmp(hstore,hstore) IS 'U'",
			"SECURITY LABEL ON FUNCTION hstore_hash(hstore) IS 'U'",
		} {
			enginetest.Run(t, e, query)
		}
		read := e.CatalogRead()
		var parent *catalog.Relation
		for _, descriptor := range read.Descriptors() {
			if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == "cb_publish" {
				parent = relation
			}
		}
		if parent == nil {
			t.Fatal("missing actual publication parent")
		}
		rebuild := map[ids.ObjectAddress]bool{parent.Address(): true}
		trusted := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: read}}
		internal, err := exec.InternalIndexPublicationEvaluation(&trusted, read)
		if err != nil || internal.FunctionInput().IndexSupport != nil {
			t.Fatal("genuine internal publication retained a client support callback", err)
		}
		roots, err := index.PrepareIndexPublication(&trusted, read.State(), read.State(), read, e.Storage.Relations, e.Storage.IndexRoots, rebuild, nil, nil, true, sqltype.OutputEnvironment{}, access.View{Catalog: read}, nil, nil, nil, index.ProgressBinding{})
		if err != nil {
			t.Fatal("explicit genuine no-principal publication invoked client callback", err)
		}
		count := 0
		for address, root := range roots {
			relation, ok := read.Relation(address)
			if ok && relation.SQLIndex != nil && relation.SQLIndex.Parent == parent.Address() {
				count++
				if root == nil {
					t.Fatal("internal publication lost an actual typed index root")
				}
				if root.Hash != nil {
					entries := 0
					for _, bucket := range root.Hash.Buckets {
						entries += len(bucket)
					}
					if entries != 2 {
						t.Fatal("internal publication lost actual hash entries", entries)
					}
				} else if len(root.Entries) != 2 {
					t.Fatal("internal publication lost actual Btree/GIN entries")
				}
				if root.Gin != nil && (len(root.Gin.Columns) != 1 || len(root.Gin.Columns[0].Tokens) != 2 || len(root.Gin.Columns[0].NonNull) != 2 || len(root.Gin.Pending) != 0) {
					t.Fatal("internal publication lost actual GIN tokens or postings")
				}
			}
		}
		if count != 3 {
			t.Fatal("internal publication failed to rebuild real Btree/hash/GIN roots", count)
		}
		client, _ := sepgsqlSupportEvaluation(t, e)
		if _, err := index.PrepareIndexPublication(client, read.State(), read.State(), read, e.Storage.Relations, e.Storage.IndexRoots, rebuild, nil, nil, true, sqltype.OutputEnvironment{}, access.View{Catalog: read}, nil, nil, nil, index.ProgressBinding{}); err == nil {
			t.Fatal("live SQL client borrowed explicit internal publication")
		}
	})
	t.Run("actual_internal_derived_requires_owner", func(t *testing.T) {
		e := sepgsqlSupportEngine(t)
		for _, query := range []string{
			"CREATE TABLE cb_recipe(i integer)",
			"INSERT INTO cb_recipe VALUES(-1),(2)",
			"CREATE INDEX cb_recipe_i ON cb_recipe(abs(i))",
		} {
			enginetest.Run(t, e, query)
		}
		read := e.CatalogRead()
		var parent, idx *catalog.Relation
		for _, descriptor := range read.Descriptors() {
			if relation, ok := descriptor.(*catalog.Relation); ok {
				switch relation.Name() {
				case "cb_recipe":
					parent = relation
				case "cb_recipe_i":
					idx = relation
				}
			}
		}
		if parent == nil || idx == nil || idx.SQLIndex.Keys[0].Derived == nil {
			t.Fatal("missing genuine executable index recipe")
		}
		trusted := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: read}}
		internal, err := exec.InternalIndexPublicationEvaluation(&trusted, read)
		if err != nil {
			t.Fatal(err)
		}
		original := e.Storage.IndexRoots[idx.Address()]
		if root, err := index.BuildIndexRoot(&internal, parent, idx.SQLIndex, e.Storage.Relations[parent.Address()], nil); err != ids.ErrCatalogReplaced || root != nil {
			t.Fatal("actual derived recipe borrowed missing live initializer", root, err)
		}
		if e.Storage.IndexRoots[idx.Address()] != original {
			t.Fatal("rejected derived recipe changed the live index root")
		}
	})
	for name, mutate := range map[string]func(catalog.IndexRoutineCallback) string{
		"class": func(c catalog.IndexRoutineCallback) string {
			return "ALTER OPERATOR CLASS " + c.Class.Name() + " USING btree RENAME TO cb_renamed_class"
		},
		"family": func(c catalog.IndexRoutineCallback) string {
			return "ALTER OPERATOR FAMILY " + c.Family.Name() + " USING btree RENAME TO cb_renamed_family"
		},
		"member_cohort": func(catalog.IndexRoutineCallback) string { return "DROP EXTENSION hstore CASCADE" },
	} {
		t.Run("actual_stale_"+name, func(t *testing.T) {
			e := sepgsqlSupportEngine(t)
			original := e.CatalogRead()
			typ := sepgsqlSupportType(t, original, "hstore")
			class, ok, err := original.DefaultIndexClass(403, typ)
			if err != nil || !ok {
				t.Fatal(err)
			}
			kernel, err := routine.BindIndexOrdering(original, typ.Usage(), class)
			if err != nil {
				t.Fatal(err)
			}
			captured, err := catalog.CaptureIndexRoutineCallback(original, kernel.Support(), 1)
			if err != nil {
				t.Fatal(err)
			}
			enginetest.Run(t, e, mutate(captured))
			if err := captured.Validate(e.CatalogRead()); err == nil {
				t.Fatal("old callback accepted genuinely changed catalog source", name)
			}
			if name == "member_cohort" {
				enginetest.Run(t, e, "CREATE EXTENSION hstore")
			}
			evaluation, subject := sepgsqlSupportEvaluation(t, e)
			typ = sepgsqlSupportType(t, evaluation.Catalog, "hstore")
			class, ok, err = evaluation.Catalog.DefaultIndexClass(403, typ)
			if err != nil || !ok {
				t.Fatal(err)
			}
			kernel, err = routine.BindIndexOrdering(evaluation.Catalog, typ.Usage(), class)
			if err != nil {
				t.Fatal("fresh genuine source failed after supported mutation", err)
			}
			a, err := typ.InputWithEnvironment(evaluation.Context, "a=>1", -1, evaluation.FunctionInput())
			if err != nil {
				t.Fatal(err)
			}
			b, err := typ.InputWithEnvironment(evaluation.Context, "b=>2", -1, evaluation.FunctionInput())
			if err != nil {
				t.Fatal(err)
			}
			if sign, err := kernel.Compare(evaluation.Context, a, b, evaluation.FunctionInput()); err != nil || sign >= 0 {
				t.Fatal("fresh source kernel failed after mutation", name, sign, err)
			}
			if subject() != "R" {
				t.Fatal("fresh source leaked subject", name)
			}
		})
	}
}
