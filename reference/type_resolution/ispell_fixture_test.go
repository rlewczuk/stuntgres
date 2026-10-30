package reference_test

import (
	"database/sql"
	"embed"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

//go:embed testdata/ispell/*
var ispellData embed.FS

// The released MySpell fixture deliberately uses ispell_sample.dict, not a
// nonexistent hunspell_sample.dict. Each installed name is package-unique.
var ispellFiles = ispellManifest()

func ispellManifest() []stuntgres.TextSearchFile {
	var files []stuntgres.TextSearchFile
	for _, pair := range []struct{ name, source string }{
		{"tr_ispell_legacy.affix", "ispell_sample.affix"},
		{"tr_ispell_legacy.dict", "ispell_sample.dict"},
		{"tr_ispell_default.affix", "hunspell_sample.affix"},
		{"tr_ispell_default.dict", "ispell_sample.dict"},
		{"tr_ispell_long.affix", "hunspell_sample_long.affix"},
		{"tr_ispell_long.dict", "hunspell_sample_long.dict"},
		{"tr_ispell_num.affix", "hunspell_sample_num.affix"},
		{"tr_ispell_num.dict", "hunspell_sample_num.dict"},
	} {
		data, err := ispellData.ReadFile("testdata/ispell/" + pair.source)
		if err != nil {
			panic(err)
		}
		files = append(files, stuntgres.TextSearchFile{Name: pair.name, Data: data})
	}
	for _, file := range []struct{ name, data string }{
		{"tr_ispell_book.stop", "book\n"},
		{"tr_ispell_sun.stop", "sun\n"},
		{"tr_ispell_empty.dict", ""}, {"tr_ispell_empty.affix", ""}, {"tr_ispell_empty.stop", ""},
		{"tr_ispell_utf8.affix", "FLAG UTF-8\n"},
		{"tr_ispell_mixed.affix", "prefixes\nPFX A Y 1\n"},
		{"tr_ispell_syntax.affix", "suffixes\nflag A:\n. > 1\n"},
		{"tr_ispell_regex.affix", "SFX A Y 1\nSFX A 0 s (\n"},
		{"tr_ispell_alias_zero.affix", "FLAG long\nAF 0\n"},
		{"tr_ispell_alias_extra.affix", "FLAG long\nAF 1\nAF AA\nAF BB\n"},
		{"tr_ispell_alias_short.affix", "FLAG long\nAF 2\nAF AA\n"},
		// Released FLAGNUM_MAXSIZE is 1<<16; the parser rejects values above
		// that inclusive bound, not values above the reader comment's 65000.
		{"tr_ispell_range.affix", "FLAG num\nCOMPOUNDFLAG 65537\n"},
		{"tr_ispell_badbytes.dict", "book\n\xff\n"},
		{"tr_ispell_badbytes.affix", "# valid\n\xff\n"},
		{"tr_ispell_badbytes.stop", "book\n\xff\n"},
		// Independently specified review regressions. An incomplete long flag
		// is dormant until compound setup or a matching affix demands it.
		{"tr_ispell_long_odd.dict", "root/A\n"},
		{"tr_ispell_long_even.dict", "root/AA\n"},
		{"tr_ispell_plain.dict", "root\n"},
		{"tr_ispell_long_demand.affix", "FLAG long\nCOMPOUNDFLAG ZZ\n"},
		{"tr_ispell_long_deferred.affix", "FLAG long\nPFX AA N 1\nPFX AA 0 pre .\n"},
		// Duplicate words merge distinct raw flag identities before long-mode
		// membership decoding. Both orders have a recognizing suffix rule.
		{"tr_ispell_duplicate_long.dict", "root/A\nroot/B\n"},
		{"tr_ispell_duplicate_long_reverse.dict", "root/B\nroot/A\n"},
		{"tr_ispell_duplicate_long_equal.dict", "root/A\nroot/A\n"},
		{"tr_ispell_duplicate_long_complete.dict", "root/AB\n"},
		{"tr_ispell_duplicate_long.affix", "FLAG long\nSFX AB N 1\nSFX AB 0 s .\nSFX BA N 1\nSFX BA 0 s .\n"},
		{"tr_ispell_duplicate_long_equal.affix", "FLAG long\nSFX AA N 1\nSFX AA 0 s .\n"},
		{"tr_ispell_reader_ignored.affix", "FLAG long\n\n# ignored comment\nUNRECOGNIZED\n"},
		{"tr_ispell_compound_first_error.affix", "COMPOUNDFLAG \t\nFLAG UTF-8\n"},
		// Legacy compound keys remain complete strings even in character mode.
		{"tr_ispell_legacy_compound.dict", "root/Z\n"},
		{"tr_ispell_legacy_compound_single.affix", "compoundwords controlled Z\n"},
		{"tr_ispell_legacy_compound_full.affix", "compoundwords controlled ZZ\n"},
		// Raw first-pass FLAG witnesses: SFX selects MySpell before FLAG,
		// independently of the legacy reader's single-character flag rule.
		{"tr_ispell_flag_hash.affix", "SFX A Y 1\nSFX A 0 s .\nFLAG #\n"},
		{"tr_ispell_flag_hash_prefix.affix", "SFX A Y 1\nSFX A 0 s .\nFLAG#\n"},
		{"tr_ispell_flag_unknown.affix", "SFX A Y 1\nSFX A 0 s .\nFLAG \tUTF-8\n"},
		{"tr_ispell_flag_upper.affix", "SFX A Y 1\nSFX A 0 s .\nFLAG LONG\n"},
		{"tr_ispell_flag_empty.affix", "SFX A Y 1\nSFX A 0 s .\nFLAG"},
		{"tr_ispell_flag_space.affix", "SFX A Y 1\nSFX A 0 s .\nFLAG \t\r\n"},
		{"tr_ispell_flag_indented.affix", "SFX A Y 1\nSFX A 0 s .\n FLAG #\n"},
		{"tr_ispell_flag_lowercase.affix", "SFX A Y 1\nSFX A 0 s .\nflag #\n"},
		{"tr_ispell_flag_default.affix", "SFX A Y 1\nSFX A 0 s .\nFLAGdefault_extra\n"},
		{"tr_ispell_flag_long_prefix.affix", "SFX AA Y 1\nSFX AA 0 s .\nFLAGlong_extra\n"},
		{"tr_ispell_flag_num_prefix.affix", "SFX 12 Y 1\nSFX 12 0 s .\nFLAGnum_extra\n"},
		{"tr_ispell_flag_num.dict", "root/12\n"},
		{"tr_ispell_flag_long_default.affix", "FLAG long\nFLAG default\nSFX AA Y 1\nSFX AA 0 s .\n"},
		{"tr_ispell_flag_num_default.affix", "FLAG num\nFLAG default\nSFX 12 Y 1\nSFX 12 0 s .\n"},
		{"tr_ispell_flag_long_empty.affix", "FLAG long\nFLAG \t\nSFX AA Y 1\nSFX AA 0 s .\n"},
		{"tr_ispell_flag_last_num.affix", "FLAG long\nFLAG num\nSFX 12 Y 1\nSFX 12 0 s .\n"},
		{"tr_ispell_flag_last_long.affix", "FLAG num\nFLAG long\nSFX AA Y 1\nSFX AA 0 s .\n"},
		{"tr_ispell_flag_legacy_hash.affix", "FLAG #\n"},
		{"tr_ispell_flag_legacy_prefix.affix", "FLAG#\n"},
		// Second-pass dispatch is lowercased prefix matching. Missing first
		// AF operands reject, but later empty AF entries are real aliases.
		{"tr_ispell_af_bare.affix", "SFX A Y 1\nSFX A 0 s .\nAF\n"},
		{"tr_ispell_af_space.affix", "SFX A Y 1\nSFX A 0 s .\nAF \t\n"},
		{"tr_ispell_af_comment.affix", "SFX A Y 1\nSFX A 0 s .\nAF # comment\n"},
		{"tr_ispell_af_attached_comment.affix", "SFX A Y 1\nSFX A 0 s .\nAF#\n"},
		{"tr_ispell_af_prefix_zero.affix", "SFX A Y 1\nSFX A 0 s .\naFextra 0\n"},
		{"tr_ispell_af_prefix_bare.affix", "SFX A Y 1\nSFX A 0 s .\nAFextra\n"},
		{"tr_ispell_af_prefix_valid.affix", "SFX A Y 1\nSFX A 0 s .\naFextra 1\nAfentry A\n"},
		{"tr_ispell_af_alias.dict", "root/1\n"},
		{"tr_ispell_af_empty_entry.affix", "SFX A Y 1\nSFX A 0 s .\nAF 1\nAF\n"},
		{"tr_ispell_af_empty_comment.affix", "SFX A Y 1\nSFX A 0 s .\nAF 1\nAF # empty\n"},
		{"tr_ispell_af_empty_excess.affix", "SFX A Y 1\nSFX A 0 s .\nAF 1\nAF A\nAF\n"},
		{"tr_ispell_sfx_prefix.affix", "SFXextra A Y 1\nsFxrule A 0 s .\n"},
		{"tr_ispell_pfx_prefix.affix", "PFXextra A Y 1\npFxrule A 0 pre .\n"},
		{"tr_ispell_sfx_header.affix", "SFXextra A Y 1\nPFXrule A 0 s .\n"},
		{"tr_ispell_pfx_header.affix", "PFXextra A Y 1\nSFXrule A 0 pre .\n"},
		{"tr_ispell_second_ignored.affix", "SFX A Y 1\nSFX A 0 s .\nXAF\nXSFX A 0 x .\nXPFX A 0 pre .\nPFX\nSFX A\n"},
		// Reader witnesses: dictionary words end at C whitespace, retain flag
		// case, lowercase words; a count is an ordinary dictionary word.
		{"tr_ispell_reader.dict", "4\nMiXeD/A\nwrong/a\nROOT/A trailing text\nplain ignored\n"},
		{"tr_ispell_reader.affix", "SET UTF-8\nIGNORED directive\nSFX A Y 99\nSFX A 0 s .\n"},
		// Positional compound flags and affix continuation flags. Distinct
		// word spellings make wrong-position and forbidden splits observable.
		{"tr_ispell_positions.dict", "sun/B\nmid/M\nset/E\nlink/ZE\nonly/LO\nplain/Z\n"},
		{"tr_ispell_positions.affix", "COMPOUNDBEGIN B\nCOMPOUNDMIDDLE M\nCOMPOUNDEND E\nCOMPOUNDFLAG Z\nONLYINCOMPOUND O\nCOMPOUNDPERMITFLAG P\nCOMPOUNDFORBIDFLAG F\nSFX L Y 2\nSFX L 0 x/P .\nSFX L 0 q/F .\n"},
	} {
		files = append(files, stuntgres.TextSearchFile{Name: file.name, Data: []byte(file.data)})
	}
	for _, command := range []string{
		"COMPOUNDFLAG", "COMPOUNDBEGIN", "COMPOUNDMIDDLE", "COMPOUNDLAST",
		"COMPOUNDEND", "ONLYINCOMPOUND", "COMPOUNDPERMITFLAG", "COMPOUNDFORBIDFLAG",
	} {
		for _, form := range []struct{ name, ending string }{
			{"bare", "\n"}, {"space", " \t\n"},
		} {
			name := strings.ToLower(command) + "_" + form.name
			line := command + form.ending
			// FLAG long selects MySpell even for directives that alone are
			// intentionally ignored by PostgreSQL's legacy-format detector.
			files = append(files,
				stuntgres.TextSearchFile{Name: "tr_ispell_missing_" + name + ".affix", Data: []byte("FLAG long\n" + line)},
				stuntgres.TextSearchFile{Name: "tr_ispell_unmarked_" + name + ".affix", Data: []byte(line)},
			)
		}
	}
	files = append(files, ispellNumericFiles()...)
	return append(files, ispellInterleavedAliasFiles()...)
}

func ispellConfigure(d *stuntgres.Driver) error {
	for _, file := range ispellFiles {
		if err := d.RegisterTextSearchFile(file); err != nil {
			return err
		}
	}
	return nil
}

func ispellScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"ispell_creator", "ispell_peer", "ispell_recipient"})
	fixture.SchemaReferenceCleanup(t, "ispell_owned", "ispell_right")
	p := fixture.NewScenarioWithDriverConfig(t, ispellConfigure)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func ispellCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name, format, extra string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TEXT SEARCH DICTIONARY "+name+" (TEMPLATE=pg_catalog.ispell,DictFile=tr_ispell_"+format+",AffFile=tr_ispell_"+format+extra+")", 0))
}

func ispellToken(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name, token string, want any) {
	t.Helper()
	dictionaryResult(t, p, b, e, "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", want, name, token)
}
