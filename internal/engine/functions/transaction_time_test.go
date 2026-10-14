package functions

import (
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestTransactionTimeOfDayFormatting(t *testing.T) {
	stamp := time.Date(2000, 1, 1, 12, 0, 0, 123456789, time.UTC)
	ny, err := sqltype.ResolveZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	numeric, _, err := sqltype.ParseSessionZone("5.5")
	if err != nil {
		t.Fatal(err)
	}
	posix, err := sqltype.ResolveZone("+05:30")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		stamp time.Time
		zone  *sqltype.Zone
		want  string
	}{
		{stamp, nil, "Sat Jan 01 12:00:00.123456 2000 UTC"},
		{stamp, ny, "Sat Jan 01 07:00:00.123456 2000 EST"},
		{time.Date(2000, 7, 1, 12, 0, 0, 123456789, time.UTC), ny, "Sat Jul 01 08:00:00.123456 2000 EDT"},
		{stamp, numeric, "Sat Jan 01 17:30:00.123456 2000 +05:30"},
		{stamp, posix, "Sat Jan 01 06:30:00.123456 2000 "},
		{time.Date(1999, 12, 31, 23, 0, 0, 1000, time.UTC), numeric, "Sat Jan 01 04:30:00.000001 2000 +05:30"},
	} {
		if got := transactionTimeOfDayText(item.stamp, item.zone); got != item.want {
			t.Errorf("text=%q want=%q", got, item.want)
		}
	}
}
