package reference_test

import (
	"encoding/binary"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

// NumericShort and NumericLong are both released physical forms; neither is
// numeric_send's network ndigits/weight/sign/dscale header.
func mcvNumeric(t *testing.T, p []byte) (float64, int, uint16) {
	t.Helper()
	if len(p) < 2 {
		t.Fatal("numeric header")
	}
	h := binary.LittleEndian.Uint16(p)
	sign := h & 0xc000
	if sign == 0xc000 {
		return math.NaN(), 0, h
	}
	scale, weight, start := 0, 0, 2
	if sign == 0x8000 {
		scale = int((h & 0x1f80) >> 7)
		weight = int(h & 0x3f)
		if h&0x40 != 0 {
			weight -= 64
		}
		sign = 0
		if h&0x2000 != 0 {
			sign = 0x4000
		}
	} else {
		if len(p) < 4 {
			t.Fatal("numeric long header")
		}
		scale = int(h & 0x3fff)
		weight = int(int16(binary.LittleEndian.Uint16(p[2:])))
		start = 4
	}
	value := 0.0
	for i := start; i < len(p); i += 2 {
		if i+2 > len(p) {
			t.Fatal("numeric digit")
		}
		digit := binary.LittleEndian.Uint16(p[i:])
		if digit >= 10000 {
			t.Fatal("numeric base", digit)
		}
		value += float64(digit) * math.Pow(10000, float64(weight-(i-start)/2))
	}
	if sign == 0x4000 {
		value = -value
	}
	return value, scale, h
}
func TestReferenceStatisticsMCVStructured(t *testing.T) {
	for _, c := range []struct {
		literal string
		number  float64
		scale   int
	}{{"12345.6700", 12345.67, 4}, {"-0.00001230", -.0000123, 8}, {"0.00", 0, 2}, {"1e70", 1e70, 0}, {"'NaN'", math.NaN(), 0}, {"'Infinity'", math.Inf(1), 0}, {"'-Infinity'", math.Inf(-1), 0}} {
		t.Run(c.literal, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				s.run(fixture.Exec("CREATE TABLE mcv_n(a NUMERIC,b INT)", 0))
				s.run(fixture.Exec("INSERT INTO mcv_n VALUES("+c.literal+",1)", 1))
				s.run(fixture.Exec("CREATE STATISTICS mcv_ns(mcv) ON a,b FROM mcv_n", 0))
				s.run(fixture.Exec("ANALYZE mcv_n", 0))
				_, v := readMCV(t, s, "mcv_ns", false)
				n, scale, h := mcvNumeric(t, v.items[0].values[0])
				if math.IsNaN(c.number) {
					if h != 0xc000 {
						t.Fatal(h)
					}
				} else if math.IsInf(c.number, 1) {
					if h != 0xd000 {
						t.Fatal(h)
					}
				} else if math.IsInf(c.number, -1) {
					if h != 0xf000 {
						t.Fatal(h)
					}
				} else if math.Abs(n-c.number) > math.Abs(c.number)*1e-14 || scale != c.scale {
					t.Fatal(n, scale, c)
				}
			})
		})
	}
	t.Run("jsonb", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			s.run(fixture.Exec("CREATE TABLE mcv_j(a JSONB,b INT)", 0))
			s.run(fixture.Exec(`INSERT INTO mcv_j VALUES('{"x": [true, null, 12.50], "long": {"a": false}}',1)`, 1))
			s.run(fixture.Exec("CREATE STATISTICS mcv_js(mcv) ON a,b FROM mcv_j", 0))
			s.run(fixture.Exec("ANALYZE mcv_j", 0))
			_, v := readMCV(t, s, "mcv_js", false)
			mcvJSONContainer(t, v.items[0].values[0])
			p := v.items[0].values[0]
			if binary.LittleEndian.Uint32(p) != 0x20000002 {
				t.Fatal("JSONB object header", p)
			}
			if string(p[20:25]) != "xlong" {
				t.Fatal("JSONB canonical keys", p)
			}
		})
	})
	t.Run("domain-enum", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			for _, q := range []string{"CREATE TYPE mcv_e AS ENUM('z','a')", "CREATE DOMAIN mcv_d AS mcv_e", "CREATE TABLE mcv_e_t(a mcv_d,b INT)", "CREATE STATISTICS mcv_es(mcv) ON a,b FROM mcv_e_t"} {
				s.run(fixture.Exec(q, 0))
			}
			s.run(fixture.Exec("INSERT INTO mcv_e_t VALUES('z',1),('z',1),('a',2)", 3))
			s.run(fixture.Exec("ANALYZE mcv_e_t", 0))
			_, v := readMCV(t, s, "mcv_es", false)
			var domain, member int64
			if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT oid::bigint FROM pg_type WHERE typname='mcv_d'").Scan(&domain); err != nil {
				t.Fatal(err)
			}
			if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT oid::bigint FROM pg_enum WHERE enumtypid='mcv_e'::regtype AND enumlabel='z'").Scan(&member); err != nil {
				t.Fatal(err)
			}
			if v.dimensions[0].oid != uint32(domain) || binary.LittleEndian.Uint32(v.items[0].values[0]) != uint32(member) || v.items[0].frequency != 2.0/3 {
				t.Fatal(v)
			}
		})
	})
	t.Run("arrays", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			// Native admission needs exact metadata; PostgreSQL already has a
			// complete polymorphic array class. An incomplete exact class would
			// shadow that usable provider, so setup follows real catalog authority.
			if !s.pg {
				s.run(fixture.Exec("CREATE OPERATOR CLASS mcv_arr_ops DEFAULT FOR TYPE int[] USING btree AS STORAGE int[]", 0))
			}
			s.run(fixture.Exec("CREATE TABLE mcv_arr(a INT[],b INT)", 0))
			s.run(fixture.Exec("CREATE STATISTICS mcv_as(mcv) ON a,b FROM mcv_arr", 0))
			for _, literal := range []string{"'{}'", "'[0:2]={1,NULL,3}'", "'{{1,2},{3,4}}'"} {
				s.run(fixture.Exec("DELETE FROM mcv_arr", 0))
				s.run(fixture.Exec("INSERT INTO mcv_arr VALUES("+literal+",1)", 1))
				s.run(fixture.Exec("ANALYZE mcv_arr", 0))
				_, v := readMCV(t, s, "mcv_as", false)
				p := v.items[0].values[0]
				ndim := int(binary.LittleEndian.Uint32(p))
				offset := int(binary.LittleEndian.Uint32(p[4:]))
				if binary.LittleEndian.Uint32(p[8:]) != 23 {
					t.Fatal("array element identity", p)
				}
				switch literal {
				case "'{}'":
					if ndim != 0 || offset != 0 || len(p) != 12 {
						t.Fatal(p)
					}
				case "'[0:2]={1,NULL,3}'":
					if ndim != 1 || offset != 32 || binary.LittleEndian.Uint32(p[12:]) != 3 || binary.LittleEndian.Uint32(p[16:]) != 0 || p[20]&7 != 5 || binary.LittleEndian.Uint32(p[28:]) != 1 || binary.LittleEndian.Uint32(p[32:]) != 3 {
						t.Fatal("array bitmap/bounds/native offset", p)
					}
				default:
					if ndim != 2 || offset != 0 || binary.LittleEndian.Uint32(p[12:]) != 2 || binary.LittleEndian.Uint32(p[16:]) != 2 {
						t.Fatal(p)
					}
				}
				s.run(fixture.Exec("DELETE FROM mcv_arr", 1))
			}
		})
	})
	for _, extension := range []string{"cube", "hstore"} {
		t.Run(extension, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				s.run(fixture.Exec("CREATE EXTENSION "+extension, 0))
				s.run(fixture.Exec("CREATE TABLE mcv_ext(a "+extension+",b INT)", 0))
				s.run(fixture.Exec("CREATE STATISTICS mcv_xs(mcv) ON a,b FROM mcv_ext", 0))
				literal := "'(1,2)'"
				if extension == "hstore" {
					literal = `'"a"=>NULL,"b"=>""'`
				}
				s.run(fixture.Exec("INSERT INTO mcv_ext VALUES("+literal+",1)", 1))
				s.run(fixture.Exec("ANALYZE mcv_ext", 0))
				_, v := readMCV(t, s, "mcv_xs", false)
				p := v.items[0].values[0]
				h := binary.LittleEndian.Uint32(p)
				if extension == "cube" {
					if h != 0x80000002 || len(p) != 20 || math.Float64frombits(binary.LittleEndian.Uint64(p[4:])) != 1 || math.Float64frombits(binary.LittleEndian.Uint64(p[12:])) != 2 {
						t.Fatal(p)
					}
				} else if h != 0x80000002 || len(p) != 22 || binary.LittleEndian.Uint32(p[8:])&0x40000000 == 0 || string(p[20:]) != "ab" {
					t.Fatal(p)
				}
			})
		})
	}
}

// Recursively check JEntry lengths/offsets and native aligned numeric/container
// payloads. Types and container flags, not JSON textual output, drive parsing.
func mcvJSONContainer(t *testing.T, p []byte) {
	t.Helper()
	if len(p) < 4 {
		t.Fatal("JSONB container")
	}
	h := binary.LittleEndian.Uint32(p)
	n := int(h & 0x0fffffff)
	if h&0x20000000 != 0 {
		n *= 2
	}
	base := 4 + 4*n
	if base > len(p) {
		t.Fatal("JEntry extent")
	}
	start := 0
	for i := range n {
		e := binary.LittleEndian.Uint32(p[4+4*i:])
		end := int(e & 0x0fffffff)
		if e&0x80000000 == 0 {
			end += start
		}
		if end < start || base+end > len(p) {
			t.Fatal("JEntry bounds")
		}
		kind := e & 0x70000000
		switch kind {
		case 0:
		case 0x10000000:
			aligned := (base + start + 3) &^ 3
			body := p[aligned : base+end]
			if len(body) < 4 {
				t.Fatal("JSONB numeric")
			}
			size := int(binary.LittleEndian.Uint32(body) >> 2)
			if size != len(body) {
				t.Fatal("numeric varlena", size, len(body))
			}
			mcvNumeric(t, body[4:])
		case 0x20000000, 0x30000000, 0x40000000:
			if start != end {
				t.Fatal("JSONB scalar bytes")
			}
		case 0x50000000:
			aligned := (base + start + 3) &^ 3
			mcvJSONContainer(t, p[aligned:base+end])
		default:
			t.Fatal("JEntry type", e)
		}
		start = end
	}
	if base+start != len(p) {
		t.Fatal("JSONB trailing bytes")
	}
}
