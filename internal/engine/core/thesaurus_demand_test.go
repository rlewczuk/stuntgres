package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
)

// Inspect actual private receipts without exporting authority just for tests.
// Pointer reads distinguish retained runtime/source backing from output equality.
func thesaurusReceipts(s *session.Session) reflect.Value {
	return reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("thesaurusMemos")
}

func thesaurusReceiptBacking(s *session.Session, address ids.ObjectAddress) (uintptr, uintptr) {
	memo := thesaurusReceipts(s).MapIndex(reflect.ValueOf(address))
	if !memo.IsValid() {
		return 0, 0
	}
	return memo.FieldByName("runtime").Elem().Pointer(), memo.FieldByName("normalizer").Pointer()
}

func thesaurusUnitAddress(e *core.Engine, class ids.OID, name string) ids.ObjectAddress {
	return e.CatalogRead().NameAddress(ids.NameKey{ClassID: class, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
}

func thesaurusUnitConvert(t *testing.T, e *core.Engine, s *session.Session, ctx context.Context, input string, gate func(workadmission.Class) error) (sqltype.Value, error) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	view := e.CatalogRead()
	configType, _ := view.LookupType(sqltype.Regconfig)
	vectorType, _ := view.LookupType(sqltype.TSVector)
	address := thesaurusUnitAddress(e, ids.TSConfigurationClassID, "thes_unit_cfg")
	config, err := sqltype.RegTSValue(configType, uint32(address.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	if gate != nil {
		ctx = workadmission.Activate(workadmission.With(ctx, func(_ context.Context, class workadmission.Class) error {
			if !e.Mu.TryLock() {
				t.Fatal("dictionary work callback holds Engine.Mu")
			}
			e.Mu.Unlock()
			return gate(class)
		}))
	}
	return core.HostOf(scope).ConvertTextSearch(ctx, fts.VectorConversion, true, config, input, vectorType)
}

func thesaurusUnitVector(t *testing.T, value sqltype.Value, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := value.Type().Output(context.Background(), value)
	if err != nil || actual != want {
		t.Fatalf("vector = %q, %v; want %q", actual, err, want)
	}
}

func TestThesaurusDemand(t *testing.T) {
	e, s := thesaurusCoreSetup(t)
	dictionary := thesaurusUnitAddress(e, ids.TSDictionaryClassID, "thes_unit")
	source := thesaurusUnitAddress(e, ids.TSDictionaryClassID, "thes_unit_sub")
	if thesaurusReceipts(s).Len() != 0 {
		t.Fatal("DDL initialized execution receipt")
	}
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.thes_unit_sub (Accept=false)")
	for _, tc := range []struct{ input, want string }{{"7", "'7':1"}, {"", ""}} {
		value, err := thesaurusUnitConvert(t, e, s, context.Background(), tc.input, nil)
		thesaurusUnitVector(t, value, err, tc.want)
		if thesaurusReceipts(s).Len() != 0 {
			t.Fatal("unused category/empty initialized dictionary")
		}
	}
	for _, first := range []string{"pg_catalog.simple", "public.thes_unit_sub"} {
		enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.thes_unit_cfg ALTER MAPPING FOR asciiword WITH "+first+",public.thes_unit")
		input, want := "one", "'one':1"
		if first == "public.thes_unit_sub" {
			input, want = "the", ""
		}
		value, err := thesaurusUnitConvert(t, e, s, context.Background(), input, nil)
		thesaurusUnitVector(t, value, err, want)
		if thesaurusReceipts(s).Len() != 0 {
			t.Fatal("recognized token/stopword initialized unreachable dictionary")
		}
	}
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.thes_unit_cfg ALTER MAPPING FOR asciiword WITH public.thes_unit,public.thes_unit_sub")
	value, err := thesaurusUnitConvert(t, e, s, context.Background(), "one", nil)
	var diagnostic *sqlstate.Error
	if value.Type() != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != `thesaurus sample word "one" isn't recognized by subdictionary (rule 1)` || thesaurusReceipts(s).Len() != 0 {
		t.Fatal("demanded failure suppressed or installed receipt", value, err)
	}
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.thes_unit_sub (Accept=true)")
	value, err = thesaurusUnitConvert(t, e, s, context.Background(), "one two three", nil)
	thesaurusUnitVector(t, value, err, "'123':1")
	runtime, normalizer := thesaurusReceiptBacking(s, dictionary)
	if runtime == 0 || normalizer != reflect.ValueOf(e.CatalogRead().MustTSDictionary(source)).Pointer() || thesaurusReceipts(s).Len() != 1 {
		t.Fatal("missing genuine initialized runtime/source backing")
	}
	value, err = thesaurusUnitConvert(t, e, s, context.Background(), "one 7 one", nil)
	thesaurusUnitVector(t, value, err, "'1':3 '7':2 'one':1")
	again, againSource := thesaurusReceiptBacking(s, dictionary)
	if runtime != again || normalizer != againSource {
		t.Fatal("unchanged session receipt/runtime was replaced")
	}
	other := enginetest.StartSession(t, e, nil)
	if thesaurusReceipts(other).Len() != 0 {
		t.Fatal("new session inherited initialization receipt")
	}
	value, err = thesaurusUnitConvert(t, e, other, context.Background(), "one", nil)
	thesaurusUnitVector(t, value, err, "'1':1")
	otherRuntime, _ := thesaurusReceiptBacking(other, dictionary)
	if otherRuntime == 0 || otherRuntime == runtime {
		t.Fatal("new session borrowed compiled runtime authority")
	}
	t.Run("ContinuationMembership", func(t *testing.T) {
		e, s := thesaurusCoreSetup(t)
		for _, query := range []string{
			"ALTER TEXT SEARCH DICTIONARY public.thes_unit (DictFile=stuntgres_thes_cross_category)",
			"CREATE TEXT SEARCH DICTIONARY public.thes_bad_sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english)",
			"CREATE TEXT SEARCH DICTIONARY public.thes_bad (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='public.thes_bad_sub')",
			"ALTER TEXT SEARCH CONFIGURATION public.thes_unit_cfg ALTER MAPPING FOR word WITH public.thes_bad,public.thes_unit,public.thes_unit_sub",
			"ALTER TEXT SEARCH DICTIONARY public.thes_bad_sub (Accept=false)",
		} {
			enginetest.SessionSQL(t, s, query)
		}
		bad := thesaurusUnitAddress(e, ids.TSDictionaryClassID, "thes_bad")
		for range 2 {
			value, err := thesaurusUnitConvert(t, e, s, context.Background(), "alpha café", nil)
			thesaurusUnitVector(t, value, err, "'cross':1")
			if runtime, _ := thesaurusReceiptBacking(s, bad); runtime != 0 || thesaurusReceipts(s).Len() != 1 {
				t.Fatal("continuation membership initialized preceding unrelated dictionary")
			}
		}
	})
	t.Run("FailureAfterPrefix", func(t *testing.T) {
		e, s := thesaurusCoreSetup(t)
		for _, query := range []string{
			"CREATE TEXT SEARCH DICTIONARY public.thes_bad_sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english)",
			"CREATE TEXT SEARCH DICTIONARY public.thes_bad (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='public.thes_bad_sub')",
			"ALTER TEXT SEARCH CONFIGURATION public.thes_unit_cfg ALTER MAPPING FOR word WITH public.thes_bad,public.thes_unit_sub",
			"ALTER TEXT SEARCH DICTIONARY public.thes_bad_sub (Accept=false)",
		} {
			enginetest.SessionSQL(t, s, query)
		}
		value, err := thesaurusUnitConvert(t, e, s, context.Background(), "one café", nil)
		var diagnostic *sqlstate.Error
		if value.Type() != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != `thesaurus sample word "one" isn't recognized by subdictionary (rule 1)` || thesaurusReceipts(s).Len() != 0 {
			t.Fatal("late demanded failure installed prefix initialization", value, err)
		}
		value, err = thesaurusUnitConvert(t, e, s, context.Background(), "one", nil)
		thesaurusUnitVector(t, value, err, "'1':1")
		if thesaurusReceipts(s).Len() != 1 {
			t.Fatal("failed demand poisoned independent recovery")
		}
	})
}

func TestThesaurusDemandCurrentness(t *testing.T) {
	for _, change := range []string{"config_before_demand", "normalizer_before_demand", "config_after_prefix", "normalizer_after_prefix", "cancel_after_prefix"} {
		t.Run(change, func(t *testing.T) {
			e, s := thesaurusCoreSetup(t)
			dictionary := thesaurusUnitAddress(e, ids.TSDictionaryClassID, "thes_unit")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			heads, gated := 0, false
			gate := func(class workadmission.Class) error {
				if class != workadmission.WorkTSVectorConvert {
					return nil
				}
				heads++
				target := 1
				if change == "config_after_prefix" || change == "normalizer_after_prefix" || change == "cancel_after_prefix" {
					target = 2
				}
				if heads != target {
					return nil
				}
				gated = true
				if thesaurusReceipts(s).Len() != 0 {
					t.Fatal("unaccepted conversion installed prefix receipt")
				}
				if change == "cancel_after_prefix" {
					cancel()
					return ctx.Err()
				}
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				view := e.CatalogRead()
				stage := e.BeginCatalogStage(view)
				defer e.DiscardCatalogStage(stage)
				if change == "config_before_demand" || change == "config_after_prefix" {
					address := thesaurusUnitAddress(e, ids.TSConfigurationClassID, "thes_unit_cfg")
					config := *view.MustTSConfiguration(address)
					config.ObjectRevision = config.Revision() + 1
					if err := stage.Replace(&config); err != nil {
						t.Fatal(err)
					}
				} else {
					address := thesaurusUnitAddress(e, ids.TSDictionaryClassID, "thes_unit_sub")
					source := *view.MustTSDictionary(address)
					source.ObjectRevision = source.Revision() + 1
					// New genuine valid backing distinguishes currentness rejection
					// from an initialization error; no fake dictionary is involved.
					runtime, err := functions.CompileSimpleDictionary(context.Background(), true, "english")
					if err != nil {
						t.Fatal(err)
					}
					source.Runtime = runtime
					if err := stage.Replace(&source); err != nil {
						t.Fatal(err)
					}
				}
				extensionUnitPublish(t, e, op, stage)
				return nil
			}
			input := "one 7"
			if change == "config_after_prefix" || change == "normalizer_after_prefix" || change == "cancel_after_prefix" {
				input = "one "
			}
			value, err := thesaurusUnitConvert(t, e, s, ctx, input, gate)
			want := ids.ErrCatalogReplaced
			if change == "cancel_after_prefix" {
				want = context.Canceled
			}
			if !gated || value.Type() != nil || !errors.Is(err, want) || thesaurusReceipts(s).Len() != 0 {
				t.Fatal("rejected capture published prefix/receipt or mixed generations", value, err, gated)
			}
			if runtime, _ := thesaurusReceiptBacking(s, dictionary); runtime != 0 {
				t.Fatal("rejected receipt survived")
			}
			value, err = thesaurusUnitConvert(t, e, s, context.Background(), "one", nil)
			thesaurusUnitVector(t, value, err, "'1':1")
			if runtime, _ := thesaurusReceiptBacking(s, dictionary); runtime == 0 {
				t.Fatal("valid recovery did not initialize")
			}
		})
	}
}

// The former scalar-only NormalizeTokens position assertions exercise the real
// composed owner here. Genuine simple stopwords and synonym prefix metadata
// distinguish unmapped/unrecognized input, recognized-empty input and lexemes.
func TestFTSConvertNormalizationOwner(t *testing.T) {
	e, s := thesaurusCoreSetup(t)
	for _, query := range []string{
		"CREATE TEXT SEARCH DICTIONARY public.convert_lexical_syn (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)",
		"ALTER TEXT SEARCH DICTIONARY public.thes_unit_sub (Accept=false)",
		"ALTER TEXT SEARCH CONFIGURATION public.thes_unit_cfg ALTER MAPPING FOR asciiword WITH public.thes_unit_sub,public.convert_lexical_syn",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	value, err := thesaurusUnitConvert(t, e, s, context.Background(), "skip the indices", nil)
	thesaurusUnitVector(t, value, err, "'index':2")
	if thesaurusReceipts(s).Len() != 0 {
		t.Fatal("rich normalization initialized unreachable thesaurus")
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	view := e.CatalogRead()
	configType, _ := view.LookupType(sqltype.Regconfig)
	queryType, _ := view.LookupType(sqltype.TSQuery)
	config, err := sqltype.RegTSValue(configType, uint32(thesaurusUnitAddress(e, ids.TSConfigurationClassID, "thes_unit_cfg").ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{
		{"indices", "'index':*"}, {"indices:AB", "'index':*AB"}, {"indices:*AB", "'index':*AB"},
	} {
		value, err := core.HostOf(scope).ConvertTextSearch(context.Background(), fts.StrictConversion, true, config, tc.input, queryType)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := value.Type().Output(context.Background(), value)
		if err != nil || actual != tc.want {
			t.Fatal(tc.input, actual, err)
		}
	}
}
