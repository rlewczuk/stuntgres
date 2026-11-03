package textsearch_test

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/tsearch"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestFTSHeadlineRichWalk(t *testing.T) {
	for _, tc := range []struct {
		config, document, query, want string
		positions                     []fts.LexicalPosition
	}{
		{"english", "The fat cats ran", "cat", "The fat <b>cats</b> ran", []fts.LexicalPosition{
			{Position: 1, Lexemes: []fts.Lexeme{}}, {Position: 2, Lexemes: []fts.Lexeme{{Text: "fat"}}}, {Position: 3, Lexemes: []fts.Lexeme{{Text: "cat"}}}, {Position: 4, Lexemes: []fts.Lexeme{{Text: "ran"}}},
		}},
		{"simple", "The fat cats ran", "cat", "The fat cats ran", []fts.LexicalPosition{
			{Position: 1, Lexemes: []fts.Lexeme{{Text: "the"}}}, {Position: 2, Lexemes: []fts.Lexeme{{Text: "fat"}}}, {Position: 3, Lexemes: []fts.Lexeme{{Text: "cats"}}}, {Position: 4, Lexemes: []fts.Lexeme{{Text: "ran"}}},
		}},
	} {
		t.Run(tc.config, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
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
			h := core.HostOf(scope).(tsearch.Host[core.AuthorizationView])
			read := e.CatalogRead()
			address := read.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: tc.config})
			capture, err := tsearch.CaptureConfiguration[core.AuthorizationView](t.Context(), read, uint32(address.ObjectID))
			if err != nil {
				t.Fatal(err)
			}
			ctx := h.ActivateRuntime(t.Context())
			position := 0
			var positions []fts.LexicalPosition
			if err := capture.Walk(ctx, h, tc.document, &position, func(p fts.LexicalPosition) { positions = append(positions, p) }); err != nil {
				t.Fatal(err)
			}
			if len(positions) != len(tc.positions) || position != len(tc.positions) {
				t.Fatal("normalization positions", position, positions)
			}
			for i := range positions {
				if positions[i].Position != tc.positions[i].Position || len(positions[i].Lexemes) != len(tc.positions[i].Lexemes) {
					t.Fatal("position/stopword", i, positions[i])
				}
				for j := range positions[i].Lexemes {
					if positions[i].Lexemes[j] != tc.positions[i].Lexemes[j] {
						t.Fatal("configured lexical value", i, j, positions[i])
					}
				}
			}
			document, err := capture.WalkDocument(ctx, h, tc.document)
			if err != nil {
				t.Fatal(err)
			}
			query, err := fts.ParseQuery(ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			options, err := fts.ParseHeadlineOptions(ctx, "HighlightAll=true", func(string) (int32, error) { t.Fatal("unexpected integer option"); return 0, nil })
			if err != nil {
				t.Fatal(err)
			}
			text, err := document.Headline(ctx, query, options)
			if err != nil || text != tc.want {
				t.Fatal("rich associations/raw rendering", text, tc.want, err)
			}
			if err := capture.Accept(ctx, h); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, mode := range []string{"synonym", "filter", "phrase", "replacement_positions", "unreached_dictionary", "long_token"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			config, document, query, want, vector := "unit_rich_cfg", "POSTGRES WORD", "pgsql", "<b>POSTGRES</b> WORD", "'pgsql':1 'word':2"
			var setup []string
			switch mode {
			case "synonym":
				setup = []string{"CREATE TEXT SEARCH DICTIONARY public.unit_rich_dict (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)", "CREATE TEXT SEARCH CONFIGURATION public.unit_rich_cfg (COPY=pg_catalog.simple)", "ALTER TEXT SEARCH CONFIGURATION public.unit_rich_cfg ALTER MAPPING FOR asciiword WITH public.unit_rich_dict,pg_catalog.simple"}
			case "filter":
				document, query, want, vector = "Hôtel plain", "hotel", "<b>Hôtel</b> plain", "'hotel':1 'plain':2"
				setup = []string{"CREATE EXTENSION unaccent", "CREATE TEXT SEARCH CONFIGURATION public.unit_rich_cfg (COPY=pg_catalog.simple)", "ALTER TEXT SEARCH CONFIGURATION public.unit_rich_cfg ALTER MAPPING FOR word,asciiword WITH public.unaccent,pg_catalog.simple"}
			case "phrase", "replacement_positions":
				document, query, want, vector = "ONE TWO THREE", "123", "<b>ONE</b><b> </b><b>TWO</b><b> </b><b>THREE</b>", "'123':1"
				if mode == "replacement_positions" {
					document, query, want, vector = "BOOKING THE TICKETS", "order & invitation & cards", "<b>BOOKING</b><b> </b><b>THE</b><b> </b><b>TICKETS</b>", "'cards':3 'invitation':2 'order':1"
				}
				setup = []string{"CREATE TEXT SEARCH DICTIONARY public.unit_rich_sub (TEMPLATE=pg_catalog.simple,StopWords=english)", "CREATE TEXT SEARCH DICTIONARY public.unit_rich_dict (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary=public.unit_rich_sub)", "CREATE TEXT SEARCH CONFIGURATION public.unit_rich_cfg (COPY=pg_catalog.simple)", "ALTER TEXT SEARCH CONFIGURATION public.unit_rich_cfg ALTER MAPPING FOR asciiword WITH public.unit_rich_dict,pg_catalog.simple"}
			case "unreached_dictionary":
				document, query, want, vector = "ONE", "1", "ONE", "'one':1"
				setup = []string{"CREATE TEXT SEARCH DICTIONARY public.unit_rich_sub (TEMPLATE=pg_catalog.simple)", "CREATE TEXT SEARCH DICTIONARY public.unit_rich_dict (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary=public.unit_rich_sub)", "ALTER TEXT SEARCH DICTIONARY public.unit_rich_sub RENAME TO unit_rich_sub_renamed", "CREATE TEXT SEARCH CONFIGURATION public.unit_rich_cfg (COPY=pg_catalog.simple)", "ALTER TEXT SEARCH CONFIGURATION public.unit_rich_cfg ALTER MAPPING FOR asciiword WITH pg_catalog.simple,public.unit_rich_dict"}
			case "long_token":
				config, document, query, want, vector = "simple", strings.Repeat("x", 2047)+" Cat", "cat", " <b>Cat</b>", "'cat':1"
			}
			for _, sql := range setup {
				enginetest.SessionSQL(t, s, sql)
			}
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
			h := core.HostOf(scope).(tsearch.Host[core.AuthorizationView])
			read := e.CatalogRead()
			namespace := ids.PublicNamespaceOID
			if mode == "long_token" {
				namespace = ids.PGCatalogNamespaceOID
			}
			address := read.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: namespace}, Name: config})
			context := &headlineDetachedContext{Context: t.Context(), engine: e, t: t}
			capture, err := tsearch.CaptureConfiguration[core.AuthorizationView](context, read, uint32(address.ObjectID))
			if err != nil {
				t.Fatal(err)
			}
			ctx := h.ActivateRuntime(context)
			position := 0
			var builder fts.VectorBuilder
			if err := capture.Walk(ctx, h, document, &position, func(p fts.LexicalPosition) {
				for _, lexeme := range p.Lexemes {
					builder.Add(lexeme.Text, p.Position)
				}
			}); err != nil {
				t.Fatal(err)
			}
			normalized, err := builder.Finish(ctx)
			if err != nil || normalized.String() != vector {
				t.Fatal("scalar walk changed", normalized, vector, err)
			}
			rich, err := capture.WalkDocument(ctx, h, document)
			if err != nil {
				t.Fatal(err)
			}
			q, err := fts.ParseQuery(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			options, err := fts.ParseHeadlineOptions(ctx, "HighlightAll=true", func(string) (int32, error) { t.Fatal("unexpected integer"); return 0, nil })
			if err != nil {
				t.Fatal(err)
			}
			text, err := rich.Headline(ctx, q, options)
			if err != nil || text != want {
				t.Fatal("rich configured associations", text, want, err)
			}
			if len(*h.ThesaurusMemos()) != 0 {
				t.Fatal("walk published receipts")
			}
			if err := capture.Accept(ctx, h); err != nil {
				t.Fatal(err)
			}
			wantReceipts := 0
			if mode == "phrase" || mode == "replacement_positions" {
				wantReceipts = 1
			}
			if len(*h.ThesaurusMemos()) != wantReceipts || context.lexicalCalls == 0 {
				t.Fatal("consulted runtime/callback witness", len(*h.ThesaurusMemos()), context.lexicalCalls)
			}
			if mode == "unreached_dictionary" {
				// Valid creation, then a real normalizer rename leaves only the
				// dictionary's logical source absent. Simple-first must not demand
				// it; moving the same dictionary first must now consult and fail.
				other := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, other, "ALTER TEXT SEARCH CONFIGURATION public.unit_rich_cfg ALTER MAPPING FOR asciiword WITH public.unit_rich_dict,pg_catalog.simple")
				current := e.CatalogRead()
				demanded, err := tsearch.CaptureConfiguration[core.AuthorizationView](context, current, uint32(address.ObjectID))
				if err != nil {
					t.Fatal(err)
				}
				partial, err := demanded.WalkDocument(ctx, h, document)
				var diagnostic *sqlstate.Error
				if partial != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != `text search dictionary "public.unit_rich_sub" does not exist` || len(*h.ThesaurusMemos()) != 0 {
					t.Fatal("consulted missing source escaped or published receipts", partial, err)
				}
			}
			if mode == "long_token" {
				wantNotice := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."}
				if notices := op.DetachNotices(); !reflect.DeepEqual(notices, []sqlstate.Error{wantNotice, wantNotice}) {
					t.Fatal("scalar/rich notice count or fields", notices)
				}
			}
		})
	}
}

func TestFTSHeadlineAcquisition(t *testing.T) {
	for _, mode := range []string{"options_failure", "cancel_before_accept", "stale_before_accept", "success"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.unit_headline_dict (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary=pg_catalog.english_stem)")
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.unit_headline_cfg (COPY=pg_catalog.simple)")
			enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.unit_headline_cfg ALTER MAPPING FOR asciiword WITH public.unit_headline_dict,pg_catalog.english_stem")
			other := enginetest.StartSession(t, e, nil)
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
			h := core.HostOf(scope).(tsearch.Host[core.AuthorizationView])
			read := e.CatalogRead()
			address := read.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "unit_headline_cfg"})
			typeOf := func(id sqltype.ID) *sqltype.Descriptor {
				return read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}).SQLType
			}
			configuration, err := sqltype.RegTSValue(typeOf(sqltype.Regconfig), uint32(address.ObjectID))
			if err != nil {
				t.Fatal(err)
			}
			query, err := typeOf(sqltype.TSQuery).Input(t.Context(), "123", -1)
			if err != nil {
				t.Fatal(err)
			}
			if len(*h.ThesaurusMemos()) != 0 {
				t.Fatal("fixture already initialized thesaurus")
			}
			if mode == "options_failure" || mode == "success" {
				options := "HighlightAll=true"
				if mode == "options_failure" {
					options = "Bad=1"
				}
				value, err := core.HostOf(scope).TextSearch().HeadlineTextSearch(t.Context(), true, configuration, sqltype.String(typeOf(sqltype.Text), "ONE TWO THREE"), query, sqltype.String(typeOf(sqltype.Text), options), typeOf(sqltype.Text))
				if mode == "options_failure" {
					if err == nil || len(*h.ThesaurusMemos()) != 0 {
						t.Fatal("failed headline published receipts", err)
					}
					return
				}
				text, textErr := value.Text()
				if err != nil || textErr != nil || text != "<b>ONE</b><b> </b><b>TWO</b><b> </b><b>THREE</b>" || len(*h.ThesaurusMemos()) != 1 {
					t.Fatal("complete headline failed to publish one receipt", text, err, textErr)
				}
				return
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			capture, err := tsearch.CaptureConfiguration[core.AuthorizationView](ctx, read, uint32(address.ObjectID))
			if err != nil {
				t.Fatal(err)
			}
			document, err := capture.WalkDocument(h.ActivateRuntime(ctx), h, "ONE TWO THREE")
			if err != nil || document == nil {
				t.Fatal(err)
			}
			if len(*h.ThesaurusMemos()) != 0 {
				t.Fatal("detached rich walk published private receipts")
			}
			before := len(*h.ThesaurusMemos())
			if mode == "cancel_before_accept" {
				cancel()
				err = capture.Accept(ctx, h)
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				enginetest.SessionSQL(t, other, "ALTER TEXT SEARCH CONFIGURATION public.unit_headline_cfg ALTER MAPPING FOR asciiword WITH pg_catalog.simple")
				err = capture.Accept(ctx, h)
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			}
			if len(*h.ThesaurusMemos()) != before {
				t.Fatal("rejected rich walk published receipts")
			}
		})
	}
}

// Observe the real dictionary callback stack, rather than replacing it with an
// echo provider. Metadata and fence Err polls may legitimately hold Engine.Mu.
type headlineDetachedContext struct {
	context.Context
	engine       *core.Engine
	t            *testing.T
	lexicalCalls int
}

func (c *headlineDetachedContext) Err() error {
	var callers [24]uintptr
	n := runtime.Callers(2, callers[:])
	frames := runtime.CallersFrames(callers[:n])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "/functions.") && strings.HasSuffix(frame.Function, ".Lexize") {
			c.lexicalCalls++
			if !c.engine.Mu.TryLock() {
				c.t.Fatal("dictionary callback retained Engine.Mu")
			}
			c.engine.Mu.Unlock()
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}
