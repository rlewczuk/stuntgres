package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

func querySetOpsConsumerWitness(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	for _, s := range []referenceStep{
		referenceSelect("SELECT * FROM (SELECT 1 AS v UNION ALL SELECT 2) d ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
		referenceSelect("SELECT (SELECT 1 UNION SELECT 1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT (SELECT 1 EXCEPT SELECT 1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}}),
		referenceSelect("SELECT EXISTS(SELECT 1 INTERSECT SELECT 2) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}),
		referenceSelect("SELECT EXISTS(SELECT 1 EXCEPT SELECT 1) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}),
		referenceSelect("SELECT EXISTS(SELECT 1 INTERSECT ALL SELECT 1) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
		referenceSelect("SELECT o.v,(SELECT o.v UNION SELECT o.v) AS same,EXISTS(SELECT o.v INTERSECT SELECT 2) AS hit,EXISTS(SELECT o.v EXCEPT SELECT 2) AS miss FROM (VALUES(1),(2)) o(v) ORDER BY o.v", []string{"v", "same", "hit", "miss"}, []string{"INT4", "INT4", "BOOL", "BOOL"}, [][]any{{int64(1), int64(1), false, true}, {int64(2), int64(2), true, false}}),
		referenceSelect("SELECT o.v,(SELECT o.v UNION ALL SELECT o.v+1 ORDER BY 1 DESC LIMIT 1) AS next FROM (VALUES(1),(2)) o(v) ORDER BY o.v", []string{"v", "next"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}}),
	} {
		t.Run(s.query, func(t *testing.T) { querySetOpsRoutes(t, p, b, s) })
	}
	cardinality := referenceImplicitError("SELECT (SELECT 1 UNION ALL SELECT 2)", referenceDiagnostic{Code: "21000", Message: "more than one row returned by a subquery used as an expression"})
	cardinality.errorStage = "execute"
	querySetOpsRoutes(t, p, b, cardinality)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE set_nums(n NUMERIC)", 0),
		referenceExec("INSERT INTO set_nums VALUES(1.1),(1.1),(1.2),(1.2)", 4),
		referenceExec("CREATE TABLE set_dest(n INT)", 0),
		referenceExec("CREATE TABLE set_identity(id INT GENERATED ALWAYS AS IDENTITY,n INT)", 0),
	} {
		referenceRunStep(t, p.ctx, b.db, b.recorder, s)
	}
	for _, prepared := range []bool{false, true} {
		for _, nonContext := range []bool{false, true} {
			s := referenceExec("INSERT INTO set_dest SELECT n FROM set_nums UNION SELECT 1.1", 2)
			s.prepared, s.nonContext = prepared, nonContext
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			querySetOpsRoutes(t, p, b, referenceSelect("SELECT n FROM set_dest ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("DELETE FROM set_dest", 2))
			s = referenceExec("INSERT INTO set_identity(id,n) OVERRIDING USER VALUE SELECT 1,7 UNION SELECT 2,7", 2)
			s.prepared, s.nonContext = prepared, nonContext
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			querySetOpsRoutes(t, p, b, referenceSelect("SELECT n FROM set_identity ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("DELETE FROM set_identity", 2))
		}
	}
}

func TestReferenceQuerySetOpsConsumers(t *testing.T) {
	querySetOpsScenario(t, func(p *referencePair, b referenceBackend) {
		querySetOpsConsumerWitness(t, p, b)
	})
}

func querySetOpsControlSeed(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	querySetOpsSeed(t, p, b)
	for _, s := range []referenceStep{
		referenceExec("CREATE DOMAIN set_copy_domain AS INT", 0),
		referenceExec("CREATE TYPE set_copy_enum AS ENUM('a','b')", 0),
		referenceExec("CREATE VIEW set_copy_bag AS SELECT v FROM sl UNION ALL SELECT v FROM sr", 0),
		referenceExec("CREATE VIEW set_copy_nested(label) AS (SELECT v FROM sl ORDER BY v LIMIT 2) UNION ALL (VALUES(9) EXCEPT SELECT 8)", 0),
		referenceExec("CREATE VIEW set_copy_unknown AS SELECT NULL AS v UNION (SELECT NULL UNION SELECT 1)", 0),
		referenceExec("CREATE VIEW set_copy_domain_view AS SELECT 1::set_copy_domain AS v UNION SELECT 1::set_copy_domain", 0),
		referenceExec("CREATE VIEW set_copy_enum_view AS SELECT 'a'::set_copy_enum AS v INTERSECT ALL SELECT 'a'::set_copy_enum", 0),
		referenceExec("CREATE VIEW set_copy_array AS SELECT '{1,NULL}'::set_copy_domain[] AS v EXCEPT ALL SELECT '{2,NULL}'::set_copy_domain[]", 0),
		referenceExec("ALTER TABLE sl RENAME COLUMN v TO renamed_v", 0),
		referenceExec("ALTER TABLE sl RENAME TO renamed_sl", 0),
	} {
		referenceRunStep(t, p.ctx, b.db, b.recorder, s)
	}
}

func querySetOpsControlReads(pg bool) []referenceStep {
	// Enum public type names are empty in both adapters; native still exports
	// string/owned enum keys. User arrays retain their native descriptor name.
	arrayType := "_SET_COPY_DOMAIN"
	if pg {
		arrayType = ""
	}
	enum := referenceSelect("SELECT v FROM set_copy_enum_view", []string{"v"}, []string{""}, [][]any{{"a"}})
	enum.checkMetadata = enumReferenceMetadata(pg, true)
	enum.checkRaw = func(t *testing.T, column, row int, value any) bool {
		t.Helper()
		if pg {
			raw, ok := value.([]byte)
			if !ok || string(raw) != "a" {
				t.Fatalf("PostgreSQL enum raw %T %v", value, value)
			}
		} else if raw, ok := value.(string); !ok || raw != "a" {
			t.Fatalf("native enum raw %T %v", value, value)
		}
		return true
	}
	array := arrayReferenceSelect("SELECT v FROM set_copy_array", []string{"v"}, []string{arrayType}, []string{"_INT4"}, [][]any{{"{1,NULL}"}}, pg)
	return []referenceStep{
		querySetOpsBag("SELECT v FROM set_copy_bag", "v", "INT4", [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}, {nil}, {nil}, {nil}}),
		querySetOpsBag("SELECT label FROM set_copy_nested", "label", "INT4", [][]any{{int64(1)}, {int64(1)}, {int64(9)}}),
		querySetOpsBag("SELECT v FROM set_copy_unknown", "v", "INT4", [][]any{{nil}, {int64(1)}}),
		querySetOpsBag("SELECT v FROM set_copy_domain_view", "v", "INT4", [][]any{{int64(1)}}),
		enum, array,
	}
}

func querySetOpsNativeControls(t *testing.T, p *referencePair) {
	t.Helper()
	dump := referenceViewDump(t, p)
	destination := newReferenceFixture(t, false, true)
	b := destination.backends()[0]
	for _, s := range []referenceStep{
		referenceExec("CREATE TYPE displaced_enum AS ENUM('x','y')", 0),
		referenceExec("CREATE DOMAIN displaced_domain AS INT", 0),
		referenceExec("CREATE TABLE sentinel(id INT)", 0),
		referenceExec("INSERT INTO sentinel VALUES(17)", 1),
	} {
		referenceRunStep(t, destination.ctx, b.db, b.recorder, s)
	}
	old, err := destination.native.QueryContext(destination.ctx, "SELECT id AS retained FROM sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	checkSentinel := func() {
		querySetOpsRoutes(t, destination, b, referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
		querySetOpsRoutes(t, destination, b, referenceSelect("SELECT count(*) AS n FROM pg_class WHERE relname='set_copy_bag'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
	const suffix = "CREATE VIEW invalid_set AS SELECT /*é*/ 1 UNION SELECT 2,3;\n"
	invalid := append(append([]byte(nil), dump...), suffix...)
	err = stuntgres.Restore(destination.ctx, destination.native, bytes.NewReader(invalid))
	want := controlsReferenceDiagnostic("42601", "each UNION query must have the same number of columns")
	want.Position = referencePosition(utf8.RuneCount(dump) + utf8.RuneCountInString(suffix[:strings.Index(suffix, "2,3")]) + 1)
	referenceAssertDiagnostic(t, err, &want)
	checkSentinel()
	ctx, cancel := context.WithCancel(destination.ctx)
	defer cancel()
	trailing := append(append([]byte(nil), dump...), "CREATE VIEW trailing_set AS SELECT 1 AS v UNION SELECT 2;\n"...)
	// Existing reader cancels only at physical EOF, after the actual stream
	// including the trailing set definition has been consumed.
	if err := stuntgres.Restore(ctx, destination.native, &enumCancelReader{bytes.NewReader(trailing), cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled trailing set restore", err)
	}
	checkSentinel()
	for range 2 {
		referenceViewRestore(t, destination, dump)
		for _, s := range querySetOpsControlReads(false) {
			querySetOpsRoutes(t, destination, b, s)
			held := referenceViewHold(t, destination, b, s.query)
			for range 2 {
				referenceRunStep(t, destination.ctx, held, b.recorder, s)
			}
		}
	}
	stringCoreRetainedRows(t, old, nil, []string{"retained"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	var retained []*sql.Rows
	reads := querySetOpsControlReads(false)
	for _, s := range reads {
		rows, err := destination.native.QueryContext(destination.ctx, s.query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		retained = append(retained, rows)
	}
	// Dropping the source owner before consuming destination keys catches leaked
	// enum/domain/array identities, not merely replay in the original engine.
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	for _, s := range reads {
		querySetOpsRoutes(t, destination, b, s)
	}
	if err := stuntgres.Reset(destination.ctx, destination.native); err != nil {
		t.Fatal(err)
	}
	referenceViewRestore(t, destination, dump)
	for i, s := range reads {
		stringCoreRetainedRows(t, retained[i], nil, s.columns, s.types, s.rows, s.bag)
		querySetOpsRoutes(t, destination, b, s)
	}
	if p.postgres != nil {
		c, err := p.postgres.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		if _, err := c.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ExecContext(p.ctx, string(dump)); err != nil {
			t.Fatal("actual native Dump PostgreSQL replay", err)
		}
		for _, s := range querySetOpsControlReads(true) {
			for _, prepared := range []bool{false, true} {
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, p.postgresRecord, s)
			}
		}
	}
}

func TestReferenceQuerySetOpsViewsControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		querySetOpsControlSeed(t, p, b)
		for _, s := range querySetOpsControlReads(b.pg) {
			querySetOpsRoutes(t, p, b, s)
			held := referenceViewHold(t, p, b, s.query)
			for range 2 {
				referenceRunStep(t, p.ctx, held, b.recorder, s)
			}
		}
		// Replay additional display variants, including width20; ViewContracts
		// asserts the exact nested compact, pretty and width40 displays.
		for i, query := range []string{"SELECT pg_get_viewdef('set_copy_nested') AS definition", "SELECT pg_get_viewdef('set_copy_nested',true) AS definition", "SELECT pg_get_viewdef('set_copy_nested'::regclass,20) AS definition"} {
			name := []string{"set_copy_compact", "set_copy_pretty", "set_copy_width"}[i]
			referenceViewCopySQL(t, p, b, name, query)
			querySetOpsRoutes(t, p, b, querySetOpsBag("SELECT label FROM "+name, "label", "INT4", [][]any{{int64(1)}, {int64(1)}, {int64(9)}}))
		}
	}
	querySetOpsNativeControls(t, p)
}

func TestReferenceQuerySetOpsSourceDependencies(t *testing.T) {
	querySetOpsScenario(t, func(p *referencePair, b referenceBackend) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE set_source(v INT)", 0),
			referenceExec("INSERT INTO set_source VALUES(1),(1),(2)", 3),
			referenceExec("CREATE VIEW set_dependent AS SELECT 9 AS v UNION ALL SELECT v FROM set_source", 0),
		} {
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
		s := querySetOpsBag("SELECT v FROM set_dependent", "v", "INT4", [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(9)}})
		held := referenceViewHold(t, p, b, s.query)
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		for _, step := range []referenceStep{referenceExec("ALTER TABLE set_source RENAME COLUMN v TO renamed_v", 0), referenceExec("ALTER TABLE set_source RENAME TO set_renamed", 0)} {
			referenceRunStep(t, p.ctx, b.db, b.recorder, step)
		}
		querySetOpsRoutes(t, p, b, s)
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		drop := referenceImplicitError("DROP TABLE set_renamed RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop table set_renamed because other objects depend on it", Detail: "view set_dependent depends on table set_renamed", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		drop.errorStage = "execute"
		querySetOpsRoutes(t, p, b, drop)
		querySetOpsRoutes(t, p, b, s)
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("DROP TABLE set_renamed CASCADE", 0))
		missing := referenceImplicitError(s.query, referenceDiagnostic{Code: "42P01", Message: "relation \"set_dependent\" does not exist", Position: "15"})
		querySetOpsRoutes(t, p, b, missing)
	})
}
