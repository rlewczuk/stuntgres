package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

var pgSequenceColumns = []string{"seqrelid", "seqtypid", "seqstart", "seqincrement", "seqmax", "seqmin", "seqcache", "seqcycle"}
var pgSequenceTypes = []string{"OID", "OID", "INT8", "INT8", "INT8", "INT8", "INT8", "BOOL"}

const pgSequenceProbe = "SELECT * FROM pg_catalog.pg_sequence WHERE seqrelid=$1::oid"

func pgSequenceMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, pgSequenceColumns) {
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
		if typ.Name() != pgSequenceColumns[i] || typ.DatabaseTypeName() != pgSequenceTypes[i] || typ.ScanType() != scan {
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

func pgSequenceSelect(backend referenceBackend, query string, rows [][]any, args ...any) referenceStep {
	s := referenceSelect(query, pgSequenceColumns, pgSequenceTypes, rows, args...)
	s.bag = true
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgSequenceMetadata(t, rows, backend.pg) }
	return s
}

func pgSequenceCheck(t *testing.T, p *referencePair, b referenceBackend, e referenceExecutor, oid int64, row []any) {
	t.Helper()
	rows := [][]any{}
	if row != nil {
		rows = append(rows, row)
	}
	s := pgSequenceSelect(b, pgSequenceProbe, rows, oid)
	pgDescriptionExecutorRows(t, p, b, e, s)
	pgDescriptionExecutorRows(t, p, b, e, referencePrepared(s))
}

func pgSequenceDefault(oid int64) []any {
	return []any{oid, int64(20), int64(1), int64(1), int64(9223372036854775807), int64(1), int64(1), false}
}
func pgSequenceCustom(oid int64) []any {
	return []any{oid, int64(23), int64(15), int64(7), int64(200), int64(-20), int64(9), true}
}
func pgSequenceExec(t *testing.T, p *referencePair, b referenceBackend, e referenceExecutor, q string) {
	t.Helper()
	referenceRunStep(t, p.ctx, e, b.recorder, referenceExec(q, 0))
}

func TestReferencePGSequenceShape(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			pgSequenceExec(t, p, b, b.db, "CREATE SEQUENCE pgs_shape_sequence")
			pgSequenceExec(t, p, b, b.db, "CREATE TABLE pgs_shape_table(a INT)")
			oid := pgDescriptionRelationID(t, p, b, "pgs_shape_sequence")
			table := pgDescriptionRelationID(t, p, b, "pgs_shape_table")
			pgSequenceCheck(t, p, b, b.db, oid, pgSequenceDefault(oid))
			for _, s := range []referenceStep{
				pgSequenceSelect(b, pgSequenceProbe, [][]any{}, int64(4294967295)),
				pgSequenceSelect(b, pgSequenceProbe, [][]any{}, table),
				pgSequenceSelect(b, pgSequenceProbe, [][]any{}, nil),
				pgSequenceSelect(b, "SELECT * FROM pg_sequence WHERE seqrelid=NULL", [][]any{}),
				pgSequenceSelect(b, "SELECT * FROM pg_catalog.pg_sequence WHERE FALSE", [][]any{}),
			} {
				pgDescriptionRows(t, p, b, s)
				pgDescriptionRows(t, p, b, referencePrepared(s))
			}
			pgDescriptionRows(t, p, b, referenceSelect("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_class WHERE oid=2224", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(2224), "pg_sequence", int64(11), "r", int64(8)}}))
			pgDescriptionRows(t, p, b, referenceSelect("SELECT oid,relkind FROM pg_class WHERE oid=$1::oid", []string{"oid", "relkind"}, []string{"OID", "CHAR"}, [][]any{{oid, "S"}}, oid))
		})
	}
}
