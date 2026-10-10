package reference_test

import (
	"database/sql"
	"testing"
)

// These existing scalar families require the EXECUTE actual's own demand
// captures, before the prepared body's evaluator opens its query sources.
func TestReferenceSQLPreparedInquiryActuals(t *testing.T) {
	for _, tc := range []struct {
		name, formal, actual, typ string
		value any
	}{
		{"comment", "text", "obj_description('prep_inquiry'::regclass,'pg_class')", "TEXT", "prepared comment"},
		{"deparse", "text", "pg_get_serial_sequence('prep_inquiry','v')", "TEXT", nil},
		{"index_property", "bool", "pg_index_has_property('prep_inquiry_idx'::regclass,'clusterable')", "BOOL", true},
		{"visibility", "bool", "pg_type_is_visible(23)", "BOOL", true},
		{"strict_comment_null", "text", "obj_description(NULL,'pg_class')", "TEXT", nil},
		{"strict_deparse_null", "text", "pg_get_serial_sequence(NULL,'v')", "TEXT", nil},
		{"strict_index_null", "bool", "pg_index_has_property(NULL,'clusterable')", "BOOL", nil},
		{"strict_visibility_null", "bool", "pg_type_is_visible(NULL)", "BOOL", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				sqlPreparedStep(t, p, b, c, referenceExec("CREATE TABLE prep_inquiry(v int)", 0))
				sqlPreparedStep(t, p, b, c, referenceExec("COMMENT ON TABLE prep_inquiry IS 'prepared comment'", 0))
				sqlPreparedStep(t, p, b, c, referenceExec("CREATE INDEX prep_inquiry_idx ON prep_inquiry(v)", 0))
				sqlPreparedStep(t, p, b, c, referenceExec("PREPARE p("+tc.formal+") AS SELECT $1 AS value", 0))
				for _, prepared := range []bool{false, true} {
					s := sqlPreparedScalar("EXECUTE p("+tc.actual+")", "value", tc.typ, tc.value)
					s.prepared = prepared
					sqlPreparedStep(t, p, b, c, s)
				}
			})
		})
	}
}
