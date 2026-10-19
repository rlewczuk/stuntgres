package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Utility Prepare is analysis-only; both routes execute through a real scope.
func sequenceCreateRestartCommand(t *testing.T, e *Engine, s *Session, query string, prepared bool) ([]sqlstate.Error, error) {
	t.Helper()
	ast := statement(t, query)
	var expected *StatementDescription
	if prepared {
		var description StatementDescription
		ast, description = sequencePreparedDescription(t, e, s, query)
		expected = &description
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
	scope.Expected = expected
	r, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	if r.Rows != nil {
		if closeErr := r.Rows.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Fatal("sequence utility returned rows")
	}
	if r.Count != 0 {
		t.Fatal("sequence utility affected rows", r.Count)
	}
	return op.DetachNotices(), err
}

func sequenceCreateRestartRow(t *testing.T, e *Engine, s *Session, name string, last int64, called bool) {
	t.Helper()
	r := sessionSQL(t, s, "SELECT * FROM "+name)
	defer r.Rows.Close()
	wantSchema := []catalog.Column{
		{Name: "last_value", Usage: testUsage(e, sqltype.Int8), NullabilityKnown: true},
		{Name: "log_cnt", Usage: testUsage(e, sqltype.Int8), NullabilityKnown: true},
		{Name: "is_called", Usage: testUsage(e, sqltype.Bool), NullabilityKnown: true},
	}
	if !reflect.DeepEqual(r.Rows.Schema(), wantSchema) {
		t.Fatal("sequence row metadata", r.Rows.Schema(), wantSchema)
	}
	if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{last, int64(0), called}}) {
		t.Fatal("sequence row", got, last, called)
	}
}

func sequenceCreateRestartDiagnostic(t *testing.T, err error, code, message, position string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
		t.Fatalf("diagnostic=%#v error=%v want=%#v", got, err, want)
	}
}

func TestSequenceCreateRestart(t *testing.T) {
	cases := []struct {
		name, options                       string
		typ                                 sqltype.ID
		increment, min, max, start, initial int64
	}{
		{"Bare", "START 5 RESTART", sqltype.Int8, 1, 1, math.MaxInt64, 5, 5},
		{"With", "START WITH 5 RESTART WITH 9", sqltype.Int8, 1, 1, math.MaxInt64, 5, 9},
		{"WithoutWith", "RESTART 9 START 5", sqltype.Int8, 1, 1, math.MaxInt64, 5, 9},
		{"SignedAscending", "INCREMENT BY +2 MINVALUE -20 MAXVALUE 20 START +5 RESTART +9", sqltype.Int8, 2, -20, 20, 5, 9},
		{"SignedDescending", "INCREMENT -2 START -5 RESTART WITH -9", sqltype.Int8, -2, math.MinInt64, -1, -5, -9},
		{"SmallintMax", "AS SMALLINT START 5 RESTART 32767", sqltype.Int2, 1, 1, math.MaxInt16, 5, math.MaxInt16},
		{"SmallintMin", "AS SMALLINT INCREMENT -1 START -5 RESTART -32768", sqltype.Int2, -1, math.MinInt16, -1, -5, math.MinInt16},
		{"BareBeforeStart", "RESTART START 5", sqltype.Int8, 1, 1, math.MaxInt64, 5, 5},
		{"IntegerMax", "AS INTEGER START 5 RESTART 2147483647", sqltype.Int4, 1, 1, math.MaxInt32, 5, math.MaxInt32},
		{"IntegerMin", "AS INTEGER INCREMENT -1 START -5 RESTART -2147483648", sqltype.Int4, -1, math.MinInt32, -1, -5, math.MinInt32},
		{"BigintMax", "AS BIGINT START 5 RESTART 9223372036854775807", sqltype.Int8, 1, 1, math.MaxInt64, 5, math.MaxInt64},
		{"BigintMin", "AS BIGINT INCREMENT -1 START -5 RESTART -9223372036854775808", sqltype.Int8, -1, math.MinInt64, -1, -5, math.MinInt64},
	}
	for _, prepared := range []bool{false, true} {
		mode := map[bool]string{false: "Direct", true: "Prepared"}[prepared]
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				notices, err := sequenceCreateRestartCommand(t, e, s, "CREATE SEQUENCE s "+tc.options, prepared)
				if err != nil || len(notices) != 0 {
					t.Fatal("CREATE", err, notices)
				}
				ref := catalogRef(t, e, e.CatalogRead(), "s")
				d := e.catalog.state.MustRelation(ref.Address)
				options := d.Sequence
				want := catalog.SequenceOptions{SQLType: tc.typ, Increment: tc.increment, Min: tc.min, Max: tc.max, Start: tc.start, Cache: 1, Persistence: "logged"}
				if !reflect.DeepEqual(*options, want) || e.storage.Sequences[ref.Address].Last != tc.initial || e.storage.Sequences[ref.Address].Called || e.storage.Sequences[ref.Address].Generation == 0 {
					t.Fatal("configured START and initial uncalled state", options, e.storage.Sequences[ref.Address], want)
				}
				sequenceCreateRestartRow(t, e, s, "s", tc.initial, false)
				_, err = sequencePreparedExecute(e, s, statement(t, "SELECT currval('s')"), nil)
				sequenceCreateRestartDiagnostic(t, err, "55000", `currval of sequence "s" is not yet defined in this session`, "")
				sequenceScalar(t, s, "SELECT nextval('s')", tc.initial)
				sequenceScalar(t, s, "SELECT currval('s')", tc.initial)
				sequenceScalar(t, s, "SELECT lastval()", tc.initial)
				sequenceCreateRestartRow(t, e, s, "s", tc.initial, true)
				if d.Sequence != options || !reflect.DeepEqual(*options, want) {
					t.Fatal("allocation mutated immutable configured options")
				}
				if tc.initial == tc.min || tc.initial == tc.max {
					_, err = sequencePreparedExecute(e, s, statement(t, "SELECT nextval('s')"), nil)
					which, bound := "maximum", tc.max
					if tc.increment < 0 {
						which, bound = "minimum", tc.min
					}
					sequenceCreateRestartDiagnostic(t, err, "2200H", fmt.Sprintf(`nextval: reached %s value of sequence "s" (%d)`, which, bound), "")
					sequenceCreateRestartRow(t, e, s, "s", tc.initial, true)
				}
				if _, err = sequenceCreateRestartCommand(t, e, s, "ALTER SEQUENCE s RESTART", prepared); err != nil {
					t.Fatal(err)
				}
				sequenceCreateRestartRow(t, e, s, "s", tc.start, false)
				sequenceScalar(t, s, "SELECT nextval('s')", tc.start)
				if !reflect.DeepEqual(*options, want) || !reflect.DeepEqual(*e.catalog.state.MustRelation(ref.Address).Sequence, want) {
					t.Fatal("bare ALTER adopted initial restart as configured START")
				}
			})
		}
	}
}

func TestSequenceCreateRestartCache(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, persistence := range []struct {
			clause, name, catalog, restart string
			initial                        int64
		}{
			{"", "LoggedWith", "logged", "RESTART WITH 9", 9},
			{"TEMP ", "TemporaryWith", "temporary", "RESTART WITH 9", 9},
			{"UNLOGGED ", "UnloggedWith", "unlogged", "RESTART WITH 9", 9},
			{"", "LoggedWithoutWith", "logged", "RESTART 9", 9},
			{"TEMP ", "TemporaryWithoutWith", "temporary", "RESTART 9", 9},
			{"UNLOGGED ", "UnloggedWithoutWith", "unlogged", "RESTART 9", 9},
			{"", "LoggedBare", "logged", "RESTART", 5},
			{"TEMP ", "TemporaryBare", "temporary", "RESTART", 5},
			{"UNLOGGED ", "UnloggedBare", "unlogged", "RESTART", 5},
		} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+persistence.name, func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				notices, err := sequenceCreateRestartCommand(t, e, a, "CREATE "+persistence.clause+"SEQUENCE s START 5 "+persistence.restart+" CACHE 3", prepared)
				if err != nil || len(notices) != 0 {
					t.Fatal(err, notices)
				}
				sequenceCreateRestartRow(t, e, a, "s", persistence.initial, false)
				ast, held := sequencePreparedDescription(t, e, a, "SELECT nextval('s')")
				r, err := sequencePreparedExecute(e, a, ast, &held)
				if err != nil {
					t.Fatal(err)
				}
				got := tuples(r)
				r.Rows.Close()
				if !reflect.DeepEqual(got, [][]driver.Value{{persistence.initial}}) {
					t.Fatal("first prepared demand", got)
				}
				ref := held.sequences.constants[0].ref
				options := e.catalog.state.MustRelation(ref.Address).Sequence
				want := catalog.SequenceOptions{SQLType: sqltype.Int8, Increment: 1, Min: 1, Max: math.MaxInt64, Start: 5, Cache: 3, Persistence: persistence.catalog}
				if !reflect.DeepEqual(*options, want) {
					t.Fatal("persistence/configured options", options, want)
				}
				sequenceCreateRestartRow(t, e, a, "s", persistence.initial+2, true)
				if persistence.catalog != "temporary" {
					sequenceScalar(t, b, "SELECT nextval('s')", persistence.initial+3)
					sequenceCreateRestartRow(t, e, a, "s", persistence.initial+5, true)
					sequenceScalar(t, b, "SELECT currval('s')", persistence.initial+3)
				}
				sequenceScalar(t, a, "SELECT nextval('s')", persistence.initial+1)
				sequenceScalar(t, a, "SELECT currval('s')", persistence.initial+1)
				sequenceScalar(t, a, "SELECT lastval()", persistence.initial+1)
				if _, err = sequenceCreateRestartCommand(t, e, a, "ALTER SEQUENCE s RESTART", prepared); err != nil {
					t.Fatal(err)
				}
				sequenceCreateRestartRow(t, e, a, "s", 5, false)
				sequenceScalar(t, a, "SELECT nextval('s')", 5)
				sequenceCreateRestartRow(t, e, a, "s", 7, true)
				if !reflect.DeepEqual(*options, want) {
					t.Fatal("restart mutated retained options")
				}
			})
		}
	}
}

func TestSequenceCreateRestartFailureAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, prefix := range []string{"CREATE SEQUENCE ", "CREATE SEQUENCE IF NOT EXISTS ", "CREATE TEMP SEQUENCE ", "CREATE TEMP SEQUENCE IF NOT EXISTS "} {
			for _, tc := range []struct{ options, code, message string }{
				{"MINVALUE 10 MAXVALUE 20 START 15 RESTART 9", "22023", "RESTART value (9) cannot be less than MINVALUE (10)"},
				{"MINVALUE 10 MAXVALUE 20 START 15 RESTART 21", "22023", "RESTART value (21) cannot be greater than MAXVALUE (20)"},
				{"AS SMALLINT START 5 RESTART 32768", "22023", "RESTART value (32768) cannot be greater than MAXVALUE (32767)"},
				{"AS INTEGER START 5 RESTART 2147483648", "22023", "RESTART value (2147483648) cannot be greater than MAXVALUE (2147483647)"},
				{"START 5 RESTART 9 RESTART WITH 10", "42601", "conflicting or redundant options"},
			} {
				query := prefix + "failed " + tc.options
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+query, func(t *testing.T) {
					e := New()
					s := sessionStart(t, e, nil)
					sessionSQL(t, s, "CREATE TABLE sentinel(v INT)")
					sessionSQL(t, s, "INSERT INTO sentinel VALUES(7)")
					sessionSQL(t, s, "CREATE SEQUENCE kept START 31 CACHE 4")
					sequenceScalar(t, s, "SELECT nextval('kept')", 31)
					table := catalogRef(t, e, e.CatalogRead(), "sentinel")
					before := captureSequenceDeletePublication(e, s, table.Address)
					notices, err := sequenceCreateRestartCommand(t, e, s, query, prepared)
					position := ""
					if tc.code == "42601" {
						position = strconv.Itoa(strings.LastIndex(query, "RESTART") + 1)
					}
					sequenceCreateRestartDiagnostic(t, err, tc.code, tc.message, position)
					if len(notices) != 0 {
						t.Fatal("failed CREATE emitted notice", notices)
					}
					before.assertUnchanged(t, e, s)
					if s.transactions.catalog != nil || s.transactions.writes != nil || s.transactions.deletes != nil || s.transactions.state != TxIdle {
						t.Fatal("failed implicit CREATE retained private publication")
					}
					_, err = sequencePreparedExecute(e, s, statement(t, "SELECT * FROM failed"), nil)
					sequenceCreateRestartDiagnostic(t, err, "42P01", `relation "failed" does not exist`, "15")
					sequenceScalar(t, s, "SELECT currval('kept')", 31)
					sequenceScalar(t, s, "SELECT lastval()", 31)
					sequenceScalar(t, s, "SELECT nextval('kept')", 32)
				})
			}
		}
	}
}

func TestSequenceCreateRestartIfNotExists(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE sentinel(v INT)")
			sessionSQL(t, s, "INSERT INTO sentinel VALUES(7)")
			sessionSQL(t, s, "CREATE SEQUENCE s START 31 CACHE 4")
			sequenceScalar(t, s, "SELECT nextval('s')", 31)
			table := catalogRef(t, e, e.CatalogRead(), "sentinel")
			for _, query := range []string{
				"CREATE SEQUENCE IF NOT EXISTS s START 5 RESTART 0",
				"CREATE SEQUENCE IF NOT EXISTS s START 5 RESTART 9 RESTART 10",
				"CREATE SEQUENCE IF NOT EXISTS sentinel START 5 RESTART 0",
			} {
				before := captureSequenceDeletePublication(e, s, table.Address)
				notices, err := sequenceCreateRestartCommand(t, e, s, query, prepared)
				name := "s"
				if strings.Contains(query, "sentinel") {
					name = "sentinel"
				}
				want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: fmt.Sprintf(`relation "%s" already exists, skipping`, name)}}
				if err != nil || !reflect.DeepEqual(notices, want) {
					t.Fatal("existing target must skip option validation", err, notices, want)
				}
				before.assertUnchanged(t, e, s)
			}
			for _, options := range []string{"START 5 RESTART", "START 5 RESTART WITH 9", "RESTART 9 START 5"} {
				notices, err := sequenceCreateRestartCommand(t, e, s, "CREATE SEQUENCE IF NOT EXISTS fresh "+options, prepared)
				if err != nil || len(notices) != 0 {
					t.Fatal("absent target must create", err, notices)
				}
				initial := int64(9)
				if options == "START 5 RESTART" {
					initial = 5
				}
				sequenceCreateRestartRow(t, e, s, "fresh", initial, false)
				sequenceScalar(t, s, "SELECT nextval('fresh')", initial)
				if _, err = sequenceCreateRestartCommand(t, e, s, "ALTER SEQUENCE fresh RESTART", prepared); err != nil {
					t.Fatal(err)
				}
				sequenceScalar(t, s, "SELECT nextval('fresh')", 5)
				sessionSQL(t, s, "DROP SEQUENCE fresh")
			}
			sequenceScalar(t, s, "SELECT nextval('s')", 32)
		})
	}
}

func TestSequenceCreateRestartTransactionalPublication(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Rollback", true: "/Commit"}[commit], func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE sentinel(v INT)")
				sessionSQL(t, a, "INSERT INTO sentinel VALUES(7)")
				table := catalogRef(t, e, e.CatalogRead(), "sentinel")
				before := captureSequenceDeletePublication(e, a, table.Address)
				tx := transactionBegin(t, a)
				if _, err := sequenceCreateRestartCommand(t, e, a, "CREATE SEQUENCE s START 5 RESTART 9", prepared); err != nil {
					t.Fatal(err)
				}
				sequenceCreateRestartRow(t, e, a, "s", 9, false)
				_, err := sequencePreparedExecute(e, b, statement(t, "SELECT * FROM s"), nil)
				sequenceCreateRestartDiagnostic(t, err, "42P01", `relation "s" does not exist`, "15")
				transactionEnd(t, a, tx, commit)
				if commit {
					sequenceCreateRestartRow(t, e, b, "s", 9, false)
					sequenceScalar(t, b, "SELECT nextval('s')", 9)
					sessionSQL(t, b, "ALTER SEQUENCE s RESTART")
					sequenceScalar(t, b, "SELECT nextval('s')", 5)
				} else {
					before.assertUnchanged(t, e, a)
					_, err = sequencePreparedExecute(e, b, statement(t, "SELECT * FROM s"), nil)
					sequenceCreateRestartDiagnostic(t, err, "42P01", `relation "s" does not exist`, "15")
				}
				if a.transactions.catalog != nil || a.transactions.state != TxIdle {
					t.Fatal("transaction end retained private CREATE")
				}
			})
		}
	}
}

func TestSequenceCreateRestartConstructorFailure(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, failure := range []string{"OID", "Revision", "Namespace", "Cancellation"} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+failure, func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				sessionSQL(t, s, "CREATE TABLE sentinel(v INT)")
				sessionSQL(t, s, "INSERT INTO sentinel VALUES(7)")
				sessionSQL(t, s, "CREATE SEQUENCE kept START 31 CACHE 4")
				sequenceScalar(t, s, "SELECT nextval('kept')", 31)
				table := catalogRef(t, e, e.CatalogRead(), "sentinel")
				query := "CREATE SEQUENCE failed START 5 RESTART 9"
				want := "stuntgres: catalog object identity exhausted"
				switch failure {
				case "OID":
					e.catalog.nextOID = 0
				case "Revision":
					e.catalog.nextRevision = ids.DefinitionRevision(^uint64(0))
					want = "stuntgres: catalog definition revision exhausted"
				case "Namespace":
					state := catalog.CloneState(e.catalog.state)
					state.SetNamespaceGeneration(^uint64(0))
					e.catalog.state = state
					query = "CREATE TEMP SEQUENCE failed START 5 RESTART 9"
					want = "stuntgres: namespace invalidation generation exhausted"
				}
				before := captureSequenceDeletePublication(e, s, table.Address)
				var err error
				if failure == "Cancellation" {
					ast := statement(t, query)
					var expected *StatementDescription
					if prepared {
						var description StatementDescription
						ast, description = sequencePreparedDescription(t, e, s, query)
						expected = &description
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					op, acquireErr := s.Acquire(ctx)
					if acquireErr != nil {
						t.Fatal(acquireErr)
					}
					defer op.Done()
					scope, startErr := e.StartStatement(op)
					if startErr != nil {
						t.Fatal(startErr)
					}
					scope.Expected = expected
					_, err = scope.Execute(ast, 0, nil)
					if err != nil {
						scope.Finish(err)
						t.Fatal("constructor must stage before cancellation", err)
					}
					// Cancel a real context after successful staging, before publication.
					cancel()
					err = scope.Finish(nil)
					if !errors.Is(err, context.Canceled) {
						t.Fatal("canceled CREATE was published", err)
					}
					if len(op.DetachNotices()) != 0 {
						t.Fatal("canceled CREATE emitted notices")
					}
					op.Done()
				} else {
					var notices []sqlstate.Error
					notices, err = sequenceCreateRestartCommand(t, e, s, query, prepared)
					if err == nil || err.Error() != want || len(notices) != 0 {
						t.Fatal("exhausted constructor", err, notices, want)
					}
					var diagnostic *sqlstate.Error
					if errors.As(err, &diagnostic) {
						t.Fatal("native capacity error gained a SQL diagnostic", diagnostic)
					}
				}
				before.assertUnchanged(t, e, s)
				if s.transactions.catalog != nil || s.transactions.writes != nil || s.transactions.deletes != nil || s.transactions.state != TxIdle {
					t.Fatal("constructor failure retained a private batch")
				}
				sequenceScalar(t, s, "SELECT nextval('kept')", 32)
				lockEmpty(t, e)
			})
		}
	}
}
