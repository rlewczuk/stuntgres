package reference_test

import (
	"database/sql"
	"strconv"
	"testing"
)

func TestReferenceFormatRegclassArrayAnnotation(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE public.format_reg_annotation_target(v integer)", 0))
		var oid int64
		if err := c.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='format_reg_annotation_target'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		// Only allocated identity is derived; the complete output is independent.
		literal := "'{" + strconv.FormatInt(oid, 10) + ",NULL}'"
		for _, operand := range []string{literal + "::regclass[]", "CAST(" + literal + " AS regclass[])"} {
			t.Run(operand, func(t *testing.T) {
				formatReferenceBoth(t, p, b, c, formatReferenceText("format('%s',"+operand+")", "{format_reg_annotation_target,NULL}"))
				formatReferenceBoth(t, p, b, c, formatReferenceText("format('%s/%L',VARIADIC "+operand+")", "format_reg_annotation_target/NULL"))
			})
		}
	})
}
