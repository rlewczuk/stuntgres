package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestTimezoneReviewQualifiedLiteralNamespace(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_literal_anchor(id INT)", 0)
	timezoneExec(t, c, "INSERT INTO tz_literal_anchor VALUES(1)", 1)
	timezoneExec(t, c, "SET TIME ZONE 'UTC'", 0)
	timezoneExec(t, c, "SET search_path TO missing,public", 0)
	for _, q := range []string{
		"SELECT pg_catalog.timestamptz '2004-07-01 12:00+00'::TEXT AS z,pg_catalog.timetz '05:34:17-05'::TEXT AS q FROM tz_literal_anchor",
		"SELECT pg_catalog.\"timestamptz\" '2004-07-01 12:00+00'::TEXT AS z,pg_catalog.\"timetz\" '05:34:17-05'::TEXT AS q FROM tz_literal_anchor",
	} {
		timezoneRows(t, c, q, []string{"TEXT", "TEXT"}, [][]any{{"2004-07-01 12:00:00+00", "05:34:17-05"}})
		s, err := c.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := s.QueryContext(ctx)
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		timeCheckRows(t, rows, []string{"z", "q"}, []string{"TEXT", "TEXT"}, [][]any{{"2004-07-01 12:00:00+00", "05:34:17-05"}})
		s.Close()
	}
}
