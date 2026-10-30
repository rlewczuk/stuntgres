package functions

import (
	"strings"
	"testing"
)

// Independent transcription of the released SBL's suffix and exception classes.
// These are test literals, never enumerated from kernel tables. Each member is
// exercised at its exact boundary, with an extra prefix, and with a consonant
// neighbor, for both incoming test1 values. Expected strings are constructed
// from the release's replacement classes, not from any production matcher.
type greekStageException struct {
	stems, add string
	suffix     bool
}
type greekStageCase struct {
	name       string
	run        func(*greekState)
	endings    string
	base       string
	retain     bool
	vowels     string
	exceptions []greekStageException
}

func greekStageManifest() []greekStageCase {
	return []greekStageCase{
		{"s1", (*greekState).s1, "ιζα ιζεσ ιζε ιζαμε ιζατε ιζαν ιζανε ιζω ιζεισ ιζει ιζουμε ιζετε ιζουν ιζουνε", "", false, "", []greekStageException{
			{"αναμπα εμπα επα ξαναπα πα περιπα αθρο συναθρο δανε", "ι", false},
			{"μαρκ κορν αμπαρ αρρ βαθυρι βαρκ β βολβορ γκρ γλυκορ γλυκυρ ιμπ λ λου μαρ μ πρ μπρ πολυρ π ρ πιπερορ", "ιζ", false},
		}},
		{"s2", (*greekState).s2, "ωθηκα ωθηκεσ ωθηκε ωθηκαμε ωθηκατε ωθηκαν ωθηκανε", "", false, "", []greekStageException{{"αλ βι εν υψ λι ζω σ χ", "ων", false}}},
		{"s3", (*greekState).s3, "ισα ισεσ ισε ισαμε ισατε ισαν ισανε", "", false, "", []greekStageException{
			{"αναμπα αθρο εμπα εσε εσωκλε επα ξαναπα επε περιπα συναθρο δανε κλε χαρτοπα εξαρχα μετεπε αποκλε απεκλε εκλε πε", "ι", false},
			{"αν αφ γε γιγαντοαφ γκε δημοκρατ κομ γκ μ π πουκαμ ολο λαρ", "ισ", false},
		}},
		{"s4", (*greekState).s4, "ισω ισεισ ισει ισουμε ισετε ισουν ισουνε", "", false, "", []greekStageException{{"αναμπα εμπα εσε εσωκλε επα ξαναπα επε περιπα αθρο συναθρο δανε κλε χαρτοπα εξαρχα μετεπε αποκλε απεκλε εκλε πε", "ι", false}}},
		{"s5", (*greekState).s5, "ιστοσ ιστου ιστο ιστε ιστοι ιστων ιστουσ ιστη ιστησ ιστα ιστεσ", "", false, "", []greekStageException{
			{"δανε συναθρο κλε σε εσωκλε ασε πλε", "ι", false},
			{"μ π απ αρ ηδ κτ σκ σχ υψ φα χρ χτ ακτ αορ ασχ ατα αχν αχτ γεμ γυρ εμπ ευπ εχθ ηφα καθ κακ κυλ λυγ μακ μεγ ταχ φιλ χωρ", "ιστ", false},
		}},
		{"s6", (*greekState).s6, "ισμο ισμοι ισμοσ ισμου ισμουσ ισμων", "", false, "", []greekStageException{
			{"σε μετασε μικροσε εγκλε αποκλε", "ισμ", false}, {"δανε αντιδανε", "ι", false},
		}},
		{"s7", (*greekState).s7, "αρακι αρακια ουδακι ουδακια", "", false, "", []greekStageException{{"σ χ", "αρακ", false}}},
		{"s8", (*greekState).s8, "ακι ακια ιτσα ιτσασ ιτσεσ ιτσων αρακι αρακια", "", false, "", []greekStageException{
			{"βαμβ βρ καιμ κον κορ λαβρ λουλ μερ μουστ ναγκασ πλ ρ ρυ σ σκ σοκ σπαν τζ φαρμ χ καπακ αλισφ αμβρ ανθρ κ φυλ κατραπ κλιμ μαλ σλοβ φ σφ τσεχοσλοβ", "ακ", false},
			{"β βαλ γιαν γλ ζ ηγουμεν καρδ μακρυν νυφ πατερ π τοσ τριπολ", "ιτσ", false}, {"κορ", "ιτσ", true},
		}},
		{"s9", (*greekState).s9, "ιδιο ιδια ιδιων", "", false, "", []greekStageException{{"αιφν ιρ ολο ψαλ", "ιδ", false}, {"ε παιχν", "ιδ", true}}},
		{"s10", (*greekState).s10, "ισκοσ ισκου ισκο ισκε", "", false, "", []greekStageException{{"δ ιβ μην ρ φραγκ λυκ οβελ", "ισκ", false}}},
		{"2a", (*greekState).step2a, "αδεσ αδων", "αδ", true, "", []greekStageException{{"οκ μαμ μαν μπαμπ πατερ γιαγι νταντ κυρ θει πεθερ", "", true}}},
		{"2b", (*greekState).step2b, "εδεσ εδων", "", true, "", []greekStageException{{"οπ ιπ εμπ υπ γηπ δαπ κρασπ μιλ", "εδ", true}}},
		{"2c", (*greekState).step2c, "ουδεσ ουδων", "", true, "", []greekStageException{{"αρκ καλιακ πεταλ λιχ πλεξ σκ σ φλ φρ βελ λουλ χν σπ τραγ φε", "ουδ", true}}},
		{"2d", (*greekState).step2d, "εωσ εων", "", false, "", []greekStageException{{"θ δ ελ γαλ ν π ιδ παρ", "ε", false}}},
		{"3", (*greekState).step3, "ια ιου ιων", "", false, "αεηιουω", nil},
		{"4", (*greekState).step4, "ικα ικο ικου ικων", "", false, "αεηιουω", []greekStageException{{"αλ αδ ενδ αμαν αμμοχαλ ηθ ανηθ αντιδ φυσ βρωμ γερ εξωδ καλπ καλλιν καταδ μουλ μπαν μπαγιατ μπολ μποσ νιτ ξικ συνομηλ πετσ πιτσ πικαντ πλιατσ ποστελν πρωτοδ σερτ συναδ τσαμ υποδ φιλον φυλοδ χασ", "ικ", false}}},
		{"5a-long", (*greekState).step5a, "αγαμε ησαμε ουσαμε ηκαμε ηθηκαμε", "", false, "", nil},
		{"5a-short", (*greekState).step5a, "αμε", "", false, "", []greekStageException{{"αναπ αποθ αποκ αποστ βουβ ξεθ ουλ πεθ πικρ ποτ σιχ χ", "αμ", false}}},
		{"5b-long", (*greekState).step5b, "αγανε ησανε ουσανε ιοντανε ιοτανε ιουντανε οντανε οτανε ουντανε ηκανε ηθηκανε", "", false, "", []greekStageException{{"τρ τσ", "αγαν", false}}},
		{"5b-short", (*greekState).step5b, "ανε", "", false, "αεηιοω", []greekStageException{{
			"βετε\u03c1 βουλκ βραχμ γ δραδουμ θ καλπουζ καστελ κορμορ λαοπλ μωαμεθ μ μουσουλμ ν ουλ π πελεκ πλ πολισ πορτολ σαρακατσ σουλτ τσαρλατ ορφ τσιγγ τσοπ φωτοστεφ χ ψυχοπλ αγ γαλ γερ δεκ διπλ αμερικαν ουρ πιθ πουριτ σ ζωντ ικ καστ κοπ λιχ λουθηρ μαιντ μελ σιγ σπ στεγ τραγ τσαγ φ ερ αδαπ αθιγγ αμηχ ανικ ανοργ απηγ απιθ ατσιγγ βασ βασκ βαθυγαλ βιομηχ βραχυκ διατ διαφ ενοργ θυσ καπνοβιομηχ καταγαλ κλιβ κοιλαρφ λιβ μεγλοβιομηχ μικροβιομηχ νταβ ξηροκλιβ ολιγοδαμ ολογαλ πενταρφ περηφ περιτρ πλατ πολυδαπ πολυμηχ στεφ ταβ τετ υπερηφ υποκοπ χαμηλοδαπ ψηλοταβ", "αν", false,
		}}},
		{"5c-long", (*greekState).step5c, "ησετε", "", false, "", nil},
		{"5c-short", (*greekState).step5c, "ετε", "", false, "αεηιοω", []greekStageException{
			{"οδ αιρ φορ ταθ διαθ σχ ενδ ευρ τιθ υπερθ ραθ ενθ ροθ σθ πυρ αιν συνδ συν συνθ χωρ πον βρ καθ ευθ εκθ νετ ρον αρκ βαρ βολ ωφελ", "ετ", true},
			{"αβαρ βεν εναρ αβρ αδ αθ αν απλ βαρον ντρ σκ κοπ μπορ νιφ παγ παρακαλ σερπ σκελ συρφ τοκ υ δ εμ θαρρ θ", "ετ", false},
		}},
		{"5d", (*greekState).step5d, "οντασ ωντασ", "", false, "", []greekStageException{{"αρχ", "οντ", false}, {"κρε", "ωντ", true}}},
		{"5e", (*greekState).step5e, "ομαστε ιομαστε", "", false, "", []greekStageException{{"ον", "ομαστ", false}}},
		{"5f-long", (*greekState).step5f, "ιεστε", "", false, "", []greekStageException{{"π απ συμπ ασυμπ ακαταπ αμεταμφ", "ιεστ", false}}},
		{"5f-short", (*greekState).step5f, "εστε", "", false, "", []greekStageException{{"αλ αρ εκτελ ζ μ ξ παρακαλ προ νισ", "ιεστ", false}}},
		{"5g-long", (*greekState).step5g, "ηθηκα ηθηκεσ ηθηκε", "", false, "", nil},
		{"5g-short", (*greekState).step5g, "ηκα ηκεσ ηκε", "", false, "", []greekStageException{{"σκωλ σκουλ ναρθ σφ οθ πιθ", "ηκ", true}, {"διαθ θ παρακαταθ προσθ συνθ", "ηκ", false}}},
		{"5h", (*greekState).step5h, "ουσα ουσεσ ουσε", "", false, "", []greekStageException{
			{"ποδαρ βλεπ πανταχ φρυδ μαντιλ μαλλ κυματ λαχ ληγ φαγ ομ πρωτ", "ουσ", true},
			{"φαρμακ χαδ αγκ αναρρ βρομ εκλιπ λαμπιδ λεχ μ πατ ρ λ μεδ μεσαζ υποτειν αμ αιθ ανηκ δεσποζ ενδιαφερ δε δευτερευ καθαρευ πλε τσα", "ουσ", false},
		}},
		{"5i", (*greekState).step5i, "αγα αγεσ αγε", "", false, "", []greekStageException{
			{"κολλ", "αγ", true}, {"ψοφ ναυλοχ", "", true}, {"οφ πελ χορτ λλ σφ ρπ φρ πρ λοχ σμην", "αγ", true},
			{"αβαστ πολυφ αδηφ παμφ ρ ασπ αφ αμαλ αμαλλι ανυστ απερ ασπαρ αχαρ δερβεν δροσοπ ξεφ νεοπ νομοτ ολοπ ομοτ προστ προσωποπ συμπ συντ τ υποτ χαρ αειπ αιμοστ ανυπ αποτ αρτιπ διατ εν επιτ κροκαλοπ σιδηροπ λ ναυ ουλαμ ουρ π τρ μ", "αγ", false},
		}},
		{"5j", (*greekState).step5j, "ησε ησου ησα", "", false, "", []greekStageException{{"ν χερσον δωδεκαν ερημον μεγαλον επταν", "ησ", false}}},
		{"5k", (*greekState).step5k, "ηστε", "", false, "", []greekStageException{{"ασβ σβ αχρ χρ απλ αειμν δυσχρ ευχρ κοινοχρ παλιμψ", "ηστ", false}}},
		{"5l", (*greekState).step5l, "ουνε ησουνε ηθουνε", "", false, "", []greekStageException{{"ν ρ σπι στραβομουτσ κακομουτσ εξων", "ουν", false}}},
		{"5m", (*greekState).step5m, "ουμε ησουμε ηθουμε", "", false, "", []greekStageException{{"παρασουσ φ χ ωριοπλ αζ αλλοσουσ ασουσ", "ουμ", false}}},
		{"7", (*greekState).step7, "εστερ εστατ οτερ οτατ υτερ υτατ ωτερ ωτατ", "", true, "", nil},
	}
}

func greekAssertStage(t *testing.T, run func(*greekState), input, want string, incoming, flag bool) {
	t.Helper()
	g := greekState{w: append(make([]rune, 0, len(input)+8), []rune(input)...), test1: incoming}
	run(&g)
	if string(g.w) != want || g.test1 != flag {
		t.Errorf("%q flag=%v -> %q/%v, want %q/%v", input, incoming, string(g.w), g.test1, want, flag)
	}
}

func TestSnowballGreekStages(t *testing.T) {
	t.Run("irregular", greekTestIrregular)
	t.Run("secondary-and-sequential", greekTestSecondary)
	t.Run("generic-and-truncations", greekTestGeneric)
	t.Run("cursor-restoration-overlaps", greekTestCursorOverlaps)
	for _, stage := range greekStageManifest() {
		t.Run(stage.name, func(t *testing.T) {
			// Different stage groups may share the same routine (the independent
			// do-block and the required trailing block are tested separately).
			prefixes := []string{"σσσ", "", "ξ", "θ", "α", "ε", "η", "ι", "ο", "υ", "ω", "σσσα", "σσσε", "σσση", "σσσι", "σσσο", "σσσυ", "σσσω"}
			for _, ex := range stage.exceptions {
				for _, stem := range strings.Fields(ex.stems) {
					prefixes = append(prefixes, stem, "σσσ"+stem, stem+"ξ")
				}
			}
			for _, ending := range strings.Fields(stage.endings) {
				for _, incoming := range []bool{false, true} {
					for _, prefix := range prefixes {
						add := stage.base
						if prefix != "" && strings.ContainsRune(stage.vowels, []rune(prefix)[len([]rune(prefix))-1]) {
							switch stage.name {
							case "3":
								add = "ι"
							case "4":
								add = "ικ"
							case "5b-short":
								add = "αν"
							case "5c-short":
								add = "ετ"
							}
						}
						for _, ex := range stage.exceptions {
							matched := false
							for _, stem := range strings.Fields(ex.stems) {
								if prefix == stem || ex.suffix && strings.HasSuffix(prefix, stem) {
									matched = true
									break
								}
							}
							if matched {
								add = ex.add
								break
							}
						}
						want, flag := prefix+add, incoming && stage.retain
						if stage.name == "s3" && prefix == "" && ending == "ισα" {
							want, flag = "ισ", incoming
						}
						if stage.name == "5a-long" && prefix == "" && ending == "αγαμε" {
							want, flag = "αγαμ", incoming
						}
						// Longest released matches shadow short-arm boundaries.
						// step5b lists both ι-prefixed and plain long endings.
						// A trailing ι in the constructed prefix belongs to the
						// longer suffix, rather than surviving its deletion.
						if stage.name == "5b-long" && strings.HasSuffix(prefix, "ι") {
							switch ending {
							case "οντανε", "οτανε", "ουντανε":
								want = strings.TrimSuffix(prefix, "ι")
							}
						}
						if stage.name == "5f-short" && strings.HasSuffix(prefix, "ι") {
							want = strings.TrimSuffix(prefix, "ι")
						}
						if stage.name == "5e" && strings.HasSuffix(prefix, "ι") && ending == "ομαστε" {
							want = strings.TrimSuffix(prefix, "ι")
						}
						if stage.name == "5b-short" && strings.HasSuffix(prefix, "αγ") {
							left := strings.TrimSuffix(prefix, "αγ")
							want = left
							if left == "τρ" || left == "τσ" {
								want += "αγαν"
							}
						}
						greekAssertStage(t, stage.run, prefix+ending, want, incoming, flag)
					}
					for _, tail := range []string{"!", "\u0301"} {
						input := "σσσ" + ending + tail
						greekAssertStage(t, stage.run, input, input, incoming, incoming)
					}
				}
			}
			for _, incoming := range []bool{false, true} {
				greekAssertStage(t, stage.run, "σσσ", "σσσ", incoming, incoming)
			}
		})
	}
}
