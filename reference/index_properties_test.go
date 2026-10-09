package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func indexPropertiesScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func indexPropertiesBoolStep(q string, labels []string, values ...any) referenceStep {
	types := make([]string, len(labels))
	for i := range types {
		types[i] = "BOOL"
	}
	s := referenceSelect(q, labels, types, [][]any{values})
	s.checkMetadata = visibilityBoolMetadata
	return s
}

func indexPropertiesSeed(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE ip_data(a INT,b INT,p TEXT)", 0),
		referenceExec("INSERT INTO ip_data VALUES(1,2,'one'),(2,3,'two')", 2),
		referenceExec("CREATE INDEX ip_default ON ip_data(a)", 0),
		referenceExec("CREATE INDEX ip_desc ON ip_data(a DESC)", 0),
		referenceExec("CREATE INDEX ip_mixed ON ip_data(b ASC NULLS FIRST,a DESC NULLS LAST,(a+1) DESC,a) INCLUDE(p)", 0),
		referenceExec("CREATE TABLE ip_unique(a INT UNIQUE)", 0),
		referenceExec("CREATE VIEW ip_plain_view AS SELECT a FROM ip_data", 0),
		referenceExec("CREATE SEQUENCE ip_sequence", 0),
	} {
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	}
}

func TestReferenceIndexPropertiesAccessMethods(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		props := []string{"can_order", "can_unique", "can_multi_col", "can_exclude", "can_include"}
		for _, tc := range []struct {
			oid    int
			values []any
		}{
			{403, []any{true, true, true, true, true}}, {405, []any{false, false, false, true, false}},
			{783, []any{false, false, true, true, true}}, {2742, []any{false, false, true, false, false}},
			{4000, []any{false, false, false, true, true}}, {3580, []any{false, false, true, false, false}},
		} {
			calls := make([]string, len(props))
			for i, prop := range props {
				calls[i] = fmt.Sprintf("pg_indexam_has_property(%d,'%s') AS %s", tc.oid, prop, prop)
			}
			referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT "+strings.Join(calls, ","), props, tc.values...))
		}
		for _, oid := range []string{"0", "2", "4294967295::oid"} {
			referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT pg_indexam_has_property("+oid+",'can_order') AS result", []string{"result"}, nil))
		}
		for _, prop := range []string{"bogus", "", " can_order", "can_order ", "asc", "backward_scan"} {
			referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT pg_indexam_has_property(403,$1) AS result", []string{"result"}, nil).withIndexPropertyArgs(prop))
		}
		referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT pg_catalog.pg_indexam_has_property(403,'CaN_OrDeR')", []string{"pg_indexam_has_property"}, true))
	})
}

func (s referenceStep) withIndexPropertyArgs(args ...any) referenceStep { s.args = args; return s }

func TestReferenceIndexPropertiesIndexDefinitions(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		for _, name := range []string{"ip_default", "ip_desc", "ip_mixed", "ip_unique_a_key"} {
			for _, prop := range []string{"clusterable", "index_scan", "bitmap_scan", "backward_scan"} {
				s := indexPropertiesBoolStep("SELECT pg_index_has_property($1,$2) AS result", []string{"result"}, true)
				s.args = []any{name, prop}
				referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
			}
		}
		for _, prop := range []string{"can_order", "asc", "bogus", "", " index_scan", "index_scan "} {
			s := indexPropertiesBoolStep("SELECT pg_index_has_property('ip_default',$1) AS result", []string{"result"}, nil)
			s.args = []any{prop}
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT pg_index_has_property('ip_default','InDeX_ScAn')", []string{"pg_index_has_property"}, true))
	})
}

func TestReferenceIndexPropertiesColumns(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
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
			referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT "+strings.Join(calls, ","), props, tc.want...))
		}
		for _, prop := range []string{"can_order", "index_scan", "bogus", "", " asc", "asc "} {
			s := indexPropertiesBoolStep("SELECT pg_index_column_has_property('ip_mixed',1,$1) AS result", []string{"result"}, nil)
			s.args = []any{prop}
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
	})
}

func TestReferenceIndexPropertiesNullAndInvalid(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		for _, call := range []string{
			"pg_indexam_has_property(NULL,'can_order')", "pg_indexam_has_property(403,NULL::text)",
			"pg_index_has_property(NULL,'index_scan')", "pg_index_has_property('ip_default',NULL)",
			"pg_index_column_has_property(NULL,1,'asc')", "pg_index_column_has_property('ip_default',NULL,'asc')", "pg_index_column_has_property('ip_default',1,NULL)",
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT "+call+" AS result", []string{"result"}, nil))
		}
		for _, obj := range []string{"0::oid", "4294967295::oid", "'ip_data'", "'ip_plain_view'", "'ip_sequence'"} {
			referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT pg_index_has_property("+obj+",'index_scan') AS whole,pg_index_column_has_property("+obj+",1,'asc') AS col", []string{"whole", "col"}, nil, nil))
		}
		for _, ordinal := range []int{-2147483648, -1, 0, 6, 2147483647} {
			s := indexPropertiesBoolStep("SELECT pg_index_column_has_property('ip_mixed',$1,'asc') AS result", []string{"result"}, nil)
			s.args = []any{ordinal}
			referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
		}
		for _, s := range []referenceStep{
			indexPropertiesBoolStep("SELECT pg_indexam_has_property($1,$2) AS result", []string{"result"}, nil).withIndexPropertyArgs(nil, "can_order"),
			indexPropertiesBoolStep("SELECT pg_indexam_has_property($1,$2) AS result", []string{"result"}, nil).withIndexPropertyArgs(403, nil),
			indexPropertiesBoolStep("SELECT pg_index_has_property($1,$2) AS result", []string{"result"}, nil).withIndexPropertyArgs(nil, "index_scan"),
			indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, nil).withIndexPropertyArgs(nil, 1, "asc"),
			indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, nil).withIndexPropertyArgs("ip_default", 1, nil),
			indexPropertiesBoolStep("SELECT pg_index_has_property($1,$2) AS result", []string{"result"}, nil).withIndexPropertyArgs("ip_default", nil),
			indexPropertiesBoolStep("SELECT pg_index_column_has_property($1,$2,$3) AS result", []string{"result"}, nil).withIndexPropertyArgs("ip_default", nil, "asc"),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
		}
		s := indexPropertiesBoolStep("SELECT pg_index_has_property('ip_default','index_scan') AS result FROM ip_data WHERE a=99", []string{"result"})
		s.rows = nil
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	})
}

func TestReferenceIndexPropertiesSignaturesAndInputs(t *testing.T) {
	indexPropertiesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		indexPropertiesSeed(t, p, b, c)
		for _, input := range []string{"'ip_default'", "'ip_default'::text", "'ip_default'::varchar", "'ip_default'::regclass"} {
			s := indexPropertiesBoolStep("SELECT pg_index_has_property("+input+",'index_scan') AS result", []string{"result"}, true)
			for _, prepared := range []bool{false, true} {
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			}
		}
		for _, input := range []string{"oid", "oid::integer", "oid::bigint"} {
			s := indexPropertiesBoolStep("SELECT pg_index_has_property("+input+",'index_scan') AS result FROM pg_class WHERE relname='ip_default'", []string{"result"}, true)
			for _, prepared := range []bool{false, true} {
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			}
		}
		for _, input := range []string{"0::smallint", "0::integer", "0::bigint", "-1::integer", "4294967295::bigint"} {
			referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT pg_index_has_property("+input+",'index_scan') AS result", []string{"result"}, nil))
		}
		for _, s := range []referenceStep{referenceExec("CREATE DOMAIN ip_reg AS regclass", 0), referenceExec("CREATE DOMAIN ip_text AS text", 0)} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		for _, step := range []referenceStep{
			referenceExec("CREATE TABLE ip_domain_inputs(r ip_reg,t ip_text)", 0),
			referenceExec("INSERT INTO ip_domain_inputs VALUES('ip_default','ip_default')", 1),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, step)
		}
		for _, column := range []string{"r", "t"} {
			referenceRunStep(t, p.ctx, c, b.recorder, indexPropertiesBoolStep("SELECT pg_index_has_property("+column+",'index_scan') AS result FROM ip_domain_inputs", []string{"result"}, true))
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
				s.args = tc.args
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE ip_inputs(obj OID,n INT,prop TEXT)", 0))
		for _, values := range []string{"1,'asc'", "5,'orderable'", "5,'asc'"} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO ip_inputs SELECT oid,"+values+" FROM pg_class WHERE relname='ip_mixed'", 1))
		}
		s := referenceSelect("SELECT pg_index_column_has_property(obj,n,prop) AS result FROM ip_inputs ORDER BY n,prop", []string{"result"}, []string{"BOOL"}, [][]any{{true}, {nil}, {false}})
		s.checkMetadata = visibilityBoolMetadata
		referenceRunStep(t, p.ctx, c, b.recorder, s)
		// Metadata is checked before iteration even when no values exist.
		s.rows = nil
		s.query = "SELECT pg_index_column_has_property(obj,n,prop) AS result FROM ip_inputs WHERE n=99"
		referenceRunStep(t, p.ctx, c, b.recorder, s)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE SEQUENCE ip_strict", 0))
		for _, prepared := range []bool{false, true} {
			s := indexPropertiesBoolStep("SELECT pg_indexam_has_property(nextval('ip_strict')::oid,NULL::text) AS result FROM ip_data WHERE a=1", []string{"result"}, nil)
			s.prepared = prepared
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		// Strict NULL folding must not advance a volatile argument.
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT nextval('ip_strict') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		q := "SELECT pg_index_column_has_property($1::text,1,'desc') AS result"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		s = indexPropertiesBoolStep(q, []string{"result"}, false).withIndexPropertyArgs("ip_default")
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE INDEX ip_dynamic_name ON ip_data(a DESC)", 0))
		s = indexPropertiesBoolStep(q, []string{"result"}, true).withIndexPropertyArgs("ip_dynamic_name")
		referenceRunStep(t, p.ctx, held, b.recorder, s)
	})
}
