package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgSequencesColumns = []string{"schemaname", "sequencename", "sequenceowner", "data_type", "start_value", "min_value", "max_value", "increment_by", "cycle", "cache_size", "last_value"}
var pgSequencesTypes = []string{"NAME", "NAME", "NAME", "REGTYPE", "INT8", "INT8", "INT8", "INT8", "BOOL", "INT8", "INT8"}

const pgSequencesProbe = "SELECT * FROM pg_catalog.pg_sequences WHERE sequencename=$1"

func pgSequencesMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, pgSequencesColumns) {
		t.Fatal("view columns", cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 11 {
		t.Fatal("view types", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i < 4 {
			scan = reflect.TypeFor[string]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
		}
		if i == 8 {
			scan = reflect.TypeFor[bool]()
		}
		if typ.Name() != cols[i] || typ.DatabaseTypeName() != pgSequencesTypes[i] || typ.ScanType() != scan {
			t.Errorf("descriptor %d: name=%q type=%q scan=%v want=%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), scan)
		}
		n, known := typ.Nullable()
		if known == pg || n != !pg {
			t.Errorf("nullable %d: %v/%v", i, n, known)
		}
		if n, known := typ.Length(); n != 0 || known {
			t.Errorf("length %d: %d/%v", i, n, known)
		}
		if p, s, known := typ.DecimalSize(); p != 0 || s != 0 || known {
			t.Errorf("precision %d: %d/%d/%v", i, p, s, known)
		}
	}
}

func pgSequencesSelect(b fixture.Backend, q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, pgSequencesColumns, pgSequencesTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgSequencesMetadata(t, rows, b.PG) }
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if col >= 4 {
			return false
		}
		if b.PG {
			if _, ok := v.([]byte); !ok {
				t.Fatalf("raw field %d row %d: %T %#v, want pq bytes", col, row, v, v)
			}
		} else {
			if _, ok := v.(string); !ok {
				t.Fatalf("raw field %d: %T %#v, want native string", col, v, v)
			}
		}
		return true
	}
	return s
}

func pgSequencesOwner(t *testing.T, p *fixture.Pair, e fixture.Executor) string {
	t.Helper()
	var owner string
	rows, err := e.QueryContext(p.Ctx, "SELECT current_user")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("current_user missing", rows.Err())
	}
	if err := rows.Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

func pgSequencesDefault(owner, name string, last any) []any {
	return []any{"public", name, owner, "bigint", int64(1), int64(1), int64(9223372036854775807), int64(1), false, int64(1), last}
}

func pgSequencesCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name string, row []any) {
	t.Helper()
	want := [][]any{}
	if row != nil {
		want = append(want, row)
	}
	s := pgSequencesSelect(b, pgSequencesProbe, want, name)
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
	fixture.PgDescriptionExecutorRows(t, p, b, e, fixture.Prepared(s))
}

func TestReferencePGSequencesShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pgSequenceExec(t, p, b, b.DB, "CREATE SEQUENCE pgsv_shape")
			owner := pgSequencesOwner(t, p, b.DB)
			t.Run("RowsAndMetadata", func(t *testing.T) {
				pgSequencesCheck(t, p, b, b.DB, "pgsv_shape", pgSequencesDefault(owner, "pgsv_shape", nil))
				for _, q := range []string{"SELECT * FROM pg_catalog.pg_sequences WHERE FALSE", "SELECT * FROM pg_sequences WHERE sequencename=NULL", "SELECT * FROM pg_sequences WHERE sequencename='pgsv_absent'"} {
					s := pgSequencesSelect(b, q, [][]any{})
					fixture.PgDescriptionRows(t, p, b, s)
					fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
				}
			})
			t.Run("ViewDefinition", func(t *testing.T) {
				// Independently recorded from the pinned PostgreSQL 18.0
				// pg_views.definition oracle, not native summary metadata.
				// Preserve the complete deparser spelling without normalization.
				const want = ` SELECT n.nspname AS schemaname,
    c.relname AS sequencename,
    pg_get_userbyid(c.relowner) AS sequenceowner,
    (s.seqtypid)::regtype AS data_type,
    s.seqstart AS start_value,
    s.seqmin AS min_value,
    s.seqmax AS max_value,
    s.seqincrement AS increment_by,
    s.seqcycle AS cycle,
    s.seqcache AS cache_size,
    pg_sequence_last_value((c.oid)::regclass) AS last_value
   FROM ((pg_sequence s
     JOIN pg_class c ON ((c.oid = s.seqrelid)))
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE ((NOT pg_is_other_temp_schema(n.oid)) AND (c.relkind = 'S'::"char"));`
				s := fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND viewname='pg_sequences'", []string{"definition"}, []string{"TEXT"}, [][]any{{want}})
				fixture.PgDescriptionRows(t, p, b, s)
				fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
			})
			q := "SELECT c.oid,c.relkind,c.relnatts,c.reltype,t.typrelid,t.typarray,a.typelem FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.relname='pg_sequences'"
			var oid, fields, rowtype, relation, array, element int64
			var kind string
			if err := b.DB.QueryRowContext(p.Ctx, q).Scan(&oid, &kind, &fields, &rowtype, &relation, &array, &element); err != nil {
				t.Fatal(err)
			}
			if oid != 12048 || kind != "v" || fields != 11 || rowtype != 12050 || array != 12049 || relation != oid || element != rowtype {
				t.Errorf("identity oid=%d kind=%q fields=%d rowtype=%d relation=%d array=%d element=%d", oid, kind, fields, rowtype, relation, array, element)
			}
		})
	}
}
