package copyfmt

import (
	"bufio"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCopyFilterOptions(t *testing.T) {
	for _, tc := range []struct {
		suffix    string
		action    ErrorAction
		verbosity LogVerbosity
		limit     int64
	}{
		{"", Stop, LogDefault, 0},
		{" WITH(ON_ERROR 'IgNoRe',LOG_VERBOSITY 'VeRbOsE',REJECT_LIMIT 9223372036854775807)", Ignore, LogVerbose, 9223372036854775807},
		{" WITH(REJECT_LIMIT '+0x10',ON_ERROR ignore,LOG_VERBOSITY silent)", Ignore, LogSilent, 16},
		{" WITH(FORMAT binary,ON_ERROR stop)", Stop, LogDefault, 0},
	} {
		o, err := ParseOptions(copyStatement(t, "COPY t FROM STDIN"+tc.suffix))
		if err != nil || o.OnError != tc.action || o.LogVerbosity != tc.verbosity || o.RejectLimit != tc.limit {
			t.Fatalf("options=%+v err=%v", o, err)
		}
	}
	q := "COPY t FROM STDIN WITH(ON_ERROR ignore,ON_ERROR stop)"
	_, err := ParseOptions(copyStatement(t, q))
	var record *sqlstate.Error
	if !errors.As(err, &record) || record.Code != "42601" || record.Position != strconv.Itoa(strings.LastIndex(q, "ON_ERROR")+1) {
		t.Fatal("duplicate position", err)
	}
	for _, option := range []string{"REJECT_LIMIT 0", "REJECT_LIMIT -1", "REJECT_LIMIT 1.5", "REJECT_LIMIT true", "REJECT_LIMIT 9223372036854775808"} {
		if _, err := ParseOptions(copyStatement(t, "COPY t FROM STDIN WITH(ON_ERROR ignore,"+option+")")); err == nil {
			t.Fatal("accepted invalid limit", option)
		}
	}
}

func TestCopyFilterMarkerBytes(t *testing.T) {
	for _, format := range []string{"text", "csv"} {
		o := csvOptions(t, "COPY t FROM STDIN WITH(FORMAT "+format+",DEFAULT 'D')")
		reader := NewLineReader(o, bufio.NewReader(strings.NewReader("D\n")), []Target{{Name: "v", HasDefault: true}})
		if present, err := reader.ReadLine(); !present || err != nil {
			t.Fatal("marker record", present, err)
		}
		if err := reader.Split(true); err != nil {
			t.Fatal(err)
		}
		fields := reader.Fields()
		if len(fields) != 1 || !fields[0].Default || string(fields[0].Decoded) != "D" || reader.FieldNull(0) {
			t.Fatalf("%s marker=%+v", format, fields)
		}
	}
}
