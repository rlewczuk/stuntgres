package reference_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func szColumnDDL(n int) string {
	columns := make([]string, n)
	for i := range columns {
		columns[i] = fmt.Sprintf("c%04d integer", i+1)
	}
	return strings.Join(columns, ",")
}

func szTableColumns(c *szCase, dropped bool) {
	c.t.Helper()
	want := make([][]any, 0, 1600)
	for i := 1; i <= 1600; i++ {
		if dropped && i == 1 {
			continue
		}
		want = append(want, []any{fmt.Sprintf("c%04d", i)})
	}
	c.run(c.conn, fixture.Select(`SELECT attname FROM pg_catalog.pg_attribute WHERE attrelid='public.sz_wide'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum`, []string{"attname"}, []string{"NAME"}, want))
}

func szNotices(c *szCase, original, clipped string, count int) {
	c.t.Helper()
	var want []fixture.Diagnostic
	for range count {
		want = append(want, fixture.Diagnostic{
			Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622",
			Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, original, clipped),
		})
	}
	if !reflect.DeepEqual(*c.notices, want) {
		c.t.Fatalf("notices=%#v want %#v", *c.notices, want)
	}
	*c.notices = nil
}

func TestReferenceInfoSQLSizingBoundaries(t *testing.T) {
	t.Run("table physical high water", func(t *testing.T) {
		szScenario(t, false, func(c *szCase) {
			// DDL widths are independent witnesses, never read from sql_sizing.
			c.exec("CREATE TABLE public.sz_wide(" + szColumnDDL(1600) + ")")
			szTableColumns(c, false)
			c.check(c.conn, szQuery+" WHERE sizing_id=101", [][]any{{int64(101), "MAXIMUM COLUMNS IN TABLE", int64(1600), nil}})
			denied := func(q string) {
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("54011", "tables can have at most 1600 columns"))
				s.ErrorStage = "execute"
				c.run(c.conn, s)
			}
			denied("ALTER TABLE public.sz_wide ADD COLUMN c1601 integer")
			szTableColumns(c, false)
			denied("CREATE TABLE public.sz_failed(" + szColumnDDL(1601) + ")")
			c.run(c.conn, fixture.Select(`SELECT c.relname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='sz_failed'`, []string{"relname"}, []string{"NAME"}, nil))
			szTableColumns(c, false)
			c.exec("ALTER TABLE public.sz_wide DROP COLUMN c0001")
			szTableColumns(c, true)
			denied("ALTER TABLE public.sz_wide ADD COLUMN c1601 integer")
			szTableColumns(c, true)
			c.check(c.conn, szQuery, c.want)
		})
	})
	for _, tc := range []struct {
		name     string
		original string
		clipped  string
	}{
		{"ASCII 63", strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{"ASCII 64", strings.Repeat("a", 64), strings.Repeat("a", 63)},
		{"UTF8 63", strings.Repeat("é", 31) + "a", strings.Repeat("é", 31) + "a"},
		{"UTF8 64", strings.Repeat("é", 32), strings.Repeat("é", 31)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			szScenario(t, false, func(c *szCase) {
				count := 0
				if tc.original != tc.clipped {
					count = 1
				}
				for _, prepared := range []bool{false, true} {
					*c.notices = nil
					s := fixture.Select(`SELECT 1 AS "`+tc.original+`"`, []string{tc.clipped}, []string{"INT4"}, [][]any{{int64(1)}})
					s.Prepared = prepared
					c.run(c.conn, s)
					szNotices(c, tc.original, tc.clipped, count)
				}
				*c.notices = nil
				c.exec(`CREATE TABLE public."` + tc.original + `"("` + tc.original + `" integer)`)
				szNotices(c, tc.original, tc.clipped, 2*count)
				c.run(c.conn, fixture.Select(`SELECT c.relname,a.attname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid WHERE n.nspname='public' AND c.relname=$1 AND a.attnum=1`, []string{"relname", "attname"}, []string{"NAME", "NAME"}, [][]any{{tc.clipped, tc.clipped}}, tc.clipped))
				q := `SELECT "` + tc.clipped + `" FROM public."` + tc.clipped + `"`
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(q, []string{tc.clipped}, []string{"INT4"}, nil)
					s.Prepared = prepared
					c.run(c.conn, s)
					s = fixture.Select("SELECT $1::name AS v", []string{"v"}, []string{"NAME"}, [][]any{{tc.clipped}}, tc.original)
					s.Prepared = prepared
					c.run(c.conn, s)
				}
				szNotices(c, "", "", 0)
				c.check(c.conn, szQuery+" WHERE sizing_id=10005", [][]any{{int64(10005), "MAXIMUM IDENTIFIER LENGTH", int64(63), szComment}})
			})
		})
	}
	for _, width := range []int{1664, 1665} {
		t.Run(fmt.Sprintf("SELECT %d targets", width), func(t *testing.T) {
			szScenario(t, false, func(c *szCase) {
				targets, names, types, row := make([]string, width), make([]string, width), make([]string, width), make([]any, width)
				for i := range targets {
					names[i] = fmt.Sprintf("c%d", i+1)
					targets[i], types[i], row[i] = "1 AS "+names[i], "INT4", int64(1)
				}
				q := "SELECT " + strings.Join(targets, ",")
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(q, names, types, [][]any{row})
					if c.b.PG && width == 1665 {
						s = fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("54011", "target lists can have at most 1664 entries"))
						s.SelectRows, s.ErrorStage = true, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
					}
					s.Prepared = prepared
					c.run(c.conn, s)
				}
				value := int64(0)
				if c.b.PG {
					value = 1664
				}
				c.check(c.conn, szQuery+" WHERE sizing_id=100", [][]any{{int64(100), "MAXIMUM COLUMNS IN SELECT", value, nil}})
			})
		})
	}
}
