//go:build reference

package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func newTimestampReferencePair(t *testing.T) *referencePair {
	t.Helper()
	p := newReferencePair(t)
	p.postgres.SetMaxOpenConns(1)
	if _, e := p.postgres.ExecContext(p.ctx, "SET TIME ZONE 'UTC'; SET DateStyle TO 'ISO, MDY'"); e != nil {
		t.Fatal(e)
	}
	return p
}

var timestampReferenceInputs = []struct {
	input string
	want  any
}{
	{"4714-11-23 250000 BC", time.Date(-4713, 11, 24, 1, 0, 0, 0, time.UTC)},
	{"J .1", time.Date(-4713, 11, 24, 2, 24, 0, 0, time.UTC)},
	{"J .", time.Date(-4713, 11, 24, 0, 0, 0, 0, time.UTC)},
	{"1999 January 04:05:06 8.5", time.Date(1999, 1, 8, 4, 5, 6, 500000000, time.UTC)},
	{"January 8 04:05:06 99.1", time.Date(99, 1, 8, 4, 5, 6, 100000000, time.UTC)},
	{"1999-01-08 119999 PM", time.Date(1999, 1, 9, 0, 40, 39, 0, time.UTC)},
	{"PST dst January 8 1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"EDT January 8 1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"epoch BC PM Friday dst", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"- InFiNiTy BC PM Friday dst", "-infinity"},
	{"1999 1 8.5", time.Date(1999, 1, 8, 0, 0, 0, 500000000, time.UTC)},
	{"1999 1.50 8", time.Date(1999, 1, 8, 0, 0, 0, 500000000, time.UTC)},
	{"1999 1.00 8", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999 January 8.5", time.Date(1999, 1, 8, 0, 0, 0, 500000000, time.UTC)},
	{"1999 1 8.5 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999 1 8.", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 250000", time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)},
	{"1999-01-08 2500", time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)},
	{"1999-01-08 250000-8", time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)},
	{"1999-01-08 049900", time.Date(1999, 1, 8, 5, 39, 0, 0, time.UTC)},
	{"1999-01-08 040599", time.Date(1999, 1, 8, 4, 6, 39, 0, time.UTC)},
	{"1999-01-08 250000.5", time.Date(1999, 1, 9, 1, 0, 0, 500000000, time.UTC)},
	{"1999-12-31 250000", time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC)},
	{"J on 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"J UTC 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"J +02 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"J Friday 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"J BC 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"J PM 2451187", time.Date(1999, 1, 8, 12, 0, 0, 0, time.UTC)},
	{"1999-01-08 T on 040506", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 T 040506-8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 PST dst", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 dst PST", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 UTC dst", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 dst UTC", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 +02 dst", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 dst +02", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08 ", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08!", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08!", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"PST 1999-01-08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 04:05:06 + 8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"+ InFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\t8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"+\tInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\n8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"+\nInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\r8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"+\rInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\f8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"+\fInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 +\u000b8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"+\u000bInFiNiTy", "infinity"},
	{"1999-01-08 04:05:06 - 8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"- InFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\t8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"-\tInFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\n8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"-\nInFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\r8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"-\rInFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\f8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"-\fInFiNiTy", "-infinity"},
	{"1999-01-08 04:05:06 -\u000b8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"-\u000bInFiNiTy", "-infinity"},
	{"January 8 04:05:06 120000 PST", time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999 01 8.1", time.Date(1999, 1, 8, 0, 0, 0, 100000000, time.UTC)},
	{"1999 January 8.1", time.Date(1999, 1, 8, 0, 0, 0, 100000000, time.UTC)},
	{"January 8 99.1", time.Date(99, 1, 8, 0, 0, 0, 100000000, time.UTC)},
	{"on epoch", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"epoch at", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"on infinity", "infinity"},
	{"infinity at", "infinity"},
	{"on +InFiNiTy", "infinity"},
	{"+InFiNiTy at", "infinity"},
	{"on - InFiNiTy", "-infinity"},
	{"- InFiNiTy at", "-infinity"},
	{"1999 8 JANUARY", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999 8 JaNuArY 04:05:06 WEDS", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-JaN-08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 04:05:06 aMeRiCa/nEw_yOrK", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"BC JaNuArY 1 0001", time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"PM JaNuArY 8 1999 04:05:06", time.Date(1999, 1, 8, 16, 5, 6, 0, time.UTC)},
	{"On JaNuArY 8 1999 At 04:05:06 pM aD PsT", time.Date(1999, 1, 8, 16, 5, 6, 0, time.UTC)},
	{"0001-01-08 04:05:06 bC", time.Date(0, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"JuLiAn2451187.1", time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC)},
	{"EpOcH", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 aLlBaLlS", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999.01.08.", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01--08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999/01//08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999.01..08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"8 January 1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-Jan08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999 1 January", time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08-", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999/01/08/", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999--01--08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999//01//08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999 8 January", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999 31 January", time.Date(1999, 1, 31, 0, 0, 0, 0, time.UTC)},
	{"2004-10-19 10:23:54+15:59:59", time.Date(2004, 10, 19, 10, 23, 54, 0, time.UTC)},
	{"2004-10-19 10:23:54-15:59:59", time.Date(2004, 10, 19, 10, 23, 54, 0, time.UTC)},
	{"January 08-1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"Jan 08/1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"January 1999 08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"UTC 1999-01-08 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"BC January 1 1", time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"1999.366", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"1999 366", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"1999.366 04:05:06", time.Date(2000, 1, 1, 4, 5, 6, 0, time.UTC)},
	{"1999 366 04:05:06", time.Date(2000, 1, 1, 4, 5, 6, 0, time.UTC)},
	{"2000.366", time.Date(2000, 12, 31, 0, 0, 0, 0, time.UTC)},
	{"2000 366", time.Date(2000, 12, 31, 0, 0, 0, 0, time.UTC)},
	{"2000.366 04:05:06", time.Date(2000, 12, 31, 4, 5, 6, 0, time.UTC)},
	{"1999 01-08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"Friday 19990108 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"BC January 1 0001", time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"PM January 8 1999 04:05:06", time.Date(1999, 1, 8, 16, 5, 6, 0, time.UTC)},
	{"1999-01-08 Friday 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999.008 Friday 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 weds", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"J2451187.1", time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC)},
	{"J2451187.2", time.Date(1999, 1, 8, 4, 48, 0, 0, time.UTC)},
	{"J2451187.000001", time.Date(1999, 1, 8, 0, 0, 0, 86400000, time.UTC)},
	{"J2451187.0", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"J2451187.1 UTC", time.Date(1999, 1, 8, 2, 24, 0, 0, time.UTC)},
	{"J2451187.0 UTC", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"J2451187 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"04:05:06 J2451187", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"J2451187-8", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 04:05:06.", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05.", time.Date(1999, 1, 8, 0, 4, 5, 0, time.UTC)},
	{"1999-01-08 allballs", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 04:05:06.1e2", time.Date(1999, 1, 8, 4, 5, 6, 100000000, time.UTC)},
	{"1999-01-08 04:05:06 e2d", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 AB0CD1", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 A0:00:60", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 FOO/BAR0", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999.008 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999.008 allballs", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 040506-8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"19990108T040506-8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"JD2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 04:05:06 - 8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"                                                                                                                                1999-01-08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"January 8, 1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"January 8 1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-Jan-08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"Jan-08-1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"08-Jan-1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"8 Jan 1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1/8/1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1-8-1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1.8.1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"08-Jan-99", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"Jan-08-99", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"19990108", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"990108", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999.008", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999 008", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"J2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1/18/1999", time.Date(1999, 1, 18, 0, 0, 0, 0, time.UTC)},
	{"01/02/03", time.Date(2003, 1, 2, 0, 0, 0, 0, time.UTC)},
	{"01/02/69", time.Date(2069, 1, 2, 0, 0, 0, 0, time.UTC)},
	{"01/02/70", time.Date(1970, 1, 2, 0, 0, 0, 0, time.UTC)},
	{"0099-01-08", time.Date(99, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"J2451187.5", time.Date(1999, 1, 8, 12, 0, 0, 0, time.UTC)},
	{"1999-01-08 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08T04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"19990108T040506", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"990108 040506", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"January 8 04:05:06 1999 PST", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"Friday January 8 1999 at 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"on Fri 8 January 1999 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05", time.Date(1999, 1, 8, 4, 5, 0, 0, time.UTC)},
	{"1999-01-08 0405", time.Date(1999, 1, 8, 4, 5, 0, 0, time.UTC)},
	{"1999-01-08 040506.789", time.Date(1999, 1, 8, 4, 5, 6, 789000000, time.UTC)},
	{"1999-01-08 04:05 AM", time.Date(1999, 1, 8, 4, 5, 0, 0, time.UTC)},
	{"1999-01-08 04:05 PM", time.Date(1999, 1, 8, 16, 5, 0, 0, time.UTC)},
	{"1999-01-08 12:00 AM", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 12:00 PM", time.Date(1999, 1, 8, 12, 0, 0, 0, time.UTC)},
	{"1999-01-08 04:05:06.1", time.Date(1999, 1, 8, 4, 5, 6, 100000000, time.UTC)},
	{"1999-01-08 04:05:06.1234564", time.Date(1999, 1, 8, 4, 5, 6, 123456000, time.UTC)},
	{"1999-01-08 04:05:06.1234566", time.Date(1999, 1, 8, 4, 5, 6, 123457000, time.UTC)},
	{"1999-01-08 04:05:06-8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06-800", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06-08:00", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06-8:00:00", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06+0730", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06+07:30:00", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06+02", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06Z", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 zulu", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 UTC", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 GMT", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 PST", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 EDT", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 MSK", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 America/New_York", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 aMeRiCa/NeW_yOrK", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 US/Eastern", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 Etc/UTC", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 PST8PDT", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 FOOBAR0", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 FOO-2:30BAR", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 FOO+2BAR3", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"January 8, 99 BC 04:05:06", time.Date(-98, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"0001-01-01 00:00:00 BC", time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"1999-01-08 04:05:06 AD PST", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"1999-01-08 04:05:06 PST BC", time.Date(-1998, 1, 8, 4, 5, 6, 0, time.UTC)},
	{"2000-02-29 24:00:00", time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC)},
	{"1999-12-31 23:59:60", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"2024-03-10 02:30 America/New_York", time.Date(2024, 3, 10, 2, 30, 0, 0, time.UTC)},
	{"2024-11-03 01:30 America/New_York", time.Date(2024, 11, 3, 1, 30, 0, 0, time.UTC)},
	{"epoch", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
	{"INFINITY", "infinity"},
	{"-Infinity", "-infinity"},
	{"4714-11-24 BC", time.Date(-4713, 11, 24, 0, 0, 0, 0, time.UTC)},
	{"294276-12-31 23:59:59.999999", time.Date(294276, 12, 31, 23, 59, 59, 999999000, time.UTC)},
}

func TestReferenceTimestampDocumentedInputs(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE anchor(n INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO anchor VALUES(1)", 1))
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,v TIMESTAMP)", 0))
	for i, tc := range timestampReferenceInputs {
		t.Run(tc.input, func(t *testing.T) {
			literal := referenceSelect("SELECT TIMESTAMP WITHOUT TIME ZONE '"+tc.input+"' FROM anchor", []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{tc.want}})
			p.step(t, literal)
			p.step(t, referencePrepared(literal))
			insert := referenceExec("INSERT INTO t VALUES($1,$2)", 1, i+1, tc.input)
			p.step(t, insert)
			p.step(t, referencePrepared(insert))
			p.step(t, referencePrepared(referenceSelect("SELECT v FROM t WHERE id=$1", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{tc.want}, {tc.want}}, i+1)))
		})
	}
}
func TestReferenceTimestampPrecisionAndCivilParameters(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE anchor(n INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO anchor VALUES(1)", 1))
	for prec, ns := range []int{0, 100000000, 120000000, 123000000, 123500000, 123460000, 123456000} {
		table := fmt.Sprintf("p%d", prec)
		p.step(t, referencePrepared(referenceExec(fmt.Sprintf("CREATE TABLE %s(v TIMESTAMP(%d) WITHOUT TIME ZONE)", table, prec), 0)))
		p.step(t, referencePrepared(referenceExec("INSERT INTO "+table+" VALUES($1)", 1, "2000-01-01 00:00:00.123456")))
		want := time.Date(2000, 1, 1, 0, 0, 0, ns, time.UTC)
		p.step(t, referenceSelect("SELECT v FROM "+table, []string{"v"}, []string{"TIMESTAMP"}, [][]any{{want}}))
		p.step(t, referencePrepared(referenceSelect(fmt.Sprintf("SELECT TIMESTAMP(%d) '2000-01-01 00:00:00.123456' FROM anchor", prec), []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{want}})))
	}
	p.step(t, referenceExec("CREATE TABLE sized(v TIMESTAMP(2))", 0))
	p.step(t, referenceExec("INSERT INTO sized VALUES('2000-01-01 00:00:00.1245'),(TIMESTAMP(3) '2000-01-01 00:00:00.1245')", 2))
	p.step(t, referenceSelect("SELECT v FROM sized ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 120000000, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 130000000, time.UTC)}}))
	for _, tc := range []struct {
		q    string
		want time.Time
	}{
		{"TIMESTAMP(0) '2000-01-01 00:00:00.5'", time.Date(2000, 1, 1, 0, 0, 1, 0, time.UTC)},
		{"TIMESTAMP(0) '1999-12-31 23:59:59.5'", time.Date(1999, 12, 31, 23, 59, 59, 0, time.UTC)},
		{"TIMESTAMP(3) '1999-12-31 23:59:59.9996'", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"TIMESTAMP '2000-01-01 00:00:00.0000005'", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"TIMESTAMP '2000-01-01 00:00:00.0000015'", time.Date(2000, 1, 1, 0, 0, 0, 2000, time.UTC)},
		{"TIMESTAMP(0) '294276-12-31 23:59:59.999999'", time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		p.step(t, referencePrepared(referenceSelect("SELECT "+tc.q+" FROM anchor", []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{tc.want}})))
	}
	p.step(t, referenceExec("CREATE TABLE params(id INTEGER,v TIMESTAMP(6))", 0))
	for i, year := range []int{0, -98, 2004, 12000} {
		raw := time.Date(year, 10, 19, 10, 23, 54, 123456600, time.FixedZone("east", 7200))
		p.step(t, referencePrepared(referenceExec("INSERT INTO params VALUES($1,$2)", 1, i+1, raw)))
		p.step(t, referenceSelect("SELECT v FROM params WHERE id=$1", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(year, 10, 19, 10, 23, 54, 123457000, time.UTC)}}, i+1))
	}
	for i, raw := range []any{[]byte("infinity"), "-infinity", nil} {
		p.step(t, referencePrepared(referenceExec("INSERT INTO params VALUES($1,$2)", 1, i+5, raw)))
	}
	p.step(t, referenceSelect("SELECT v FROM params WHERE id=5", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{"infinity"}}))
	p.step(t, referenceSelect("SELECT v FROM params WHERE id=6", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{"-infinity"}}))
	p.step(t, referenceSelect("SELECT v FROM params WHERE id=7", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{nil}}))
	// Assert zero offset and Scan(*time.Time) independently, before parity normalization.
	for _, db := range []referenceExecutor{p.memory, p.postgres} {
		rows, e := db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), "SELECT v FROM params WHERE id=3")
		if e != nil {
			t.Fatal(e)
		}
		if !rows.Next() {
			t.Fatal("missing finite")
		}
		var got time.Time
		if e = rows.Scan(&got); e != nil {
			t.Fatal(e)
		}
		rows.Close()
		_, off := got.Zone()
		if off != 0 || got.Hour() != 10 || got.Nanosecond() != 123457000 {
			t.Fatal(got)
		}
	}
}
func TestReferenceTimestampQuotedUnicodeTypesAndKeywordIdentifiers(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referencePrepared(referenceExec(`CREATE TABLE timestamp(without "timestamp"(3),time U&"time\0073tamp"(3),zone TIMESTAMP,timestamp TIMESTAMP)`, 0)))
	p.step(t, referencePrepared(referenceExec("INSERT INTO timestamp VALUES($1,$1,'epoch','epoch')", 1, "2000-01-01 00:00:00.1236")))
	want := [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 124000000, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 124000000, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}
	query := referenceSelect("SELECT without,time,zone,timestamp FROM timestamp", []string{"without", "time", "zone", "timestamp"}, []string{"TIMESTAMP", "TIMESTAMP", "TIMESTAMP", "TIMESTAMP"}, want)
	p.step(t, query)
	p.step(t, referencePrepared(query))
}

func TestReferenceTimestampOrdinalBoundaryOrdering(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,v TIMESTAMP)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'2000-01-01'),(2,TIMESTAMP '1999.366'),(3,'1999.365'),(4,'2000 366')", 4))
	p.step(t, referenceSelect("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{
		{time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC)},
		{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2000, 12, 31, 0, 0, 0, 0, time.UTC)},
	}))
	equal := referenceSelect("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, "1999.366")
	p.step(t, equal)
	p.step(t, referencePrepared(equal))
}
