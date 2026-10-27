package enginetests

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func hstoreImageHash(t *testing.T) (sqltype.PartitionHash, *sqltype.Descriptor) {
	t.Helper()
	var bytea *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Bytea {
			bytea = d
		}
	}
	class, ok := sqltype.DefaultHashIndexClass(bytea)
	if !ok {
		t.Fatal("missing bytea hash class")
	}
	class = class.WithSupport(2, sqltype.IndexSupport{Family: class.FamilyOID(), Member: 1, Routine: 6414, Left: uint32(sqltype.Bytea), Right: uint32(sqltype.Bytea)})
	p, err := sqltype.BindPartitionHash(bytea.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	return p, bytea
}

func TestHstoreComparisonHash(t *testing.T) {
	ctx := context.Background()
	d := hstoreUnitType(t, 65000)
	primitive, bytea := hstoreImageHash(t)
	seeds := []uint64{0, 1, ^uint64(0), 4294967296}
	// Literal images and integer vectors independently specified from REL_18_6;
	// the root observed these vectors against the pinned PostgreSQL oracle.
	for _, tc := range []struct {
		text, image string
		low         int32
		want        [4]int64
	}{
		{"", "00000080", -1524351049, [4]int64{1977219185673256887, 3044001184559810835, 6110074166949006181, -893720521979811119}},
		{`""=>NULL`, "010000800000008000000040", 1561063391, [4]int64{-2878332216582344737, -8859877389916340727, 349300310550366115, 2947191734221126799}},
		{`""=>""`, "010000800000008000000000", 1462368681, [4]int64{-7108022843074807383, -5986516051496788269, 2884702024890064648, 3683723839779953221}},
		{"a=>bc", "010000800100008003000000616263", -1685126002, [4]int64{2385238037795636366, 1277871365356202019, -8238432420750837198, 123372787139268927}},
		{"ab=>c", "010000800200008003000000616263", -1038475849, [4]int64{-6485611737115910729, -2922402731808865909, 1679162986316688136, -3520827279571124536}},
		{"a=>b,c=>d", "020000800100008002000000030000000400000061626364", 245248040, [4]int64{-7179599273143685080, -6303129113984916754, -386666045449919255, 8318128998519414986}},
		{"a=>é", "01000080010000800300000061c3a9", 13266073, [4]int64{-6254575734291272551, -8027660884482757422, -5527249767148096311, 2393370504018584747}},
	} {
		v := hstoreComparisonValue(t, d, tc.text)
		image, err := hex.DecodeString(tc.image)
		if err != nil {
			t.Fatal(err)
		}
		for i, seed := range seeds {
			got, err := sqltype.HstoreHash(ctx, v, seed)
			if err != nil || int64(got) != tc.want[i] {
				t.Fatalf("%q seed %d: %d/%v want %d", tc.text, seed, int64(got), err, tc.want[i])
			}
			if seed == 0 && int32(uint32(got)) != tc.low {
				t.Fatalf("%q low32 %d want %d", tc.text, int32(uint32(got)), tc.low)
			}
			imageValue, err := sqltype.Binary(bytea, string(image))
			if err != nil {
				t.Fatal(err)
			}
			fromImage, err := primitive.Hash(ctx, imageValue, seed)
			if err != nil || fromImage != got {
				t.Fatalf("%q image hash %d/%v != %d", tc.text, fromImage, err, got)
			}
		}
	}
	// Exercise every remainder around several 12-byte mixing boundaries. This
	// independently creates a single-entry image, not a production formatter.
	for n := range 37 {
		text := strings.Repeat("x", n)
		v := hstoreComparisonValue(t, d, `a=>"`+text+`"`)
		image := make([]byte, 13+n)
		binary.LittleEndian.PutUint32(image, 0x80000001)
		binary.LittleEndian.PutUint32(image[4:], 0x80000001)
		binary.LittleEndian.PutUint32(image[8:], uint32(n+1))
		image[12] = 'a'
		copy(image[13:], text)
		for _, seed := range seeds {
			imageValue, err := sqltype.Binary(bytea, string(image))
			if err != nil {
				t.Fatal(err)
			}
			want, err := primitive.Hash(ctx, imageValue, seed)
			if err != nil {
				t.Fatal(err)
			}
			got, err := sqltype.HstoreHash(ctx, v, seed)
			if err != nil || got != want {
				t.Fatalf("length %d seed %d: %d/%v want %d", n, seed, got, err, want)
			}
		}
	}
	a := hstoreComparisonValue(t, d, "b=>2,a=>1")
	b := hstoreComparisonValue(t, d, `"a"=>"1", "b"=>"2"`)
	for _, seed := range seeds {
		ha, err := sqltype.HstoreHash(ctx, a, seed)
		if err != nil {
			t.Fatal(err)
		}
		hb, err := sqltype.HstoreHash(ctx, b, seed)
		if err != nil || ha != hb {
			t.Fatalf("normalized hashes %d/%d: %v", ha, hb, err)
		}
	}
	foreign, err := sqltype.Binary(bytea, "a=>b")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []sqltype.Value{{}, sqltype.Null(d), foreign} {
		if _, err := sqltype.HstoreHash(ctx, v, 0); err == nil {
			t.Fatal("invalid hash carrier admitted")
		}
	}
	long := hstoreComparisonValue(t, d, "a=>"+strings.Repeat("x", 16384))
	if allocs := testing.AllocsPerRun(100, func() {
		if _, err := sqltype.HstoreHash(ctx, long, 4294967296); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatalf("hash allocated %g times", allocs)
	}
	base, cancel := context.WithCancel(ctx)
	defer cancel()
	reached := &hstoreCancelContext{Context: base, cancel: cancel, remaining: 8}
	if _, err := sqltype.HstoreHash(reached, long, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("reached hash cancellation: %v", err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := sqltype.HstoreHash(canceled, hstoreComparisonValue(t, d, ""), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("empty hash cancellation: %v", err)
	}
}
