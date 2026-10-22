package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func indexPropertiesScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func indexPropertiesBoolStep(q string, labels []string, values ...any) fixture.Step {
	types := make([]string, len(labels))
	for i := range types {
		types[i] = "BOOL"
	}
	s := fixture.Select(q, labels, types, [][]any{values})
	s.CheckMetadata = fixture.VisibilityBoolMetadata
	return s
}

func indexPropertiesSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ip_data(a INT,b INT,p TEXT)", 0),
		fixture.Exec("INSERT INTO ip_data VALUES(1,2,'one'),(2,3,'two')", 2),
		fixture.Exec("CREATE INDEX ip_default ON ip_data(a)", 0),
		fixture.Exec("CREATE INDEX ip_desc ON ip_data(a DESC)", 0),
		fixture.Exec("CREATE INDEX ip_mixed ON ip_data(b ASC NULLS FIRST,a DESC NULLS LAST,(a+1) DESC,a) INCLUDE(p)", 0),
		fixture.Exec("CREATE TABLE ip_unique(a INT UNIQUE)", 0),
		fixture.Exec("CREATE VIEW ip_plain_view AS SELECT a FROM ip_data", 0),
		fixture.Exec("CREATE SEQUENCE ip_sequence", 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceIndexPropertiesAccessMethods(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		props := []string{"can_order", "can_unique", "can_multi_col", "can_exclude", "can_include"}
		for _, tc := range []struct {
			oid    int
			values []any
		}{
			{403, []any{true, true, true, true, true}}, {405, []any{false, false, false, true, false}},
			{783, []any{false, false, true, true, true}}, {2742, []any{false, false, true, false, false}},
			{4000, []any{false, false, false, true, true}}, {3580, []any{false, false, true, false, false}},
		} {
			// Native exposes no method descriptor for these unsupported AMs.
			// Preserve the released PostgreSQL vectors independently; native
			// inquiry must agree with its actual catalog rather than static OIDs.
			if tc.oid == 2742 || tc.oid == 4000 || tc.oid == 3580 {
				n := int64(1)
				if !b.PG {
					tc.values = []any{nil, nil, nil, nil, nil}
					n = 0
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, amCount(`SELECT count(*) FROM pg_am WHERE oid=$1::oid`, n, tc.oid))
			}
			calls := make([]string, len(props))
			for i, prop := range props {
				calls[i] = fmt.Sprintf("pg_indexam_has_property(%d,'%s') AS %s", tc.oid, prop, prop)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT "+strings.Join(calls, ","), props, tc.values...))
		}
		for _, oid := range []string{"0", "2", "4294967295::oid"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT pg_indexam_has_property("+oid+",'can_order') AS result", []string{"result"}, nil))
		}
		for _, prop := range []string{"bogus", "", " can_order", "can_order ", "asc", "backward_scan"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT pg_indexam_has_property(403,$1) AS result", []string{"result"}, nil).WithIndexPropertyArgs(prop))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT pg_catalog.pg_indexam_has_property(403,'CaN_OrDeR')", []string{"pg_indexam_has_property"}, true))
	})
}

func TestReferenceIndexPropertiesIndexDefinitions(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		for _, name := range []string{"ip_default", "ip_desc", "ip_mixed", "ip_unique_a_key"} {
			for _, prop := range []string{"clusterable", "index_scan", "bitmap_scan", "backward_scan"} {
				s := indexPropertiesBoolStep("SELECT pg_index_has_property($1,$2) AS result", []string{"result"}, true)
				s.Args = []any{name, prop}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		}
		for _, prop := range []string{"can_order", "asc", "bogus", "", " index_scan", "index_scan "} {
			s := indexPropertiesBoolStep("SELECT pg_index_has_property('ip_default',$1) AS result", []string{"result"}, nil)
			s.Args = []any{prop}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT pg_index_has_property('ip_default','InDeX_ScAn')", []string{"pg_index_has_property"}, true))
	})
}

func TestReferenceIndexPropertiesColumns(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		props := []string{"asc", "desc", "nulls_first", "nulls_last", "orderable", "distance_orderable", "returnable", "search_array", "search_nulls"}
		for _, tc := range []struct {
			name    string
			ordinal int
			want    []any
		}{
			{"ip_default", 1, []any{true, false, false, true, true, false, true, true, true}},
			{"ip_desc", 1, []any{false, true, true, false, true, false, true, true, true}},
			{"ip_mixed", 1, []any{true, false, true, false, true, false, true, true, true}},
			{"ip_mixed", 2, []any{false, true, false, true, true, false, true, true, true}},
			{"ip_mixed", 3, []any{false, true, true, false, true, false, true, true, true}},
			{"ip_mixed", 4, []any{true, false, false, true, true, false, true, true, true}},
			{"ip_mixed", 5, []any{nil, nil, nil, nil, false, false, true, nil, nil}},
			{"ip_unique_a_key", 1, []any{true, false, false, true, true, false, true, true, true}},
		} {
			calls := make([]string, len(props))
			for i, prop := range props {
				calls[i] = fmt.Sprintf("pg_index_column_has_property('%s',%d,'%s') AS %s", tc.name, tc.ordinal, prop, prop)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT "+strings.Join(calls, ","), props, tc.want...))
		}
		for _, prop := range []string{"can_order", "index_scan", "bogus", "", " asc", "asc "} {
			s := indexPropertiesBoolStep("SELECT pg_index_column_has_property('ip_mixed',1,$1) AS result", []string{"result"}, nil)
			s.Args = []any{prop}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func TestReferenceIndexPropertiesNullAndInvalid(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		for _, call := range []string{
			"pg_indexam_has_property(NULL,'can_order')", "pg_indexam_has_property(403,NULL::text)",
			"pg_index_has_property(NULL,'index_scan')", "pg_index_has_property('ip_default',NULL)",
			"pg_index_column_has_property(NULL,1,'asc')", "pg_index_column_has_property('ip_default',NULL,'asc')", "pg_index_column_has_property('ip_default',1,NULL)",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT "+call+" AS result", []string{"result"}, nil))
		}
		for _, obj := range []string{"0::oid", "4294967295::oid", "'ip_data'", "'ip_plain_view'", "'ip_sequence'"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT pg_index_has_property("+obj+",'index_scan') AS whole,pg_index_column_has_property("+obj+",1,'asc') AS col", []string{"whole", "col"}, nil, nil))
		}
		for _, ordinal := range []int{-2147483648, -1, 0, 6, 2147483647} {
			s := indexPropertiesBoolStep("SELECT pg_index_column_has_property('ip_mixed',$1,'asc') AS result", []string{"result"}, nil)
			s.Args = []any{ordinal}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, s := range []fixture.Step{
			indexPropertiesBoolStep("SELECT pg_indexam_has_property($1,$2) AS result", []string{"result"}, nil).WithIndexPropertyArgs(nil, "can_order"),
			indexPropertiesBoolStep("SELECT pg_indexam_has_property($1,$2) AS result", []string{"result"}, nil).WithIndexPropertyArgs(403, nil),
			indexPropertiesBoolStep("SELECT pg_index_has_property($1,$2) AS result", []string{"result"}, nil).WithIndexPropertyArgs(nil, "index_scan"),
			indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, nil).WithIndexPropertyArgs(nil, 1, "asc"),
			indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, nil).WithIndexPropertyArgs("ip_default", 1, nil),
			indexPropertiesBoolStep("SELECT pg_index_has_property($1,$2) AS result", []string{"result"}, nil).WithIndexPropertyArgs("ip_default", nil),
			indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, nil).WithIndexPropertyArgs("ip_default", nil, "asc"),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		s := indexPropertiesBoolStep("SELECT pg_index_has_property('ip_default','index_scan') AS result FROM ip_data WHERE a=99", []string{"result"})
		s.Rows = nil
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	})
}

func TestReferenceIndexPropertiesSignaturesAndInputs(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		for _, input := range []string{"'ip_default'", "'ip_default'::text", "'ip_default'::varchar", "'ip_default'::regclass"} {
			s := indexPropertiesBoolStep("SELECT pg_index_has_property("+input+",'index_scan') AS result", []string{"result"}, true)
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, input := range []string{"oid", "oid::integer", "oid::bigint"} {
			s := indexPropertiesBoolStep("SELECT pg_index_has_property("+input+",'index_scan') AS result FROM pg_class WHERE relname='ip_default'", []string{"result"}, true)
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, input := range []string{"0::smallint", "0::integer", "0::bigint", "-1::integer", "4294967295::bigint"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT pg_index_has_property("+input+",'index_scan') AS result", []string{"result"}, nil))
		}
		for _, s := range []fixture.Step{fixture.Exec("CREATE DOMAIN ip_reg AS regclass", 0), fixture.Exec("CREATE DOMAIN ip_text AS text", 0)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE ip_domain_inputs(r ip_reg,t ip_text)", 0),
			fixture.Exec("INSERT INTO ip_domain_inputs VALUES('ip_default','ip_default')", 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
		for _, column := range []string{"r", "t"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, indexPropertiesBoolStep("SELECT pg_index_has_property("+column+",'index_scan') AS result FROM ip_domain_inputs", []string{"result"}, true))
		}
		for _, tc := range []struct {
			q    string
			args []any
			want any
		}{
			{"SELECT pg_indexam_has_property($1,$2) AS result", []any{403, "can_order"}, true},
			{"SELECT pg_index_has_property($1,$2) AS result", []any{"ip_default", "index_scan"}, true},
			{"SELECT pg_index_column_has_property($1,$2,$3) AS result", []any{"ip_mixed", 5, "orderable"}, false},
			{"SELECT pg_index_column_has_property($1,$2,$3) AS result", []any{"ip_mixed", 5, "asc"}, nil},
		} {
			for _, prepared := range []bool{false, true} {
				s := indexPropertiesBoolStep(tc.q, []string{"result"}, tc.want)
				s.Args = tc.args
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ip_inputs(obj OID,n INT,prop TEXT)", 0))
		for _, values := range []string{"1,'asc'", "5,'orderable'", "5,'asc'"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ip_inputs SELECT oid,"+values+" FROM pg_class WHERE relname='ip_mixed'", 1))
		}
		s := fixture.Select("SELECT pg_index_column_has_property(obj,n,prop) AS result FROM ip_inputs ORDER BY n,prop", []string{"result"}, []string{"BOOL"}, [][]any{{true}, {nil}, {false}})
		s.CheckMetadata = fixture.VisibilityBoolMetadata
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		// Metadata is checked before iteration even when no values exist.
		s.Rows = nil
		s.Query = "SELECT pg_index_column_has_property(obj,n,prop) AS result FROM ip_inputs WHERE n=99"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE ip_strict", 0))
		for _, prepared := range []bool{false, true} {
			s := indexPropertiesBoolStep("SELECT pg_indexam_has_property(nextval('ip_strict')::oid,NULL::text) AS result FROM ip_data WHERE a=1", []string{"result"}, nil)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		// Strict NULL folding must not advance a volatile argument.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('ip_strict') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		q := "SELECT pg_index_column_has_property($1::text,1,'desc') AS result"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s = indexPropertiesBoolStep(q, []string{"result"}, false).WithIndexPropertyArgs("ip_default")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE INDEX ip_dynamic_name ON ip_data(a DESC)", 0))
		s = indexPropertiesBoolStep(q, []string{"result"}, true).WithIndexPropertyArgs("ip_dynamic_name")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	})
}
