package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL accessor facts were observed on the pinned 18.6 oracle. Native
// expectations follow the public typed-driver contract independently.
func mcvItemsMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 5 {
			t.Fatal(columns, err)
		}
		for i, c := range columns {
			scan := reflect.TypeFor[float64]()
			switch i {
			case 0:
				scan = reflect.TypeFor[int64]()
				if pg {
					scan = reflect.TypeFor[int32]()
				}
			case 1, 2:
				scan = reflect.TypeFor[string]()
				if pg {
					scan = reflect.TypeFor[any]()
				}
			}
			n, nk := c.Nullable()
			l, lk := c.Length()
			p, s, dk := c.DecimalSize()
			if c.Name() != mcvItemsNames[i] || c.DatabaseTypeName() != mcvItemsTypes[i] || c.ScanType() != scan || n || nk || l != 0 || lk || p != 0 || s != 0 || dk {
				t.Fatalf("item metadata %d: name=%q type=%q scan=%v want=%v nullable=%t/%t length=%d/%t decimal=%d,%d/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), scan, n, nk, l, lk, p, s, dk)
			}
		}
	}
}

func mcvItemsRecordSelect(query, name string, pg bool, values [][]any, args ...any) fixture.Step {
	step := ndistinctRawResult(pg, fixture.Select(query, []string{name}, []string{"RECORD"}, values, args...), 0)
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		column := columns[0]
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		n, nk := column.Nullable()
		l, lk := column.Length()
		p, s, dk := column.DecimalSize()
		if column.Name() != name || column.DatabaseTypeName() != "RECORD" || column.ScanType() != scan || n || nk || l != 0 || lk || p != 0 || s != 0 || dk {
			t.Fatalf("item record metadata name=%q type=%q scan=%v want=%v nullable=%t/%t length=%d/%t decimal=%d,%d/%t", column.Name(), column.DatabaseTypeName(), column.ScanType(), scan, n, nk, l, lk, p, s, dk)
		}
	}
	return step
}

func TestReferenceStatisticsMCVItemsMetadata(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		for _, q := range []string{
			"SELECT * FROM pg_mcv_list_items(NULL::pg_mcv_list)",
			"SELECT * FROM pg_mcv_list_items(NULL)",
			"SELECT * FROM pg_catalog.pg_mcv_list_items(mcv_list => NULL::pg_mcv_list)",
			"SELECT * FROM pg_mcv_list_items($1::pg_mcv_list)",
		} {
			var args []any
			if q == "SELECT * FROM pg_mcv_list_items($1::pg_mcv_list)" {
				args = []any{nil}
			}
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				step := fixture.Select(prefix+q, mcvItemsNames, mcvItemsTypes, nil, args...)
				step.CheckMetadata = mcvItemsMetadata(s.pg)
				s.run(step)
			}
		}
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			s.run(mcvItemsRecordSelect(prefix+"SELECT pg_mcv_list_items(NULL::pg_mcv_list)", "pg_mcv_list_items", s.pg, nil))
		}
		// One untied item permits complete raw-column and metadata expectations.
		mcvItemsTypeSetup(s, "integer", "7")
		step := fixture.ArrayReferenceSelect(mcvItemsQuery, mcvItemsNames, mcvItemsTypes, mcvItemsTypes, [][]any{{int64(0), "{7,1}", "{f,f}", float64(1), float64(1)}}, s.pg, "mcv_item_type_s")
		step.CheckMetadata = mcvItemsMetadata(s.pg)
		s.run(step)
		target := fixture.ArrayReferenceSelect("SELECT (pg_mcv_list_items("+mcvItemsArgument+")).*", mcvItemsNames, mcvItemsTypes, mcvItemsTypes, [][]any{{int64(0), "{7,1}", "{f,f}", float64(1), float64(1)}}, s.pg, "mcv_item_type_s")
		target.CheckMetadata = mcvItemsMetadata(s.pg)
		s.run(target)
		s.run(mcvItemsRecordSelect("SELECT pg_mcv_list_items("+mcvItemsArgument+") AS item", "item", s.pg, [][]any{{`(0,"{7,1}","{f,f}",1,1)`}}, "mcv_item_type_s"))
		s.run(fixture.Select("SELECT oid::bigint,proretset,proisstrict,provolatile::text,prosecdef,proleakproof,proparallel::text,prorettype::bigint,proallargtypes::text,proargmodes::text,proargnames::text FROM pg_proc WHERE oid=3427", []string{"oid", "proretset", "proisstrict", "provolatile", "prosecdef", "proleakproof", "proparallel", "prorettype", "proallargtypes", "proargmodes", "proargnames"}, []string{"INT8", "BOOL", "BOOL", "TEXT", "BOOL", "BOOL", "TEXT", "INT8", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(3427), true, true, "s", false, false, "s", int64(2249), "{5017,23,1009,1000,701,701}", "{i,o,o,o,o,o}", "{mcv_list,index,values,nulls,frequency,base_frequency}"}}))
	})
}

func TestReferenceStatisticsMCVItemsComposition(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		indexes := [][]any{{int64(0)}, {int64(1)}, {int64(2)}}
		for _, q := range []string{
			"SELECT m.index FROM pg_catalog.pg_mcv_list_items(mcv_list => " + mcvItemsArgument + ") m ORDER BY m.index",
			"SELECT m.i FROM pg_mcv_list_items(" + mcvItemsArgument + ") AS m(i) ORDER BY m.i",
			"SELECT m.index FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE s.stxname=$1 AND NOT d.stxdinherit ORDER BY m.index",
			"SELECT m.index FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid, pg_mcv_list_items(d.stxdmcv) m WHERE s.stxname=$1 AND NOT d.stxdinherit ORDER BY m.index",
		} {
			name := "index"
			if q == "SELECT m.i FROM pg_mcv_list_items("+mcvItemsArgument+") AS m(i) ORDER BY m.i" {
				name = "i"
			}
			s.run(fixture.Select(q, []string{name}, []string{"INT4"}, indexes, "mcv_s"))
		}
		s.run(fixture.Select("SELECT a.index AS ai,b.index AS bi FROM pg_mcv_list_items("+mcvItemsArgument+") a CROSS JOIN pg_mcv_list_items("+mcvItemsArgument+") b WHERE a.index=0 ORDER BY b.index", []string{"ai", "bi"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(0)}, {int64(0), int64(1)}, {int64(0), int64(2)}}, "mcv_s"))
		s.run(fixture.Select("SELECT z.i,z.j,z.ord FROM ROWS FROM(pg_mcv_list_items("+mcvItemsArgument+"),pg_mcv_list_items(NULL::pg_mcv_list)) WITH ORDINALITY AS z(i,v,n,f,b,j,w,q,g,c,ord) ORDER BY z.i", []string{"i", "j", "ord"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(0), nil, int64(1)}, {int64(1), nil, int64(2)}, {int64(2), nil, int64(3)}}, "mcv_s"))
		s.run(fixture.Select("SELECT t.a,m.index FROM mcv_t t LEFT JOIN LATERAL pg_mcv_list_items(NULL::pg_mcv_list) m ON true WHERE t.a=1 ORDER BY t.a", []string{"a", "index"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(1), nil}}))
		mcvItemsTypeSetup(s, "integer", "7")
		step := fixture.ArrayReferenceSelect("SELECT (pg_mcv_list_items("+mcvItemsArgument+")).*", mcvItemsNames, mcvItemsTypes, mcvItemsTypes, [][]any{{int64(0), "{7,1}", "{f,f}", float64(1), float64(1)}}, s.pg, "mcv_item_type_s")
		step.CheckMetadata = mcvItemsMetadata(s.pg)
		s.run(step)
		s.run(mcvItemsRecordSelect("SELECT pg_mcv_list_items("+mcvItemsArgument+") AS item", "item", s.pg, [][]any{{`(0,"{7,1}","{f,f}",1,1)`}}, "mcv_item_type_s"))
		s.run(fixture.Exec("CREATE VIEW mcv_item_view AS SELECT m.* FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE s.stxname='mcv_item_type_s' AND NOT d.stxdinherit", 0))
		s.run(fixture.ArrayReferenceSelect("SELECT * FROM mcv_item_view ORDER BY index", mcvItemsNames, mcvItemsTypes, mcvItemsTypes, [][]any{{int64(0), "{7,1}", "{f,f}", float64(1), float64(1)}}, s.pg))
	})
}
