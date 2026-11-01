package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTSConfigLifecycle(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE TABLE public.ptsc_control_sentinel(v INT)",
			"CREATE VIEW public.ptsc_control_view AS SELECT oid,cfgname,cfgnamespace,cfgowner,cfgparser FROM pg_catalog.pg_ts_config",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO public.ptsc_control_sentinel VALUES(7)", 1))
		stmt, err := b.DB.PrepareContext(p.Ctx, pgTSConfigNameProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgTSConfigNameProbe}
		seedRows := func() [][]any {
			var english int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='english' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&english); err != nil || english <= 0 || english == 3748 {
				t.Fatal("actual English configuration identity", english, err)
			}
			var arabic int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='arabic' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&arabic); err != nil || arabic <= 0 || arabic == 3748 || arabic == english {
				t.Fatal("actual distinct Arabic configuration identity", arabic, err)
			}
			var armenian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='armenian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&armenian); err != nil || armenian <= 0 || armenian == 3748 || armenian == english || armenian == arabic {
				t.Fatal("actual distinct Armenian configuration identity", armenian, err)
			}
			var basque int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='basque' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&basque); err != nil || basque <= 0 || basque == 3748 || basque == english || basque == arabic || basque == armenian {
				t.Fatal("actual distinct Basque configuration identity", basque, err)
			}
			var danish int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='danish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&danish); err != nil || danish <= 0 || danish == 3748 || danish == english || danish == arabic || danish == armenian || danish == basque {
				t.Fatal("actual distinct Danish configuration identity", danish, err)
			}
			var dutch int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='dutch' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&dutch); err != nil || dutch <= 0 || dutch == 3748 || dutch == english || dutch == arabic || dutch == armenian || dutch == basque || dutch == danish {
				t.Fatal("actual distinct Dutch configuration identity", dutch, err)
			}
			var estonian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='estonian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&estonian); err != nil || estonian <= 0 || estonian == 3748 || estonian == english || estonian == arabic || estonian == armenian || estonian == basque || estonian == danish || estonian == dutch {
				t.Fatal("actual distinct Estonian configuration identity", estonian, err)
			}
			var german int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='german' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&german); err != nil || german <= 0 || german == 3748 || german == english || german == arabic || german == armenian || german == basque || german == danish || german == dutch || german == estonian {
				t.Fatal("actual distinct German configuration identity", german, err)
			}
			var greek int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='greek' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&greek); err != nil || greek <= 0 || greek == 3748 || greek == english || greek == arabic || greek == armenian || greek == basque || greek == danish || greek == dutch || greek == estonian || greek == german {
				t.Fatal("actual distinct Greek configuration identity", greek, err)
			}
			var hindi int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='hindi' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&hindi); err != nil || hindi <= 0 {
				t.Fatal("actual Hindi configuration identity", hindi, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek} {
				if hindi == earlier {
					t.Fatal("Hindi configuration identity reused", hindi)
				}
			}
			var hungarian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='hungarian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&hungarian); err != nil || hungarian <= 0 {
				t.Fatal("actual Hungarian configuration identity", hungarian, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi} {
				if hungarian == earlier {
					t.Fatal("Hungarian configuration identity reused", hungarian)
				}
			}
			var irish int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='irish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&irish); err != nil || irish <= 0 {
				t.Fatal("actual Irish configuration identity", irish, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian} {
				if irish == earlier {
					t.Fatal("Irish configuration identity reused", irish)
				}
			}
			var italian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='italian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&italian); err != nil || italian <= 0 {
				t.Fatal("actual Italian configuration identity", italian, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish} {
				if italian == earlier {
					t.Fatal("Italian configuration identity reused", italian)
				}
			}
			var lithuanian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='lithuanian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&lithuanian); err != nil || lithuanian <= 0 {
				t.Fatal("actual Lithuanian configuration identity", lithuanian, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian} {
				if lithuanian == earlier {
					t.Fatal("Lithuanian configuration identity reused", lithuanian)
				}
			}
			var norwegian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='norwegian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&norwegian); err != nil || norwegian <= 0 {
				t.Fatal("actual Norwegian configuration identity", norwegian, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian} {
				if norwegian == earlier {
					t.Fatal("Norwegian configuration identity reused", norwegian)
				}
			}
			var portuguese int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='portuguese' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&portuguese); err != nil || portuguese <= 0 {
				t.Fatal("actual Portuguese configuration identity", portuguese, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian} {
				if portuguese == earlier {
					t.Fatal("Portuguese configuration identity reused", portuguese)
				}
			}
			var yiddish int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='yiddish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&yiddish); err != nil || yiddish <= 0 {
				t.Fatal("actual Yiddish configuration identity", yiddish, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese} {
				if yiddish == earlier {
					t.Fatal("Yiddish configuration identity reused", yiddish)
				}
			}
			var indonesian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='indonesian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&indonesian); err != nil || indonesian <= 0 {
				t.Fatal("actual Indonesian configuration identity", indonesian, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish} {
				if indonesian == earlier {
					t.Fatal("Indonesian configuration identity reused", indonesian)
				}
			}
			var nepali int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='nepali' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&nepali); err != nil || nepali <= 0 {
				t.Fatal("actual Nepali configuration identity", nepali, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian} {
				if nepali == earlier {
					t.Fatal("Nepali configuration identity reused", nepali)
				}
			}
			var romanian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='romanian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&romanian); err != nil || romanian <= 0 {
				t.Fatal("actual Romanian configuration identity", romanian, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali} {
				if romanian == earlier {
					t.Fatal("Romanian configuration identity reused", romanian)
				}
			}
			var spanish int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='spanish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&spanish); err != nil || spanish <= 0 {
				t.Fatal("actual Spanish configuration identity", spanish, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali, romanian} {
				if spanish == earlier {
					t.Fatal("Spanish configuration identity reused", spanish)
				}
			}
			var swedish int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='swedish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&swedish); err != nil || swedish <= 0 {
				t.Fatal("actual Swedish configuration identity", swedish, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali, romanian, spanish} {
				if swedish == earlier {
					t.Fatal("Swedish configuration identity reused", swedish)
				}
			}
			var tamil int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='tamil' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&tamil); err != nil || tamil <= 0 {
				t.Fatal("actual Tamil configuration identity", tamil, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali, romanian, spanish, swedish} {
				if tamil == earlier {
					t.Fatal("Tamil configuration identity reused", tamil)
				}
			}
			var turkish int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='turkish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&turkish); err != nil || turkish <= 0 {
				t.Fatal("actual Turkish configuration identity", turkish, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali, romanian, spanish, swedish, tamil} {
				if turkish == earlier {
					t.Fatal("Turkish configuration identity reused", turkish)
				}
			}
			var serbian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='serbian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&serbian); err != nil || serbian <= 0 {
				t.Fatal("actual Serbian configuration identity", serbian, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali, romanian, spanish, swedish, tamil, turkish} {
				if serbian == earlier {
					t.Fatal("Serbian configuration identity reused", serbian)
				}
			}
			var finnish int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='finnish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&finnish); err != nil || finnish <= 0 {
				t.Fatal("actual distinct Finnish configuration identity", finnish, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali, romanian, spanish, swedish, tamil, turkish, serbian} {
				if finnish == earlier {
					t.Fatal("Finnish configuration identity reused", finnish)
				}
			}
			var catalan int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='catalan' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&catalan); err != nil || catalan <= 0 || catalan == 3748 || catalan == english || catalan == arabic || catalan == armenian || catalan == basque || catalan == finnish {
				t.Fatal("actual distinct Catalan configuration identity", catalan, err)
			}
			for _, earlier := range []int64{danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali, romanian, spanish, swedish, tamil, turkish, serbian} {
				if catalan == earlier {
					t.Fatal("Catalan configuration identity reused", catalan)
				}
			}
			var french int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='french' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&french); err != nil || french <= 0 || french == 3748 || french == english || french == arabic || french == armenian || french == basque || french == danish || french == dutch || french == estonian || french == german || french == greek || french == hindi || french == hungarian || french == irish || french == italian || french == lithuanian || french == norwegian || french == portuguese || french == yiddish || french == indonesian || french == nepali || french == romanian || french == spanish || french == swedish || french == tamil || french == turkish || french == serbian || french == finnish || french == catalan {
				t.Fatal("actual distinct French configuration identity", french, err)
			}
			var russian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='russian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&russian); err != nil || russian <= 0 {
				t.Fatal("actual Russian configuration identity", russian, err)
			}
			for _, earlier := range []int64{3748, english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali, romanian, spanish, swedish, tamil, turkish, serbian, finnish, catalan, french} {
				if russian == earlier {
					t.Fatal("Russian configuration identity reused", russian)
				}
			}
			rows := append([][]any{
				{arabic, "arabic", int64(11), int64(10), int64(3722)},
				{armenian, "armenian", int64(11), int64(10), int64(3722)},
				{basque, "basque", int64(11), int64(10), int64(3722)},
				{catalan, "catalan", int64(11), int64(10), int64(3722)},
				{danish, "danish", int64(11), int64(10), int64(3722)},
				{dutch, "dutch", int64(11), int64(10), int64(3722)},
				{english, "english", int64(11), int64(10), int64(3722)},
				{estonian, "estonian", int64(11), int64(10), int64(3722)},
				{finnish, "finnish", int64(11), int64(10), int64(3722)},
				{french, "french", int64(11), int64(10), int64(3722)},
				{german, "german", int64(11), int64(10), int64(3722)},
				{greek, "greek", int64(11), int64(10), int64(3722)},
				{hindi, "hindi", int64(11), int64(10), int64(3722)},
				{hungarian, "hungarian", int64(11), int64(10), int64(3722)},
				{indonesian, "indonesian", int64(11), int64(10), int64(3722)},
				{irish, "irish", int64(11), int64(10), int64(3722)},
				{italian, "italian", int64(11), int64(10), int64(3722)},
				{lithuanian, "lithuanian", int64(11), int64(10), int64(3722)},
				{nepali, "nepali", int64(11), int64(10), int64(3722)},
				{norwegian, "norwegian", int64(11), int64(10), int64(3722)},
				{portuguese, "portuguese", int64(11), int64(10), int64(3722)},
				{romanian, "romanian", int64(11), int64(10), int64(3722)},
				{russian, "russian", int64(11), int64(10), int64(3722)},
				{serbian, "serbian", int64(11), int64(10), int64(3722)},
			}, pgTSConfigSimple(b)...)
			return append(rows, []any{spanish, "spanish", int64(11), int64(10), int64(3722)}, []any{swedish, "swedish", int64(11), int64(10), int64(3722)}, []any{tamil, "tamil", int64(11), int64(10), int64(3722)}, []any{turkish, "turkish", int64(11), int64(10), int64(3722)}, []any{yiddish, "yiddish", int64(11), int64(10), int64(3722)})
		}
		catalog := func() {
			pgTSConfigInventory(t, p, b, b.DB)
			fixture.PgDescriptionExecutorRows(t, p, b, held, pgTSConfigStep(b, pgTSConfigNameProbe, pgTSConfigSimple(b), "simple"))
		}
		check := func() {
			catalog()
			q := "SELECT * FROM public.ptsc_control_view WHERE cfgname IN ('arabic','armenian','basque','catalan','danish','dutch','english','estonian','finnish','french','german','greek','hindi','hungarian','indonesian','irish','italian','lithuanian','nepali','norwegian','portuguese','romanian','russian','serbian','simple','spanish','swedish','tamil','turkish','yiddish') ORDER BY cfgname"
			pgTSConfigBoth(t, p, b, b.DB, fixture.Select(q, fixture.PGTSConfigColumns, pgTSConfigTypes, seedRows()))
			s := fixture.Select("SELECT v FROM public.ptsc_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		check()
		if b.PG {
			return // Native controls have no PostgreSQL API equivalent.
		}
		retainedRows := seedRows()
		old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_ts_config ORDER BY cfgname")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		saved := pgTSConfigMetadata(t, old, false)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		catalog()
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check()
		for _, expected := range retainedRows {
			if !old.Next() {
				t.Fatal("retained configuration cursor lost seed row", old.Err())
			}
			var oid, namespace, owner, parser int64
			var name string
			if err := old.Scan(&oid, &name, &namespace, &owner, &parser); err != nil || oid != expected[0] || name != expected[1] || namespace != expected[2] || owner != expected[3] || parser != expected[4] {
				t.Fatal("retained configuration seed row", oid, name, namespace, owner, parser, err)
			}
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained configuration cursor trailing rows", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		pgTSConfigSavedMetadata(t, saved, false)
	})
}
