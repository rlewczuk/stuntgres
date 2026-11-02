package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Use classBackends, not Pair.Step: a native missing-extension fatal must not
// prevent the independently asserted PostgreSQL sibling from executing.
func compoundCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	admin, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range []string{"ROLLBACK", "RESET ROLE"} {
			if _, err := conn.ExecContext(ctx, q); err != nil {
				t.Error("compound borrower cleanup", err)
			}
		}
		if _, err := admin.ExecContext(ctx, "DROP EXTENSION IF EXISTS btree_gin CASCADE"); err != nil {
			t.Error("compound extension cleanup", err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := admin.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS btree_gin CASCADE"); err != nil {
		t.Fatal(err)
	}
}

const compoundClasses = "('bit_ops','varbit_ops','enum_ops','money_ops')"
const compoundMissingDefaultHint = "You must specify an operator class for the index or define a default operator class for the data type."
const compoundOperatorHint = "No operator matches the given name and argument types. You might need to add explicit type casts."

func compoundIndexAbsent(run func(fixture.Step), table, name string, existing int64) {
	run(fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname=$1", []string{"relname"}, []string{"NAME"}, nil, name))
	run(amCount("SELECT count(*) FROM pg_index WHERE indrelid=$1::regclass", existing, table))
	run(amCount("SELECT count(*) FROM pg_depend d JOIN pg_class c ON c.oid=d.objid WHERE d.classid='pg_class'::regclass AND c.relnamespace='public'::regnamespace AND c.relname=$1", 0, name))
}

// Exact scalar metadata, including empty results. Reuse the common descriptor
// checks and the existing enum helper's pq unknown-OID contract. Bit widths
// follow the stored-source witnesses in bitwise_aggregate_metadata_test.go.
func compoundValueStep(b fixture.Backend, query, typ string, width int64, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"id", "v"}, []string{"INT4", typ}, values, args...)
	if typ == "" {
		s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			if column != 1 {
				return false
			}
			want := values[row][column]
			if want == nil {
				if value != nil {
					t.Fatalf("enum NULL carrier %T", value)
				}
				return true
			}
			if b.PG {
				raw, ok := value.([]byte)
				if !ok || raw == nil || string(raw) != want {
					t.Fatalf("enum carrier %T %v want %v", value, value, want)
				}
			} else if raw, ok := value.(string); !ok || raw != want {
				t.Fatalf("enum carrier %T %v want %v", value, value, want)
			}
			return true
		}
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.ScalarAssertDescription(t, cols, s.Columns, s.Types)
		fixture.ScalarAssertMetadata(t, b.PG, cols)
		for i, c := range cols {
			scan := reflect.TypeFor[string]()
			if i == 0 {
				scan = reflect.TypeFor[int64]()
				if b.PG {
					scan = reflect.TypeFor[int32]()
				}
			} else if b.PG && (typ == "MONEY" || typ == "") {
				scan = reflect.TypeFor[any]()
			}
			if c.ScanType() != scan {
				t.Fatalf("column %d ScanType=%v want %v", i, c.ScanType(), scan)
			}
			n, nk := c.Nullable()
			if n != !b.PG || nk != !b.PG {
				t.Fatalf("column %d nullable=%t/%t", i, n, nk)
			}
			length, lk := c.Length()
			wantLength, wantKnown := int64(0), false
			if i == 1 && (typ == "BIT" || typ == "VARBIT") {
				wantLength, wantKnown = width, true
			}
			if length != wantLength || lk != wantKnown {
				t.Fatalf("column %d length=%d/%t want %d/%t", i, length, lk, wantLength, wantKnown)
			}
			precision, scale, dk := c.DecimalSize()
			if precision != 0 || scale != 0 || dk {
				t.Fatalf("column %d decimal=%d,%d/%t", i, precision, scale, dk)
			}
		}
	}
	return s
}

// A common actual-value seed serves native controls and PostgreSQL replay.
// Every table has explicit and default indexes; the mixed index retains the
// existing array/vector lanes without introducing another control fixture.
func compoundControlSeed(run func(fixture.Step)) {
	run(fixture.Exec("SET lc_monetary='C'", 0))
	run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
	run(fixture.Exec("CREATE TYPE compound_saved_enum AS ENUM('z','a','m')", 0))
	run(fixture.Exec("ALTER TYPE compound_saved_enum ADD VALUE 'q' BEFORE 'a'", 0))
	run(fixture.Exec("CREATE TABLE compound_saved(id integer,b bit(3),v varbit,e compound_saved_enum,m money,a integer[],t tsvector)", 0))
	run(fixture.Exec("CREATE INDEX compound_saved_i ON compound_saved USING gin(b bit_ops,v varbit_ops,e enum_ops,m money_ops,a,t)", 0))
	for _, column := range []string{"b", "v", "e", "m"} {
		run(fixture.Exec("CREATE INDEX compound_saved_"+column+" ON compound_saved USING gin("+column+")", 0))
	}
	run(fixture.Exec("ALTER INDEX compound_saved_i SET(fastupdate=off,gin_pending_list_limit=64)", 0))
	run(fixture.Exec("INSERT INTO compound_saved VALUES(1,'100','0','a','0.01','{2,2}','cat:1 rat:2'),(2,'011','','q','-0.01','{}','dog:1'),(3,NULL,NULL,NULL,NULL,NULL,NULL),(4,'100','00','a','0.01','{2}','cat:1 rat:2')", 4))
}

func compoundControlCheck(b fixture.Backend, run func(fixture.Step), indexCount ...int64) {
	indexes := int64(5)
	if len(indexCount) != 0 {
		indexes = indexCount[0]
	}
	for _, s := range []fixture.Step{
		fixture.Select("SELECT extversion,extrelocatable FROM pg_extension WHERE extname='btree_gin'", []string{"extversion", "extrelocatable"}, []string{"TEXT", "BOOL"}, [][]any{{"1.3", true}}),
		fixture.Select("SELECT c.opcintype::bigint,c.opcmethod::bigint,c.opcdefault,c.opckeytype::bigint,f.opfmethod::bigint,(SELECT count(*) FROM pg_amop a WHERE a.amopfamily=f.oid) AS strategies,(SELECT count(*) FROM pg_amproc a WHERE a.amprocfamily=f.oid) AS supports,c.oid<>0 AND f.oid<>0 AS allocated FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily WHERE c.opcmethod=2742 AND c.opcintype IN(790,1560,1562,3500) ORDER BY c.opcintype",
			[]string{"opcintype", "opcmethod", "opcdefault", "opckeytype", "opfmethod", "strategies", "supports", "allocated"}, []string{"INT8", "INT8", "BOOL", "INT8", "INT8", "INT8", "INT8", "BOOL"},
			[][]any{{int64(790), int64(2742), true, int64(0), int64(2742), int64(5), int64(5), true}, {int64(1560), int64(2742), true, int64(0), int64(2742), int64(5), int64(5), true}, {int64(1562), int64(2742), true, int64(0), int64(2742), int64(5), int64(5), true}, {int64(3500), int64(2742), true, int64(0), int64(2742), int64(5), int64(5), true}}),
		ginIDs("SELECT id FROM compound_saved ORDER BY id", []int64{1, 2, 3, 4}),
		ginIDs("SELECT id FROM compound_saved WHERE b='100'::bit(3) AND v>='0'::varbit AND e>='a'::compound_saved_enum AND m>='0.01'::money AND a @> '{2}'::integer[] AND t @@ 'cat <-> rat'::tsquery ORDER BY id", []int64{1, 4}),
		ginIDs("SELECT id FROM compound_saved WHERE b<'100'::bit(3) AND v<'0'::varbit AND e<'a'::compound_saved_enum AND m<'0'::money ORDER BY id", []int64{2}),
		ginIDs("SELECT id FROM compound_saved WHERE b IS NULL AND v IS NULL AND e IS NULL AND m IS NULL ORDER BY id", []int64{3}),
		fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='compound_saved_i'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{"{fastupdate=off,gin_pending_list_limit=64}"}}),
		amCount("SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='compound_saved'::regclass AND c.relam=2742 AND i.indisvalid AND i.indisready AND i.indislive", indexes),
		compoundValueStep(b, "SELECT id,b AS v FROM compound_saved ORDER BY id", "BIT", 3, [][]any{{int64(1), "100"}, {int64(2), "011"}, {int64(3), nil}, {int64(4), "100"}}),
		compoundValueStep(b, "SELECT id,v FROM compound_saved ORDER BY id", "VARBIT", -1, [][]any{{int64(1), "0"}, {int64(2), ""}, {int64(3), nil}, {int64(4), "00"}}),
		compoundValueStep(b, "SELECT id,e AS v FROM compound_saved ORDER BY id", "", 0, [][]any{{int64(1), "a"}, {int64(2), "q"}, {int64(3), nil}, {int64(4), "a"}}),
		compoundValueStep(b, "SELECT id,m AS v FROM compound_saved ORDER BY id", "MONEY", 0, [][]any{{int64(1), "$0.01"}, {int64(2), "-$0.01"}, {int64(3), nil}, {int64(4), "$0.01"}}),
	} {
		run(s)
		run(fixture.Prepared(s))
	}
}

func compoundControlPostDML(run func(fixture.Step)) {
	run(fixture.Prepared(fixture.Exec("INSERT INTO compound_saved VALUES($1,$2::bit(3),$3::varbit,$4::compound_saved_enum,$5::money,$6::integer[],$7::tsvector)", 1, 5, "111", "10", "m", "0.02", "{2}", "cat:1 rat:2")))
	run(ginIDs("SELECT id FROM compound_saved WHERE b>'100'::bit(3) AND v>'0'::varbit AND e>'a'::compound_saved_enum AND m>'0.01'::money ORDER BY id", []int64{5}))
	run(fixture.Exec("UPDATE compound_saved SET b='000',v='',e='z',m='-0.02' WHERE id=5", 1))
	run(ginIDs("SELECT id FROM compound_saved WHERE b<'011'::bit(3) AND v=''::varbit AND e<'q'::compound_saved_enum AND m<'-0.01'::money ORDER BY id", []int64{5}))
	run(fixture.Exec("DELETE FROM compound_saved WHERE id=5", 1))
}
