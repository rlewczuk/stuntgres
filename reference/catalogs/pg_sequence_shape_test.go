package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgSequenceTypes = []string{"OID", "OID", "INT8", "INT8", "INT8", "INT8", "INT8", "BOOL"}

const pgSequenceProbe = "SELECT * FROM pg_catalog.pg_sequence WHERE seqrelid=$1::oid"

func pgSequenceMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, fixture.PGSequenceColumns) {
		t.Fatal("sequence columns", cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 8 {
		t.Fatal("sequence types", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 7 {
			scan = reflect.TypeFor[bool]()
		} else if pg && i < 2 {
			scan = reflect.TypeFor[any]()
		}
		if typ.Name() != fixture.PGSequenceColumns[i] || typ.DatabaseTypeName() != pgSequenceTypes[i] || typ.ScanType() != scan {
			t.Fatal("sequence descriptor", i, typ)
		}
		nullable, known := typ.Nullable()
		if nullable || known == pg {
			t.Fatal("sequence NOT NULL / pq unknown", i, nullable, known)
		}
		if n, known := typ.Length(); n != 0 || known {
			t.Fatal("sequence length", i, n, known)
		}
		if p, s, known := typ.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatal("sequence precision", i, p, s, known)
		}
	}
}

func pgSequenceSelect(backend fixture.Backend, query string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, fixture.PGSequenceColumns, pgSequenceTypes, rows, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgSequenceMetadata(t, rows, backend.PG) }
	return s
}

func pgSequenceCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, oid int64, row []any) {
	t.Helper()
	rows := [][]any{}
	if row != nil {
		rows = append(rows, row)
	}
	s := pgSequenceSelect(b, pgSequenceProbe, rows, oid)
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
	fixture.PgDescriptionExecutorRows(t, p, b, e, fixture.Prepared(s))
}

func pgSequenceDefault(oid int64) []any {
	return []any{oid, int64(20), int64(1), int64(1), int64(9223372036854775807), int64(1), int64(1), false}
}
func pgSequenceCustom(oid int64) []any {
	return []any{oid, int64(23), int64(15), int64(7), int64(200), int64(-20), int64(9), true}
}
func pgSequenceExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
}

func TestReferencePGSequenceShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pgSequenceExec(t, p, b, b.DB, "CREATE SEQUENCE pgs_shape_sequence")
			pgSequenceExec(t, p, b, b.DB, "CREATE TABLE pgs_shape_table(a INT)")
			oid := fixture.PGDescriptionRelationID(t, p, b, "pgs_shape_sequence")
			table := fixture.PGDescriptionRelationID(t, p, b, "pgs_shape_table")
			pgSequenceCheck(t, p, b, b.DB, oid, pgSequenceDefault(oid))
			for _, s := range []fixture.Step{
				pgSequenceSelect(b, pgSequenceProbe, [][]any{}, int64(4294967295)),
				pgSequenceSelect(b, pgSequenceProbe, [][]any{}, table),
				pgSequenceSelect(b, pgSequenceProbe, [][]any{}, nil),
				pgSequenceSelect(b, "SELECT * FROM pg_sequence WHERE seqrelid=NULL", [][]any{}),
				pgSequenceSelect(b, "SELECT * FROM pg_catalog.pg_sequence WHERE FALSE", [][]any{}),
			} {
				fixture.PgDescriptionRows(t, p, b, s)
				fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
			}
			fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_class WHERE oid=2224", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(2224), "pg_sequence", int64(11), "r", int64(8)}}))
			fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid,relkind FROM pg_class WHERE oid=$1::oid", []string{"oid", "relkind"}, []string{"OID", "CHAR"}, [][]any{{oid, "S"}}, oid))
		})
	}
}
