package core_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/tsearch"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func sepgsqlDictionaryEngine(t *testing.T) *core.Engine {
	t.Helper()
	e, _ := sepgsqlCodecEngine(t, "S")
	enginetest.Run(t, e, "CREATE EXTENSION unaccent")
	enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION unaccent_init(internal) IS 'S'")
	enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION unaccent_lexize(internal,internal,internal,internal) IS 'S'")
	return e
}

func sepgsqlDictionaryLexize(t *testing.T, e *core.Engine, op *session.Operation) error {
	t.Helper()
	result, err := e.Execute(op, enginetest.Statement(t, "SELECT ts_lexize('unaccent','café')"), 0, nil)
	if result.Rows != nil {
		defer result.Rows.Close()
	}
	return err
}

// The adapter delegates real authority and pauses only after the actual selected
// Init has entered. It never supplies a grant, fake runtime or catalog identity.
type dictionaryInitBarrier struct {
	tsearch.Host[core.AuthorizationView]
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *dictionaryInitBarrier) EnterDictionaryCallback(callback catalog.DictionaryCallback) (sqltype.RoutineBodyToken, error) {
	token, err := h.Host.EnterDictionaryCallback(callback)
	if err == nil && callback.Kind == catalog.DictionaryInitCallback {
		h.once.Do(func() { close(h.entered); <-h.release })
	}
	return token, err
}

func TestSepgsqlSimulationDictionaries(t *testing.T) {
	t.Run("actual_callback_rejects_foreign_invalid_cancel_ended", func(t *testing.T) {
		e := sepgsqlDictionaryEngine(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, done, err := enginetest.Operation(e, ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(nil)
		if err := scope.Gate(enginetest.Statement(t, "SELECT ts_lexize('unaccent','café')")); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := scope.RoutineAuthorizationLocked()
		if err == nil {
			err = scope.CaptureIdentityLocked(auth)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		host := core.HostOf(scope).(tsearch.Host[core.AuthorizationView])
		read := auth.Catalog
		dictionary := read.MustTSDictionary(thesaurusUnitAddress(e, ids.TSDictionaryClassID, "unaccent"))
		template, ok := read.TSTemplate(dictionary.Template)
		if !ok {
			t.Fatal("missing actual dictionary template")
		}
		helper, ok := read.Procedure(template.Lexize)
		if !ok {
			t.Fatal("missing actual callback helper")
		}
		callback := catalog.DictionaryCallback{Read: read, Dictionary: dictionary, Template: template, Helper: helper, Kind: catalog.DictionaryLexizeCallback}
		token, err := host.EnterDictionaryCallback(callback)
		if err != nil {
			t.Fatal(err)
		}
		if op.Session.Sepgsql.Current() != "T" {
			token.Leave()
			t.Fatal("actual direct callback did not enter its trusted subject")
		}
		token.Leave()
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("actual direct callback failed to unwind")
		}
		foreign := sepgsqlDictionaryEngine(t)
		bad := callback
		bad.Read = foreign.CatalogRead()
		if _, err := host.EnterDictionaryCallback(bad); err == nil {
			t.Fatal("foreign callback read acquired original authority")
		}
		bad = callback
		bad.Helper = nil
		if _, err := host.EnterDictionaryCallback(bad); err == nil {
			t.Fatal("missing helper acquired callback authority")
		}
		bad = callback
		bad.Kind = catalog.DictionaryCallbackKind(255)
		if _, err := host.EnterDictionaryCallback(bad); err == nil {
			t.Fatal("invalid direction acquired callback authority")
		}
		cancel()
		if _, err := host.EnterDictionaryCallback(callback); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled callback entered body", err)
		}
		scope.Finish(context.Canceled)
		if _, err := host.EnterDictionaryCallback(callback); err == nil {
			t.Fatal("ended scope retained dictionary authority")
		}
	})
	t.Run("compiled_candidate_later_failure_keeps_older_grant", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "CREATE EXTENSION unaccent")
		op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs(v),abs(v) FROM init_owner_rows")
		host, owner := core.HostOf(scope), evaluation.RoutineInitialization
		if err := exec.AdmitExpression(evaluation, project.Expressions[0], evaluation.Catalog); err != nil {
			t.Fatal(err)
		}
		a := sepgsqlInitializationSite(t, evaluation, project.Expressions[0])
		statement := enginetest.Statement(t, "CREATE TEXT SEARCH DICTIONARY sepgsql_candidate (TEMPLATE=public.unaccent,RULES=unaccent)")
		stage, preparation := dictionaryUnitPrepare(t, e, op, statement)
		defer e.DiscardCatalogStage(stage)
		if preparation.Runtime == nil || thesaurusReceipts(op.Session).Len() != 0 {
			t.Fatal("genuine DDL compile missing or query memo published")
		}
		e.Mu.Lock()
		completed, err := schemacmds.CompleteDictionaryLocked(e, op, statement, stage, &preparation)
		e.Mu.Unlock()
		if err != nil || completed != stage {
			t.Fatal("actual candidate completion", err)
		}
		beforeB := scope.MarkSepgsqlObservations()
		if err := exec.AdmitExpression(evaluation, project.Expressions[1], evaluation.Catalog); err != nil {
			t.Fatal(err)
		}
		b := sepgsqlInitializationSite(t, evaluation, project.Expressions[1])
		afterB := scope.MarkSepgsqlObservations()
		if beforeB.Observations != afterB.Observations || beforeB.Initializations >= afterB.Initializations {
			t.Fatal("actual grants did not share deduplicated observation")
		}
		ctx, cancel := context.WithCancel(op.Context())
		cancel()
		if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
			t.Fatal("later real candidate validation did not fail", err)
		}
		e.DiscardCatalogStage(stage)
		if _, err := host.ValidateRoutineInitialization(owner, a); err != nil {
			t.Fatal("candidate abort removed older genuine grant", err)
		}
		if _, err := host.ValidateRoutineInitialization(owner, b); err == nil {
			t.Fatal("candidate abort retained newer deduplicated grant")
		}
		if thesaurusReceipts(op.Session).Len() != 0 || e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "sepgsql_candidate"}).ObjectID != 0 {
			t.Fatal("failed candidate published catalog or query memo")
		}
	})
	t.Run("detached_peer_label_rejected_before_compile_keeps_older_grant", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "CREATE EXTENSION unaccent")
		op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs(v),abs(v) FROM init_owner_rows")
		if err := exec.AdmitExpression(evaluation, project.Expressions[0], evaluation.Catalog); err != nil {
			t.Fatal(err)
		}
		host, owner := core.HostOf(scope), evaluation.RoutineInitialization
		site := sepgsqlInitializationSite(t, evaluation, project.Expressions[0])
		statement := enginetest.Statement(t, "CREATE TEXT SEARCH DICTIONARY sepgsql_stale_candidate (TEMPLATE=public.unaccent,RULES=unaccent)")
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		stage := e.BeginCatalogStage(auth.Catalog)
		stage.AuthorizeLocked(auth, op.Session)
		preparation, err := extension.PrepareDictionary(schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, statement)
		e.Mu.Unlock()
		defer e.DiscardCatalogStage(stage)
		if err != nil {
			t.Fatal(err)
		}
		key := catalog.SecurityLabelKey{Object: preparation.Template.Init, Provider: "selinux"}
		original := stage.Base.State().SecurityLabel(key)
		mark := scope.MarkSepgsqlObservations()
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION unaccent_init(internal) IS 'D'")
		if e.CatalogRead().State().SecurityLabel(key) == original {
			t.Fatal("peer did not publish a genuine helper label revision")
		}
		err = preparation.Compile(op.Context(), core.DDLHost{E: e, Op: op, CatalogStage: stage})
		if !errors.Is(err, ids.ErrCatalogReplaced) || preparation.Runtime != nil {
			t.Fatal("detached compiler adopted peer label authority", err)
		}
		if scope.MarkSepgsqlObservations() != mark || stage.Base.State().SecurityLabel(key) != original ||
			thesaurusReceipts(op.Session).Len() != 0 {
			t.Fatal("rejected preparation changed original fences, older observations or query state")
		}
		e.DiscardCatalogStage(stage)
		if _, err := host.ValidateRoutineInitialization(owner, site); err != nil {
			t.Fatal("rejected preparation removed the older genuine grant", err)
		}
	})
	t.Run("physical_cold_warm_subject_relabel_generation_failure", func(t *testing.T) {
		e := sepgsqlDictionaryEngine(t)
		warm, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		if thesaurusReceipts(warm.Session).Len() != 0 {
			t.Fatal("DDL warmed physical query state")
		}
		if err := sepgsqlDictionaryLexize(t, e, warm); err != nil {
			t.Fatal(err)
		}
		if thesaurusReceipts(warm.Session).Len() != 1 {
			t.Fatal("cold success did not publish its actual memo")
		}
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION unaccent_init(internal) IS 'U'")
		enginetest.RoleExecute(t, e, warm, "SELECT sepgsql_setcon('R')")
		if err := sepgsqlDictionaryLexize(t, e, warm); err != nil {
			t.Fatal("warm subject/relabel change reran Init", err)
		}
		cold, closeCold := enginetest.RoleOperation(t, e, "postgres")
		defer closeCold()
		enginetest.RoleExecute(t, e, cold, "SELECT sepgsql_setcon('R')")
		enginetest.State(t, sepgsqlDictionaryLexize(t, e, cold), "42501")
		if thesaurusReceipts(cold.Session).Len() != 0 || cold.Session.Sepgsql.Current() != "R" {
			t.Fatal("failed cold Init published or leaked subject")
		}
		enginetest.RoleExecute(t, e, cold, "SELECT sepgsql_setcon(NULL)")
		if err := sepgsqlDictionaryLexize(t, e, cold); err != nil {
			t.Fatal("failed Init poisoned later allowed initialization", err)
		}
		enginetest.RoleExecute(t, e, cold, "SELECT sepgsql_setcon('R')")
		if err := sepgsqlDictionaryLexize(t, e, cold); err != nil {
			t.Fatal(err)
		}
		enginetest.Run(t, e, "ALTER FUNCTION unaccent_init(internal) RENAME TO sepgsql_dictionary_init")
		enginetest.State(t, sepgsqlDictionaryLexize(t, e, warm), "42501")
		if warm.Session.Sepgsql.Current() != "R" {
			t.Fatal("generation refresh failure leaked subject")
		}
	})

	t.Run("filtering_normalizer_rejects_unchanged_sample", func(t *testing.T) {
		e := sepgsqlDictionaryEngine(t)
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		// Stock unaccent recognizes only changed words. The released sample's
		// first ASCII word cannot be normalized by that filtering dictionary.
		result, err := e.Execute(op, enginetest.Statement(t, "CREATE TEXT SEARCH DICTIONARY sepgsql_invalid_thes (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='public.unaccent')"), 0, nil)
		if result.Rows != nil {
			result.Rows.Close()
		}
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: "F0000", Message: `thesaurus sample word "one" isn't recognized by subdictionary (rule 1)`})
		if thesaurusReceipts(op.Session).Len() != 0 || op.Session.Sepgsql.Current() != "A" ||
			thesaurusUnitAddress(e, ids.TSDictionaryClassID, "sepgsql_invalid_thes").ObjectID != 0 {
			t.Fatal("invalid normalizer published dictionary, query memo or subject")
		}
	})
	t.Run("nested_normalizer_failure_and_recovery", func(t *testing.T) {
		e := sepgsqlDictionaryEngine(t)
		// Snowball is a genuine C callback pair, but unlike filtering unaccent
		// it recognizes the complete released thesaurus sample.
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.dsnowball_init(internal) IS 'S'")
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.dsnowball_lexize(internal,internal,internal,internal) IS 'S'")
		enginetest.Run(t, e, "CREATE TEXT SEARCH DICTIONARY sepgsql_thes (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='pg_catalog.english_stem')")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		if thesaurusReceipts(op.Session).Len() != 0 {
			t.Fatal("DDL normalizer warmed query state")
		}
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.dsnowball_lexize(internal,internal,internal,internal) IS 'U'")
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		query := enginetest.Statement(t, "SELECT ts_lexize('sepgsql_thes','one')::text")
		result, err := e.Execute(op, query, 0, nil)
		if result.Rows != nil {
			result.Rows.Close()
		}
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "SELinux: security policy violation"})
		if thesaurusReceipts(op.Session).Len() != 0 || op.Session.Sepgsql.Current() != "R" {
			t.Fatal("failed outer compiler published normalizer memo or subject")
		}
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.dsnowball_lexize(internal,internal,internal,internal) IS 'S'")
		result, err = e.Execute(op, query, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Rows.Close()
		rows := enginetest.Tuples(result)
		if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != "{1}" {
			t.Fatal("actual nested normalizer output", rows)
		}
		if thesaurusReceipts(op.Session).Len() != 2 || op.Session.Sepgsql.Current() != "R" {
			t.Fatal("successful outer acquisition did not jointly publish or unwind")
		}
	})
	for _, change := range []string{"helper", "dictionary", "role", "cancel", "nested_helper", "nested_cancel"} {
		t.Run("cold_publication_"+change, func(t *testing.T) {
			e := sepgsqlDictionaryEngine(t)
			nested := strings.HasPrefix(change, "nested_")
			name, input, helper := "unaccent", "café", "public.unaccent_init"
			if nested {
				enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.dsnowball_init(internal) IS 'S'")
				enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.dsnowball_lexize(internal,internal,internal,internal) IS 'S'")
				enginetest.Run(t, e, "CREATE TEXT SEARCH DICTIONARY sepgsql_thes (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='pg_catalog.english_stem')")
				name, input, helper = "sepgsql_thes", "one", "pg_catalog.dsnowball_init"
			}
			ctx, cancel := context.WithTimeout(context.Background(), enginetest.Liveness)
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			if err := scope.Gate(enginetest.Statement(t, "SELECT ts_lexize('"+name+"','"+input+"')")); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := scope.RoutineAuthorizationLocked()
			if err == nil {
				err = scope.CaptureIdentityLocked(auth)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			host, ok := core.HostOf(scope).(tsearch.Host[core.AuthorizationView])
			if !ok {
				t.Fatal("actual statement host lost dictionary adapter")
			}
			barrier := &dictionaryInitBarrier{Host: host, entered: make(chan struct{}), release: make(chan struct{})}
			read := auth.Catalog
			dictionary := thesaurusUnitAddress(e, ids.TSDictionaryClassID, name)
			regdictionary, _ := read.LookupType(sqltype.Regdictionary)
			selector, err := sqltype.RegTSValue(regdictionary, uint32(dictionary.ObjectID))
			if err != nil {
				t.Fatal(err)
			}
			array, _ := read.LookupType(1009)
			finished := make(chan error, 1)
			go func() { _, err := tsearch.Lexize(barrier, selector, input, array); finished <- err }()
			var release sync.Once
			unblock := func() { release.Do(func() { close(barrier.release) }) }
			joined := false
			defer func() {
				unblock()
				if !joined {
					<-finished
				}
			}()
			select {
			case <-barrier.entered:
			case err := <-finished:
				joined = true
				t.Fatalf("Init did not enter: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if op.Session.Sepgsql.Current() != "T" {
				t.Fatal("barrier did not pause actual trusted Init")
			}
			switch change {
			case "helper", "nested_helper":
				enginetest.Run(t, e, "ALTER FUNCTION "+helper+"(internal) RENAME TO sepgsql_stale_init")
			case "dictionary":
				enginetest.Run(t, e, "ALTER TEXT SEARCH DICTIONARY unaccent (RULES=unaccent)")
			case "role":
				enginetest.Run(t, e, "CREATE ROLE sepgsql_dictionary_peer")
			case "cancel", "nested_cancel":
				cancel()
			}
			unblock()
			err = <-finished
			joined = true
			if strings.HasSuffix(change, "cancel") {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("canceled cold publication", err)
				}
			} else if err == nil {
				t.Fatal("stale cold initialization published")
			}
			if thesaurusReceipts(op.Session).Len() != 0 || op.Session.Sepgsql.Current() != "R" {
				t.Fatal("failed cold publication retained memo or trusted subject")
			}
		})
	}
}
