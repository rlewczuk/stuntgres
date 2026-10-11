package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

// Independent constants from PostgreSQL REL_18_0 dbsize regression output and
// exact binary arithmetic; no expected result is obtained from either backend.
func TestReferenceSizeBytes(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		vectors := []struct {
			input string
			want  int64
		}{
			{"1", 1}, {"1 bytes", 1}, {"1 B", 1}, {"1 kB", 1024}, {"1 MB", 1048576},
			{"1 GB", 1073741824}, {"1 TB", 1099511627776}, {"1 PB", 1125899906842624},
			{"1.5GB", 1610612736}, {"1e6 MB", 1048576000000}, {"99PB", 111464090777419776},
			{"+1 mb", 1048576}, {"1 ByTeS", 1}, {"1 Kb", 1024}, {"1 gB", 1073741824},
			{"1 tB", 1099511627776}, {"1 pB", 1125899906842624}, {"1 b", 1},
			{".5", 1}, {"-.5", -1}, {".499999999999", 0}, {"-.499999999999", 0},
			{"1.", 1}, {"-.1kb", -102}, {"-.1", 0}, {".00048828125kB", 1},
			{"-.00048828125kB", -1}, {".000488281249999kB", 0}, {"-.000488281249999kB", 0},
			{"9007199254740993 bytes", 9007199254740993},
			{"9223372036854775807.49", 9223372036854775807},
			{"-9223372036854775808.49", -9223372036854775808}, {"-8192PB", -9223372036854775808},
			{"1e-3 kB", 1}, {"-1e-3 kB", -1}, {"0PB", 0},
		}
		for _, ws := range []string{" ", "\t", "\n", "\r", "\f", "\v"} {
			vectors = append(vectors, struct {
				input string
				want  int64
			}{ws + "1" + ws + "MB" + ws, 1048576})
		}
		for _, v := range vectors {
			t.Run(v.input, func(t *testing.T) {
				sizeBoth(t, p, b, c, fixture.Select("SELECT pg_size_bytes($1)", []string{"pg_size_bytes"}, []string{"INT8"}, [][]any{{v.want}}, v.input), false)
			})
		}
		sizeBoth(t, p, b, c, fixture.Select("SELECT pg_size_bytes('1 MB')", []string{"pg_size_bytes"}, []string{"INT8"}, [][]any{{int64(1048576)}}), false)
	})
}

func sizePrettyVector(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, input, typ, want string) {
	t.Helper()
	sizeBoth(t, p, b, c, fixture.Select("SELECT pg_size_pretty($1::"+typ+")", []string{"pg_size_pretty"}, []string{"TEXT"}, [][]any{{want}}, input), false)
}

func TestReferenceSizePrettyBigint(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, v := range []struct {
			n    int64
			want string
		}{
			{0, "0 bytes"}, {1024, "1024 bytes"}, {10239, "10239 bytes"}, {10240, "10 kB"},
			{10485247, "10239 kB"}, {10485248, "10 MB"}, {10736893951, "10239 MB"}, {10736893952, "10 GB"},
			{10994579406847, "10239 GB"}, {10994579406848, "10 TB"}, {11258449312612351, "10239 TB"}, {11258449312612352, "10 PB"},
			{10751, "10 kB"}, {10752, "11 kB"}, {1000000, "977 kB"}, {1000000000, "954 MB"},
			{1000000000000, "931 GB"}, {1000000000000000, "909 TB"}, {9223372036854775807, "8192 PB"},
		} {
			t.Run(strconv.FormatInt(v.n, 10), func(t *testing.T) {
				sizePrettyVector(t, p, b, c, strconv.FormatInt(v.n, 10), "bigint", v.want)
				if v.n != 0 {
					sizePrettyVector(t, p, b, c, strconv.FormatInt(-v.n, 10), "bigint", "-"+v.want)
				}
			})
		}
		sizePrettyVector(t, p, b, c, "-9223372036854775808", "bigint", "-8192 PB")
	})
}

func TestReferenceSizePrettyNumeric(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, v := range []struct{ input, want string }{
			{"10.5000", "10.5000 bytes"}, {"0.000", "0.000 bytes"},
			{"0.00000000000000000001", "0.00000000000000000001 bytes"}, {"-0.00000000000000000001", "-0.00000000000000000001 bytes"},
			{"10239.9999", "10239.9999 bytes"}, {"10240.0000", "10 kB"},
			{"10751.9999", "10 kB"}, {"10752.0000", "11 kB"},
			{"10485247.9999", "10239 kB"}, {"10485248", "10 MB"}, {"10736893951", "10239 MB"}, {"10736893952", "10 GB"},
			{"10994579406847", "10239 GB"}, {"10994579406848", "10 TB"}, {"11258449312612351", "10239 TB"}, {"11258449312612352", "10 PB"},
			{"11528652096115048447", "10239 PB"}, {"11528652096115048448", "10240 PB"},
			{"9007199254740993", "8192 TB"}, {"NaN", "NaN PB"}, {"Infinity", "Infinity PB"}, {"-Infinity", "-Infinity PB"},
		} {
			t.Run(v.input, func(t *testing.T) { sizePrettyVector(t, p, b, c, v.input, "numeric", v.want) })
			if v.input[0] >= '1' && v.input[0] <= '9' {
				t.Run("-"+v.input, func(t *testing.T) { sizePrettyVector(t, p, b, c, "-"+v.input, "numeric", "-"+v.want) })
			}
		}
		sizeBoth(t, p, b, c, fixture.Select("SELECT pg_size_pretty(10.5000::numeric(8,4)),pg_size_pretty(1.25)", []string{"pg_size_pretty", "pg_size_pretty"}, []string{"TEXT", "TEXT"}, [][]any{{"10.5000 bytes", "1.25 bytes"}}), false)
	})
}
