package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func classDiagnosticAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
	run(fixture.Select(`SELECT opcname,opcmethod::bigint,opcintype::bigint,opcdefault,opckeytype::bigint FROM pg_opclass WHERE opcname LIKE 'class_diag_%' ORDER BY opcname`, []string{"opcname", "opcmethod", "opcintype", "opcdefault", "opckeytype"}, []string{"NAME", "INT8", "INT8", "BOOL", "INT8"}, [][]any{{"class_diag_existing", int64(403), int64(23), false, int64(0)}}))
	run(fixture.Select(`SELECT opfname FROM pg_opfamily WHERE opfname LIKE 'class_diag_%' ORDER BY opfname`, []string{"opfname"}, []string{"NAME"}, [][]any{{"class_diag_collision"}, {"class_diag_existing"}}))
	run(fixture.Select(`SELECT f.opfname,a.amprocnum,a.amproc::oid::bigint FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE f.opfname LIKE 'class_diag_%' ORDER BY f.opfname,a.amprocnum`, []string{"opfname", "amprocnum", "amproc"}, []string{"NAME", "INT2", "INT8"}, [][]any{{"class_diag_collision", int64(1), int64(351)}, {"class_diag_existing", int64(1), int64(351)}}))
	run(fixture.Select(`SELECT a.amopstrategy FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname LIKE 'class_diag_%'`, []string{"amopstrategy"}, []string{"INT2"}, nil))
	run(fixture.Select(`SELECT relname FROM pg_class WHERE relname='class_diag_failed_index'`, []string{"relname"}, []string{"NAME"}, nil))
	run(fixture.Select(`SELECT opcname FROM pg_opclass WHERE opcdefault AND opcmethod=403 AND opcintype=23`, []string{"opcname"}, []string{"NAME"}, [][]any{{"int4_ops"}}))
	run(fixture.Select(`SELECT n FROM class_diag_rows`, []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
	run(fixture.Select(`SELECT 42 AS sentinel`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
}
