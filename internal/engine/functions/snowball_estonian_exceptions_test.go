package functions

import (
	"strings"
	"testing"
)

// Independently transcribed whole-token expectations from pinned estonian.sbl,
// grouped by their literal outputs. Never enumerate the implementation switch.
func TestSnowballEstonianExceptions(t *testing.T) {
	groups := []struct{ stem, forms string }{
		{"joo", "joon jood joob joote joome joovad jõin jõid jõi jõime jõite joomata juuakse joodakse juua jooma"},
		{"saa", "saan saad saab saate saame saavad saaksin saaksid saaks saaksite saaksime sain said sai saite saime saamata saadakse saadi saama saada"},
		{"viima", "viin viid viib viite viime viivad viiksin viiksid viiks viiksite viiksime viisin viisite viisime viimata viiakse viidi viima viia"},
		{"keesi", "keen keeb keed kees keeme keete keevad keeksin keeks keeksid keeksime keeksite keemata keema keeta keedakse"},
		{"löö", "löön lööd lööb lööme lööte löövad lööksin lööksid lööks lööksime lööksite löömata lüüakse löödakse löödi lööma lüüa"},
		{"lõi", "lõin lõid lõi lõime lõite"},
		{"loo", "loon lood loob loome loote loovad looksin looksid looks looksime looksite loomata luuakse loodi luua looma"},
		{"käisi", "käin käib käid käis käime käite käivad käiksin käiks käiksid käiksime käiksite käimata käiakse käidi käia käima"},
		{"söö", "söön sööb sööd sööme sööte söövad sööksin sööks sööksid sööksime sööksite sõin sõi sõid sõime sõite söömata süüakse söödakse söödi sööma süüa"},
		{"too", "toon tood toob toote toome toovad tooksin tooksid tooks tooksite tooksime tõin tõid tõi tõime tõite toomata tuuakse toodi tooma tuua"},
		{"võisi", "võin võid võib võime võis võite võivad võiksin võiksid võiks võiksime võiksite võimata võidakse võidi võida võima"},
		{"jääma", "jään jääd jääb jääme jääte jäävad jääksin jääksid jääks jääksime jääksite jäime jäite jäin jäid jäi jäämata jäädakse jääda jääma jäädi"},
		{"müüsi", "müün müüd müüb müüs müüme müüte müüvad müüksin müüksid müüks müüksime müüksite müümata müüakse müüdi müüa müüma"},
		{"luge", "loeb loen loed loeme loete loevad loeks loeksin loeksid loeksime loeksite"},
		{"põde", "põen põeb põed põeme põete põevad põeksin põeks põeksid põeksime põeksite"},
		{"ladu", "laon laob laod laome laote laovad laoksin laoks laoksid laoksime laoksite"},
		{"tegi", "teeksin teeks teeksid teeksime teeksite teen teeb teed teeme teete teevad tegemata tehakse tehti tegema teha"},
		{"nägi", "näen näeb näed näeme näete näevad näeksin näeks näeksid näeksime näeksite nägemata nähakse nähti näha nägema"},
	}
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, input := range strings.Fields(group.forms) {
			if seen[input] {
				t.Fatal("duplicate exception witness", input)
			}
			seen[input] = true
			if got := estonianStem(input); got != group.stem {
				t.Errorf("%q: got %q want %q", input, got, group.stem)
			}
		}
	}
	if len(groups) != 18 || len(seen) != 290 {
		t.Fatal("complete release exception inventory", len(groups), len(seen))
	}
}
