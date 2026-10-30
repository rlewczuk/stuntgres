package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently evaluated REL_18_6 hstore.h canonical little-endian images,
// hashfn.c hash_bytes/hash_bytes_extended. The image excludes the varlena
// header, includes count/NEWVERSION and HEntry flags, and uses UTF8 bytes.
// These constants require pinned-runtime confirmation; they are not observations.
var hstoreComparisonHashVectors = []struct {
	input, image string
	hash         int64
	extended     [4]int64 // seeds 0, 1, -1, 4294967296
}{
	{"", "00000080", -1524351049, [4]int64{1977219185673256887, 3044001184559810835, 6110074166949006181, -893720521979811119}},
	{`""=>NULL`, "010000800000008000000040", 1561063391, [4]int64{-2878332216582344737, -8859877389916340727, 349300310550366115, 2947191734221126799}},
	{`""=>""`, "010000800000008000000000", 1462368681, [4]int64{-7108022843074807383, -5986516051496788269, 2884702024890064648, 3683723839779953221}},
	{"a=>bc", "010000800100008003000000616263", -1685126002, [4]int64{2385238037795636366, 1277871365356202019, -8238432420750837198, 123372787139268927}},
	{"ab=>c", "010000800200008003000000616263", -1038475849, [4]int64{-6485611737115910729, -2922402731808865909, 1679162986316688136, -3520827279571124536}},
	{"a=>bcd", "01000080010000800400000061626364", -923142393, [4]int64{-8938261616064334073, 5881884604779711907, 329780416925072867, -2414349010255158971}},
	{"a=>b,c=>d", "020000800100008002000000030000000400000061626364", 245248040, [4]int64{-7179599273143685080, -6303129113984916754, -386666045449919255, 8318128998519414986}},
	{"a=>NULL", "01000080010000800100004061", -148809510, [4]int64{3084230927235242202, 4304236805274466025, -6796821201909235177, -5632536022307838347}},
	{`a=>""`, "01000080010000800100000061", 279740777, [4]int64{-2680398551243259543, -6212514688606846393, 5167999311933606028, 8633502062376044274}},
	{"a=>1,b=>2", "020000800100008002000000030000000400000061316232", -1077040027, [4]int64{-3612477808123728795, 7681866774737967775, 3334253345108746484, -2898420751796699880}},
	{"a=>é", "01000080010000800300000061c3a9", 13266073, [4]int64{-6254575734291272551, -8027660884482757422, -5527249767148096311, 2393370504018584747}},
}

func hstoreComparisonHashChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT hstore_hash($1::hstore) AS hash,hstore_hash_extended($1::hstore,$2::bigint) AS extended"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, vector := range hstoreComparisonHashVectors {
		for i, seed := range []int64{0, 1, -1, 4294967296} {
			s := fixture.Select(q, []string{"hash", "extended"}, []string{"INT4", "INT8"}, [][]any{{vector.hash, vector.extended[i]}}, vector.input, seed)
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
			hstoreRun(t, p, b, held, s)
			literal := "SELECT hstore_hash(" + hstoreLiteral(vector.input) + "::hstore) AS hash,hstore_hash_extended(" + hstoreLiteral(vector.input) + "::hstore,$1::bigint) AS extended"
			s = fixture.Select(literal, []string{"hash", "extended"}, []string{"INT4", "INT8"}, [][]any{{vector.hash, vector.extended[i]}}, seed)
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
	}
	for _, args := range [][]any{{nil, int64(0)}, {nil, nil}, {"a=>1,b=>2", nil}, {`"b"=>"2", "a"=>"1"`, int64(0)}, {"a=>1,a=>1,b=>2", int64(0)}} {
		var hash, extended any
		if args[0] != nil {
			hash = int64(-1077040027)
			if args[1] != nil {
				extended = int64(-3612477808123728795)
			}
		}
		s := fixture.Select(q, []string{"hash", "extended"}, []string{"INT4", "INT8"}, [][]any{{hash, extended}}, args...)
		hstoreRun(t, p, b, c, s)
		hstoreRun(t, p, b, c, fixture.Prepared(s))
		hstoreRun(t, p, b, held, s)
	}
}

func TestReferenceHstoreComparisonHash(t *testing.T) {
	hstoreComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreComparisonHashChecks(t, p, b, c)
	})
}
