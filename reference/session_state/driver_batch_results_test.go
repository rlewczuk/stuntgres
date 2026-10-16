package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDriverBatchEffectsCounts(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchExec(t, p, b, a, "CREATE TABLE batch_t(i INT NOT NULL); INSERT INTO batch_t VALUES(1),(2); SELECT i FROM batch_t ORDER BY i", 2)
		batchValues(t, p, b, peer, "SELECT i FROM batch_t ORDER BY i", 1, 2)
		fixture.BatchSets(t, batchQuery(t, p, a, "INSERT INTO batch_t VALUES(3); SELECT i FROM batch_t ORDER BY i; INSERT INTO batch_t VALUES(4)"), fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, false})
		batchValues(t, p, b, peer, "SELECT i FROM batch_t ORDER BY i", 1, 2, 3, 4)
		batchExec(t, p, b, a, "INSERT INTO batch_t VALUES(5); SELECT i FROM batch_t; INSERT INTO batch_t VALUES(6)", 1)
		fixture.BatchSets(t, batchQuery(t, p, a, "UPDATE batch_t SET i=i; SET application_name='batch'"), fixture.BatchSet{[]string{}, []string{}, nil, false})
		// database/sql's legacy routes preserve the parameterless direct contract.
		r, err := b.DB.Exec("INSERT INTO batch_t VALUES(8); INSERT INTO batch_t VALUES(9)")
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
		rows, err := b.DB.Query("SELECT i FROM batch_t WHERE i=8; SELECT i FROM batch_t WHERE i=9")
		if err != nil {
			t.Fatal(err)
		}
		fixture.BatchSets(t, rows, fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(8)}}, false}, fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}, false})
	})
}
func TestReferenceDriverBatchResultSets(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchExec(t, p, b, a, "CREATE TABLE batch_t(i INT,t TEXT,f BOOL)", 0)
		batchExec(t, p, b, a, "INSERT INTO batch_t VALUES(1,'a',TRUE),(1,'a',TRUE),(2,NULL,FALSE)", 3)
		q := "SET application_name='lead'; SELECT i FROM batch_t WHERE i=99; SET application_name='middle'; SELECT t,f FROM batch_t ORDER BY i; SELECT FROM batch_t; SET application_name='tail'"
		fixture.BatchSets(t, batchQuery(t, p, a, q), fixture.BatchSet{[]string{"i"}, []string{"INT4"}, nil, false}, fixture.BatchSet{[]string{"t", "f"}, []string{"TEXT", "BOOL"}, [][]any{{"a", true}, {"a", true}, {nil, false}}, false}, fixture.BatchSet{[]string{}, []string{}, [][]any{{}, {}, {}}, false})
		fixture.BatchSets(t, batchQuery(t, p, a, "SELECT i FROM batch_t; SELECT t FROM batch_t"), fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}, true}, fixture.BatchSet{[]string{"t"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}, {nil}}, true})
		r := batchQuery(t, p, a, "SELECT i FROM batch_t; SELECT t FROM batch_t")
		if b.PG {
			if r.NextResultSet() {
				t.Fatal("pq unexpectedly discovered unread next set")
			}
		} else {
			if !r.NextResultSet() {
				t.Fatal("native must skip unread set", r.Err())
			}
			fixture.BatchReadSet(t, r, fixture.BatchSet{[]string{"t"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}, {nil}}, true})
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		batchValues(t, p, b, peer, "SELECT i FROM batch_t ORDER BY i", 1, 1, 2)
		fixture.BatchSets(t, batchQuery(t, p, a, "SET bytea_output='hex'; SELECT '\\x6162'::bytea AS v; SET bytea_output='escape'; SELECT '\\x6162'::bytea AS v"), fixture.BatchSet{[]string{"v"}, []string{"BYTEA"}, [][]any{{"ab"}}, false}, fixture.BatchSet{[]string{"v"}, []string{"BYTEA"}, [][]any{{"ab"}}, false})
	})
}
func TestReferenceDriverBatchFeatureSmoke(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchExec(t, p, b, a, "CREATE TABLE batch_t(i INT NOT NULL,t TEXT); INSERT INTO batch_t VALUES(1,'a'),(2,'b')", 2)
		fixture.BatchSets(t, batchQuery(t, p, a, "SELECT i FROM batch_t ORDER BY i; SELECT t FROM batch_t ORDER BY i"), fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, false}, fixture.BatchSet{[]string{"t"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}, false})
		stmt, err := a.PrepareContext(p.Ctx, "SELECT i FROM batch_t; SELECT t FROM batch_t")
		if stmt != nil {
			stmt.Close()
			t.Fatal("multi-command handle")
		}
		w := batchDiag("42601", "cannot insert multiple commands into a prepared statement")
		fixture.AssertDiagnostic(t, err, &w)
		q := "INSERT INTO batch_t VALUES(3,'c'); SELECT missing FROM batch_t"
		w = batchDiag("42703", `column "missing" does not exist`)
		w.Position = batchPosition(q, "missing")
		batchError(t, p, a, q, w)
		batchExec(t, p, b, a, "INSERT INTO batch_t VALUES(4,'d'); BEGIN; INSERT INTO batch_t VALUES(5,'e')", 1)
		batchValues(t, p, b, peer, "SELECT i FROM batch_t ORDER BY i", 1, 2)
		batchExec(t, p, b, a, "ROLLBACK", 0)
		batchValues(t, p, b, a, "SELECT i FROM batch_t ORDER BY i", 1, 2)
	})
}

func TestReferenceDriverBatchResultSetMetadata(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchExec(t, p, b, a, "CREATE TABLE batch_metadata(i INT NOT NULL,v VARCHAR(7),n NUMERIC(8,3),c CHAR(3),f BOOL,t TEXT)", 0)
		batchExec(t, p, b, a, "INSERT INTO batch_metadata VALUES(1,'seven',12.345,'xy',TRUE,'text')", 1)
		batchExec(t, p, b, a, "CREATE TABLE batch_metadata_alt(v VARCHAR(3) NOT NULL,n NUMERIC(5,1) NOT NULL)", 0)
		batchExec(t, p, b, a, "INSERT INTO batch_metadata_alt VALUES('new',23.4)", 1)
		r := batchQuery(t, p, a, "SELECT i FROM batch_metadata; SELECT v,n FROM batch_metadata; SELECT c,f,t FROM batch_metadata; SELECT v,n FROM batch_metadata_alt")
		sets := []fixture.BatchSet{
			{[]string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}, false},
			{[]string{"v", "n"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"seven", "12.345"}}, false},
			{[]string{"c", "f", "t"}, []string{"BPCHAR", "BOOL", "TEXT"}, [][]any{{"xy ", true, "text"}}, false},
			{[]string{"v", "n"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"new", "23.4"}}, false},
		}
		type columnWant struct {
			name, typ                           string
			scan                                reflect.Type
			length, precision, scale            int64
			lengthKnown, decimalKnown, nullable bool
		}
		wants := [][]columnWant{
			{{name: "i", typ: "INT4", scan: reflect.TypeFor[int64]()}},
			{
				{name: "v", typ: "VARCHAR", scan: reflect.TypeFor[string](), length: 7, lengthKnown: true, nullable: true},
				{name: "n", typ: "NUMERIC", scan: reflect.TypeFor[string](), precision: 8, scale: 3, decimalKnown: true, nullable: true},
			},
			{
				{name: "c", typ: "BPCHAR", scan: reflect.TypeFor[string](), length: 3, lengthKnown: true, nullable: true},
				{name: "f", typ: "BOOL", scan: reflect.TypeFor[bool](), nullable: true},
				{name: "t", typ: "TEXT", scan: reflect.TypeFor[string](), length: math.MaxInt64, lengthKnown: true, nullable: true},
			},
			{
				{name: "v", typ: "VARCHAR", scan: reflect.TypeFor[string](), length: 3, lengthKnown: true},
				{name: "n", typ: "NUMERIC", scan: reflect.TypeFor[string](), precision: 5, scale: 1, decimalKnown: true},
			},
		}
		if b.PG {
			wants[0][0].scan = reflect.TypeFor[int32]()
			wants[1][1].scan = reflect.TypeFor[any]()
			wants[2][0].scan = reflect.TypeFor[any]()
			wants[3][1].scan = reflect.TypeFor[any]()
		}
		check := func(columns []*sql.ColumnType, want []columnWant) {
			t.Helper()
			if len(columns) != len(want) {
				t.Fatalf("metadata width=%d want=%d", len(columns), len(want))
			}
			for i, c := range columns {
				w := want[i]
				length, lengthKnown := c.Length()
				precision, scale, decimalKnown := c.DecimalSize()
				nullable, nullableKnown := c.Nullable()
				wantNullable := !b.PG && w.nullable
				if c.Name() != w.name || c.DatabaseTypeName() != w.typ || c.ScanType() != w.scan ||
					length != w.length || lengthKnown != w.lengthKnown ||
					precision != w.precision || scale != w.scale || decimalKnown != w.decimalKnown ||
					nullable != wantNullable || nullableKnown != !b.PG {
					t.Fatalf("column%d name=%s type=%s scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t want=%+v pg=%t",
						i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthKnown, precision, scale, decimalKnown, nullable, nullableKnown, w, b.PG)
				}
			}
		}
		var saved [][]*sql.ColumnType
		for i, set := range sets {
			if i > 0 && !r.NextResultSet() {
				t.Fatalf("missing metadata set%d: %v", i, r.Err())
			}
			columns, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			check(columns, wants[i])
			saved = append(saved, columns)
			fixture.BatchReadSet(t, r, set)
			if err := r.Err(); err != nil {
				t.Fatal(err)
			}
			for j, retained := range saved {
				check(retained, wants[j])
			}
		}
		if r.NextResultSet() || r.Err() != nil {
			t.Fatal("extra metadata set", r.Err())
		}
		for range 2 {
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			for j, retained := range saved {
				check(retained, wants[j])
			}
		}
		batchValues(t, p, b, peer, "SELECT i FROM batch_metadata", 1)
	})
}
