package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func arrayAggDomainSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE DOMAIN aa_di AS INT", "CREATE DOMAIN aa_dj AS aa_di",
		"CREATE DOMAIN aa_da AS INT[]", "CREATE DOMAIN aa_dc AS aa_da",
		"CREATE TABLE aa_types(id INT,i aa_di,j aa_dj,a aa_da,c aa_dc,d aa_di[])",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO aa_types VALUES(1,7,8,'[0:1]={1,NULL}','[0:1]={3,4}','[0:1]={9,NULL}'),(2,NULL,NULL,'[0:1]={2,3}','[0:1]={5,NULL}','[0:1]={10,11}')", 2))
}

func arrayAggDomainSelect(pg bool) fixture.Step {
	types := []string{"_AA_DI", "_AA_DJ", "_INT4", "_INT4", "_AA_DI"}
	if pg {
		types[0], types[1], types[4] = "", "", ""
	}
	s := fixture.ArrayReferenceSelect("SELECT array_agg(i ORDER BY id) AS i,array_agg(j ORDER BY id) AS j,array_agg(a ORDER BY id) AS a,array_agg(c ORDER BY id) AS c,array_agg(d ORDER BY id) AS d FROM aa_types", []string{"i", "j", "a", "c", "d"}, types, []string{"_INT4", "_INT4", "_INT4", "_INT4", "_INT4"}, [][]any{{"{7,NULL}", "{8,NULL}", "[1:2][0:1]={{1,NULL},{2,3}}", "[1:2][0:1]={{3,4},{5,NULL}}", "[1:2][0:1]={{9,NULL},{10,11}}"}}, pg)
	s.CheckMetadata = arrayAggMetadata(pg, []string{"_INT4", "_INT4", "_INT4", "_INT4", "_INT4"})
	return s
}

func TestReferenceArrayAggTypes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, tc := range []struct{ input, typ, want string }{
				{"TRUE::boolean", "_BOOL", "{t}"}, {"7::smallint", "_INT2", "{7}"},
				{"7::bigint", "_INT8", "{7}"}, {"'a,b'::text", "_TEXT", `{"a,b"}`},
				{"'x'::varchar(3)", "_VARCHAR", "{x}"}, {"1.25::numeric(8,2)", "_NUMERIC", "{1.25}"},
				{"'2024-02-29'::date", "_DATE", "{2024-02-29}"},
				{"'12345678-1234-1234-1234-123456789abc'::uuid", "_UUID", "{12345678-1234-1234-1234-123456789abc}"},
			} {
				t.Run(tc.typ, func(t *testing.T) {
					s := fixture.ArrayReferenceSelect("SELECT array_agg("+tc.input+")", []string{"array_agg"}, []string{tc.typ}, []string{tc.typ}, [][]any{{tc.want}}, b.PG)
					s.CheckMetadata = arrayAggMetadata(b.PG, []string{tc.typ})
					for _, prepared := range []bool{false, true} {
						t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						})
					}
				})
			}
			arrayAggDomainSetup(run)
			arrayAggBoth(run, arrayAggDomainSelect(b.PG))
			// Shared views retain the same independently expected domain metadata.
			domain := arrayAggDomainSelect(b.PG)
			run(fixture.Exec("CREATE VIEW aa_type_identity AS "+domain.Query, 0))
			domain.Query = "SELECT i,j,a,c,d FROM aa_type_identity"
			arrayAggBoth(run, domain)
			if b.PG {
				// lib/pq has no dynamic domain-array type names. Existing PG catalog
				// projections independently pin the concrete result associations;
				// native result descriptors/dependencies are checked in engine units.
				q := "SELECT a.attname::text AS name,e.typname::text AS element_name,a.atttypid=e.typarray AS associated,r.typelem=e.oid AS element,a.atttypmod,a.attcollation::bigint AS collation FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type r ON r.oid=a.atttypid JOIN pg_catalog.pg_type e ON e.oid=r.typelem WHERE a.attrelid='aa_type_identity'::regclass AND a.attnum>0 ORDER BY a.attnum"
				arrayAggBoth(run, fixture.Select(q, []string{"name", "element_name", "associated", "element", "atttypmod", "collation"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "INT4", "INT8"}, [][]any{
					{"i", "aa_di", true, true, int64(-1), int64(0)},
					{"j", "aa_dj", true, true, int64(-1), int64(0)},
					{"a", "int4", true, true, int64(-1), int64(0)},
					{"c", "int4", true, true, int64(-1), int64(0)},
					{"d", "aa_di", true, true, int64(-1), int64(0)},
				}))
			}
		})
	}
}

func TestReferenceArrayAggMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			arrayAggSetup(run)
			for _, q := range []string{"SELECT array_agg(v) FROM aa_scalar WHERE FALSE", "SELECT array_agg(v) FROM aa_array WHERE FALSE", "SELECT array_agg(v) FROM aa_array WHERE FALSE GROUP BY id"} {
				want := [][]any{{nil}}
				if q == "SELECT array_agg(v) FROM aa_array WHERE FALSE GROUP BY id" {
					want = nil
				}
				arrayAggBoth(run, arrayAggSelect(q, "_INT4", want, b.PG))
			}
			run(fixture.Exec("CREATE TABLE aa_numeric(id INT,v NUMERIC(8,2))", 0))
			run(fixture.Exec("INSERT INTO aa_numeric VALUES(1,1.25),(2,NULL)", 2))
			s := fixture.ArrayReferenceSelect("SELECT array_agg(v ORDER BY id) AS amount FROM aa_numeric", []string{"amount"}, []string{"_NUMERIC"}, []string{"_NUMERIC"}, [][]any{{"{1.25,NULL}"}}, b.PG)
			s.CheckMetadata = arrayAggMetadata(b.PG, []string{"_NUMERIC"})
			arrayAggBoth(run, s)
			s.Rows = [][]any{{nil}}
			s.Query = "SELECT array_agg(v) AS amount FROM aa_numeric WHERE FALSE"
			arrayAggBoth(run, s)
			run(fixture.Exec("CREATE VIEW aa_builtin_identity AS SELECT array_agg(v) AS scalar_array FROM aa_scalar", 0))
			run(fixture.Exec("CREATE VIEW aa_builtin_array_identity AS SELECT array_agg(v) AS array_array FROM aa_array", 0))
			if b.PG {
				for _, tc := range []struct{ view, column string }{{"aa_builtin_identity", "scalar_array"}, {"aa_builtin_array_identity", "array_array"}} {
					q := "SELECT atttypid::bigint AS oid,atttypmod,attcollation::bigint AS collation FROM pg_catalog.pg_attribute WHERE attrelid='" + tc.view + "'::regclass AND attname='" + tc.column + "'"
					arrayAggBoth(run, fixture.Select(q, []string{"oid", "atttypmod", "collation"}, []string{"INT8", "INT4", "INT8"}, [][]any{{int64(1007), int64(-1), int64(0)}}))
				}
			}
		})
	}
}
