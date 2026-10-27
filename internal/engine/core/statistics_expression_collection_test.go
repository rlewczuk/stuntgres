package core_test

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func expressionCollectionFixture(t *testing.T) (*core.Engine, *session.Session, ids.ObjectRef) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE expr_owner(a INT,b INT,c INT)",
		"CREATE STATISTICS expr_a ON (a+1) FROM expr_owner",
		"CREATE STATISTICS expr_b(ndistinct) ON (b+1),c FROM expr_owner",
		"INSERT INTO expr_owner VALUES(1,10,10),(1,10,10),(2,20,20),(NULL,NULL,NULL)",
		"ANALYZE expr_owner",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, _ := vacuumStore(t, e, "expr_owner")
	return e, s, ref
}

func TestStatisticsExpressionPublicationJournal(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK", "COMMIT PREPARED 'expr_journal'", "ROLLBACK PREPARED 'expr_journal'"} {
		t.Run(completion, func(t *testing.T) {
			e, s, ref := expressionCollectionFixture(t)
			peer := enginetest.StartSession(t, e, nil)
			a, b := ndistinctKey(t, e, "expr_a"), ndistinctKey(t, e, "expr_b")
			baseline := analyzeFacts(e, ref.Address)
			enginetest.SessionSQL(t, s, "INSERT INTO expr_owner VALUES(3,30,30)")
			enginetest.SessionSQL(t, s, "BEGIN")
			enginetest.SessionSQL(t, s, "ANALYZE expr_owner(a)")
			first := s.Transactions.Catalog
			selected := first.Statistics[ref.Address].Extended[a]
			if selected == nil || selected.ExpressionLen() != 1 || selected.Expression(0).Rows != 5 || selected.Value.Type() != nil || analyzeFacts(e, ref.Address) != baseline || len(first.Statistics[ref.Address].Extended) != 1 {
				t.Fatal("expression-only selected delta missing or leaked")
			}
			clone := first.Clone()
			if clone.Statistics[ref.Address].Extended[a] != selected {
				t.Fatal("journal clone lost owned backing")
			}
			enginetest.SessionSQL(t, s, "SAVEPOINT expr_mark")
			enginetest.SessionSQL(t, s, "ANALYZE expr_owner(b,c)")
			enginetest.SessionSQL(t, s, "ROLLBACK TO expr_mark")
			if s.Transactions.Catalog.Statistics[ref.Address].Extended[a] != selected || len(s.Transactions.Catalog.Statistics[ref.Address].Extended) != 1 {
				t.Fatal("savepoint altered expression facts")
			}
			enginetest.SessionSQL(t, peer, "ANALYZE expr_owner(b,c)")
			other := analyzeFacts(e, ref.Address).Extended[b]
			otherA := analyzeFacts(e, ref.Address).Extended[a]
			if otherA == baseline.Extended[a] || otherA == selected || otherA.Expression(0).Rows != 5 {
				t.Fatal("expression-only peer refresh did not own independent backing")
			}
			if strings.Contains(completion, "PREPARED") {
				enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'expr_journal'")
				if core.EnginePreparedRef(e).Lookup("expr_journal").Catalog.Statistics[ref.Address].Extended[a] != selected {
					t.Fatal("prepared delta lost fact identity")
				}
				enginetest.SessionSQL(t, peer, completion)
			} else {
				enginetest.SessionSQL(t, s, completion)
			}
			want := otherA
			if strings.HasPrefix(completion, "COMMIT") {
				want = selected
			}
			final := analyzeFacts(e, ref.Address)
			if final.Extended[a] != want || final.Extended[b] != other || baseline.Extended[a].Expression(0).Rows != 4 {
				t.Fatal("completion mutated old or unselected expression backing")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

type expressionCollectionBoundary struct {
	context.Context
	action  func()
	entered bool
}

func (c *expressionCollectionBoundary) Err() error {
	if !c.entered {
		var pcs [48]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, ".(*ProjectedStatisticsCollector).Add") {
				c.entered = true
				c.action()
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}

func TestStatisticsExpressionPublicationCancellation(t *testing.T) {
	e, s, ref := expressionCollectionFixture(t)
	enginetest.SessionSQL(t, s, "INSERT INTO expr_owner VALUES(3,30,30)")
	metadata := e.Storage.RelationMetadata[ref.Address]
	before := metadata.Statistics
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	boundary := &expressionCollectionBoundary{Context: op.Ctx, action: cancel}
	op.Ctx = boundary
	_, err = e.Execute(op, enginetest.Statement(t, "ANALYZE expr_owner"), 0, nil)
	done()
	if !boundary.entered || !errors.Is(err, context.Canceled) {
		t.Fatal("detached scalar cancellation not propagated", boundary.entered, err)
	}
	if e.Storage.RelationMetadata[ref.Address] != metadata || metadata.Statistics != before {
		t.Fatal("canceled expression evaluation partially published")
	}
	enginetest.LockEmpty(t, e)
}

func TestStatisticsExpressionCollectionEvaluatesOnce(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE SEQUENCE expr_seq", "CREATE TABLE expr_once(a INT,b INT)", "INSERT INTO expr_once VALUES(1,1),(1,1),(1,1),(1,1)", "CREATE STATISTICS expr_once_s(ndistinct) ON (nextval('expr_seq')),b FROM expr_once", "ANALYZE expr_once"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, _ := vacuumStore(t, e, "expr_once")
	fact := analyzeFacts(e, ref.Address).Extended[ndistinctKey(t, e, "expr_once_s")]
	if fact == nil || fact.Items[0].Value != 4 || fact.Expression(0).DistinctCount != 4 || len(fact.Expression(0).Histogram) != 4 {
		t.Fatal("volatile result not shared by scalar/joint collectors", fact)
	}
	for i, value := range fact.Expression(0).Histogram {
		n, err := value.Integer()
		if err != nil || n != int64(i+1) {
			t.Fatal("expression evaluated more than once per row", i, n, err)
		}
	}
	// A second scan consumes the next four values, not an independently rebound or
	// precomputed volatile definition and not a second joint evaluation stream.
	enginetest.SessionSQL(t, s, "ANALYZE expr_once")
	next := analyzeFacts(e, ref.Address).Extended[ndistinctKey(t, e, "expr_once_s")]
	for i, value := range next.Expression(0).Histogram {
		n, err := value.Integer()
		if err != nil || n != int64(i+5) {
			t.Fatal("CREATE or joint collection consumed sequence values", i, n, err)
		}
	}
}

func TestStatisticsExpressionSnapshotTypedValues(t *testing.T) {
	source, s, ref := expressionCollectionFixture(t)
	enginetest.SessionSQL(t, s, "CREATE STATISTICS expr_object ON ('expr_owner'::regclass) FROM expr_owner")
	enginetest.SessionSQL(t, s, "CREATE STATISTICS expr_text ON (a::text) FROM expr_owner")
	enginetest.SessionSQL(t, s, "CREATE DOMAIN expr_domain AS INT CHECK(VALUE > 0)")
	enginetest.SessionSQL(t, s, "CREATE STATISTICS expr_domain_array ON (ARRAY[NULL::expr_domain,1::expr_domain]) FROM expr_owner")
	enginetest.SessionSQL(t, s, "ANALYZE expr_owner")
	old := analyzeFacts(source, ref.Address).Extended[ndistinctKey(t, source, "expr_a")]
	enginetest.SessionSQL(t, s, "INSERT INTO expr_owner VALUES(99,99,99)")
	enginetest.SessionSQL(t, s, "ALTER TABLE expr_owner RENAME a TO renamed")
	var dump strings.Builder
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	targetSession := enginetest.StartSession(t, target, nil)
	for _, q := range []string{"CREATE TABLE collision(a INT)", "CREATE TEMP TABLE expr_temp(a INT)", "INSERT INTO expr_temp VALUES(3),(3)", "CREATE STATISTICS pg_temp.expr_temp_s ON (a+1) FROM expr_temp", "CREATE STATISTICS pg_temp.expr_temp_text ON (a::text) FROM expr_temp", "ANALYZE expr_temp"} {
		enginetest.SessionSQL(t, targetSession, q)
	}
	beforeTempKey := ndistinctKey(t, target, "expr_temp_s")
	var beforeTemp *storage.ExtendedStatistics
	for _, entry := range target.Storage.RelationMetadata {
		if entry.Statistics != nil && entry.Statistics.Extended[beforeTempKey] != nil {
			beforeTemp = entry.Statistics.Extended[beforeTempKey]
		}
	}
	if beforeTemp == nil {
		t.Fatal("missing genuine TEMP baseline")
	}
	beforeTempRecipes := beforeTemp.Recipes
	if err := engine.Restore(context.Background(), target, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	importedRef, _ := vacuumStore(t, target, "expr_owner")
	fact := analyzeFacts(target, importedRef.Address).Extended[ndistinctKey(t, target, "expr_a")]
	if fact == nil || fact == old || fact.Expression(0).Rows != 4 || fact.Expression(0).NullCount != 1 || fact.Recipes == old.Recipes || fact.Expression(0).Usage.Type == old.Expression(0).Usage.Type {
		t.Fatal("stale facts or destination typed recipe not reminted", fact)
	}
	object := analyzeFacts(target, importedRef.Address).Extended[ndistinctKey(t, target, "expr_object")]
	oid, err := object.Expression(0).MCV[0].Value.Text()
	if err != nil || oid != strconv.FormatUint(uint64(importedRef.Address.ObjectID), 10) {
		t.Fatal("source OID adopted as destination object authority", oid, importedRef, err)
	}
	text := analyzeFacts(target, importedRef.Address).Extended[ndistinctKey(t, target, "expr_text")]
	value, err := text.Expression(0).MCV[0].Value.Text()
	if err != nil || value != "1" {
		t.Fatal("typed text summary lost", value, err)
	}
	domainArray := analyzeFacts(target, importedRef.Address).Extended[ndistinctKey(t, target, "expr_domain_array")]
	array := domainArray.Expression(0).MCV[0].Value
	if array.ArrayLen() != 2 || !array.ArrayElement(0).IsNull() || !array.ArrayElement(0).Type().IsDomain() || array.ArrayElement(0).Type() != array.Type().Element() {
		t.Fatal("nested captured domain-NULL construction authority lost", array)
	}
	for _, name := range []string{"expr_temp_s", "expr_temp_text"} {
		temp := ndistinctKey(t, target, name)
		var retained *storage.ExtendedStatistics
		for _, entry := range target.Storage.RelationMetadata {
			if entry.Statistics != nil && entry.Statistics.Extended[temp] != nil {
				retained = entry.Statistics.Extended[temp]
			}
		}
		if retained == nil {
			t.Fatal("genuine destination TEMP expression fact lost", name)
		}
		if retained.Expression(0).Rows != 2 || retained.Expression(0).MCV[0].Count != 2 {
			t.Fatal("retained TEMP summary lost", name)
		}
		read := target.CatalogRead()
		definition, ok := read.Statistics(temp.Definition.Address)
		if !ok {
			t.Fatal("retained TEMP definition lost", name)
		}
		if err := (catalog.StatisticsProvider{}).Validate(read, definition); err != nil {
			t.Fatal("invalid retained TEMP descriptor", name, err)
		}
		if retained.Recipes != definition.Expressions {
			t.Fatal("fact adopted a different retained recipe", name)
		}
		recipe := definition.Expressions.At(0)
		for i := range recipe.InputLen() {
			if recipe.Input(i).Epoch != read.Epoch() || recipe.Input(i).Domain() != read.Domain() {
				t.Fatal("TEMP input lifetime not advanced", name)
			}
		}
		for i := range recipe.DependencyLen() {
			if recipe.Dependency(i).Epoch != read.Epoch() || recipe.Dependency(i).Domain() != read.Domain() {
				t.Fatal("TEMP provider lifetime not advanced", name)
			}
		}
		if name == "expr_temp_text" {
			textType, ok := read.LookupType(sqltype.Text)
			value, err := retained.Expression(0).MCV[0].Value.Text()
			if !ok || retained.Expression(0).Usage.Type != textType || err != nil || value != "3" {
				t.Fatal("TEMP noninput result codec authority lost", value, err)
			}
		} else if retained == beforeTemp || retained.Recipes == beforeTempRecipes {
			t.Fatal("TEMP fact was not reowned into the replacement")
		}
	}
	if beforeTemp.Recipes != beforeTempRecipes || beforeTemp.Expression(0).Rows != 2 {
		t.Fatal("TEMP reownership mutated the old captured fact")
	}
	enginetest.SessionSQL(t, targetSession, "ANALYZE expr_owner")
	fresh := analyzeFacts(target, importedRef.Address).Extended[ndistinctKey(t, target, "expr_a")]
	if fresh.Expression(0).Rows != 5 || fact.Expression(0).Rows != 4 {
		t.Fatal("refresh changed captured stale backing")
	}
}

func TestStatisticsExpressionPublicationCompatibility(t *testing.T) {
	e, s, ref := expressionCollectionFixture(t)
	key := ndistinctKey(t, e, "expr_a")
	baseline := analyzeFacts(e, ref.Address)
	fact := baseline.Extended[key]
	enginetest.SessionSQL(t, s, "ALTER TABLE expr_owner RENAME a TO renamed")
	enginetest.SessionSQL(t, s, "ALTER STATISTICS expr_a SET STATISTICS 0")
	enginetest.SessionSQL(t, s, "INSERT INTO expr_owner VALUES(8,8,8)")
	if got := analyzeFacts(e, ref.Address).Extended[key]; got != fact {
		t.Fatal("rename/target/DML lost retained stale backing")
	}
	current := e.CatalogRead()
	next := catalog.CloneState(e.Catalog.State)
	typ, ok := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})
	if !ok {
		t.Fatal("missing authentic input/result provider")
	}
	replacement := *typ
	replacement.ObjectRevision = typ.Revision() + 1
	next.SetDescriptor(replacement.Address(), &replacement)
	filtered := storage.CompatibleExtendedStatistics(baseline, next.MustRelation(ref.Address), e.Catalog.Read(next))
	if filtered != nil && filtered.Extended[key] != nil {
		t.Fatal("provider replacement retained stale expression fact")
	}
	if baseline.Extended[key] != fact || fact.Expression(0).Rows != 4 {
		t.Fatal("compatibility filtering mutated captured facts")
	}
	if restored := storage.CompatibleExtendedStatistics(baseline, current.MustRelation(ref.Address), current); restored.Extended[key] != fact {
		t.Fatal("filtering poisoned original provider authority")
	}
}

func TestStatisticsExpressionSnapshotCapturedGeneration(t *testing.T) {
	e, _, ref := expressionCollectionFixture(t)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	published := e.Catalog.State
	next := catalog.CloneState(published)
	next.SetEpoch(published.Epoch() + 1)
	read := e.Catalog.Read(next)
	codec := core.StatisticsSnapshotCodec(context.Background(), e, read)
	smallint, ok := read.LookupType(sqltype.Int2)
	if !ok {
		t.Fatal("missing authentic smallint constructor")
	}
	seven, err := sqltype.Integer(smallint, 7)
	if err != nil {
		t.Fatal(err)
	}
	regclass, ok := read.LookupType(sqltype.Regclass)
	if !ok {
		t.Fatal("missing authentic object-identity constructor")
	}
	object := sqltype.String(regclass, strconv.FormatUint(uint64(ref.Address.ObjectID), 10))
	for _, value := range []sqltype.Value{seven, object} {
		encoded, err := codec.Encode(value.Type().Usage(), value)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := codec.Decode(value.Type().Usage(), encoded)
		if err != nil {
			t.Fatalf("captured destination generation constructor %s: %v", encoded, err)
		}
		if restored.Type() != value.Type() {
			t.Fatal("constructor adopted a different type generation")
		}
		if value.Type() == smallint {
			n, err := restored.Integer()
			if err != nil || n != 7 {
				t.Fatal(n, err)
			}
		} else {
			text, err := restored.Text()
			if err != nil || text != strconv.FormatUint(uint64(ref.Address.ObjectID), 10) {
				t.Fatal("object constructor used live-publication admission", text, err)
			}
		}
		if e.Catalog.State != published {
			t.Fatal("constructor installed its private destination catalog")
		}
	}
}
