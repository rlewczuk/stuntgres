package functions

func (g *greekState) step2a() {
	if g.cut("αδεσ αδων", false) && !g.has("οκ μαμ μαν μπαμπ πατερ γιαγι νταντ κυρ θει πεθερ", false) {
		g.add("αδ")
	}
}
func (g *greekState) step2b() {
	if g.cut("εδεσ εδων", false) && g.has("οπ ιπ εμπ υπ γηπ δαπ κρασπ μιλ", false) {
		g.add("εδ")
	}
}
func (g *greekState) step2c() {
	if g.cut("ουδεσ ουδων", false) && g.has("αρκ καλιακ πεταλ λιχ πλεξ σκ σ φλ φρ βελ λουλ χν σπ τραγ φε", false) {
		g.add("ουδ")
	}
}
func (g *greekState) step2d() {
	if g.cut("εωσ εων", true) && g.has("θ δ ελ γαλ ν π ιδ παρ", true) {
		g.add("ε")
	}
}
func (g *greekState) step3() {
	if g.cut("ια ιου ιων", true) && g.vowel(false) {
		g.add("ι")
	}
}

// In released steps 4/5b/5c, the first restoration inserts at the old end,
// but its backward predicate has consumed a vowel/suffix. The following
// exact lookup uses that shortened cursor and replaces the consumed span,
// retaining the inserted tail. Resolve that lookup before appending so both
// insertions fit in the invocation's existing scratch without shifting text.
func (g *greekState) restoreAtCursor(replacement, exact string, consumed int) {
	cursor := len(g.w) - consumed
	if greekMatch(g.w[:cursor], exact, true) != 0 {
		g.w = g.w[:cursor]
		g.add(replacement)
	}
	if consumed != 0 {
		g.add(replacement)
	}
}

func (g *greekState) step4() {
	if !g.cut("ικα ικο ικου ικων", true) {
		return
	}
	consumed := 0
	if g.vowel(false) {
		consumed = 1
	}
	g.restoreAtCursor("ικ", "αλ αδ ενδ αμαν αμμοχαλ ηθ ανηθ αντιδ φυσ βρωμ γερ εξωδ καλπ καλλιν καταδ μουλ μπαν μπαγιατ μπολ μποσ νιτ ξικ συνομηλ πετσ πιτσ πικαντ πλιατσ ποστελν πρωτοδ σερτ συναδ τσαμ υποδ φιλον φυλοδ χασ", consumed)
}
func (g *greekState) step5a() {
	// The initial exact replacement is a do-block: its flag is unchanged.
	if g.has("αγαμε", true) {
		g.w = g.w[:4]
	}
	g.cut("αγαμε ησαμε ουσαμε ηκαμε ηθηκαμε", true)
	if g.cut("αμε", true) && g.has("αναπ αποθ αποκ αποστ βουβ ξεθ ουλ πεθ πικρ ποτ σιχ χ", true) {
		g.add("αμ")
	}
}
func (g *greekState) step5b() {
	if g.cut("αγανε ησανε ουσανε ιοντανε ιοτανε ιουντανε οντανε οτανε ουντανε ηκανε ηθηκανε", true) && g.has("τρ τσ", true) {
		g.add("αγαν")
	}
	if !g.cut("ανε", true) {
		return
	}
	consumed := 0
	if g.vowel(true) {
		consumed = 1
	}
	g.restoreAtCursor("αν", "βετε\u03c1 βουλκ βραχμ γ δραδουμ θ καλπουζ καστελ κορμορ λαοπλ μωαμεθ μ μουσουλμ ν ουλ π πελεκ πλ πολισ πορτολ σαρακατσ σουλτ τσαρλατ ορφ τσιγγ τσοπ φωτοστεφ χ ψυχοπλ αγ γαλ γερ δεκ διπλ αμερικαν ουρ πιθ πουριτ σ ζωντ ικ καστ κοπ λιχ λουθηρ μαιντ μελ σιγ σπ στεγ τραγ τσαγ φ ερ αδαπ αθιγγ αμηχ ανικ ανοργ απηγ απιθ ατσιγγ βασ βασκ βαθυγαλ βιομηχ βραχυκ διατ διαφ ενοργ θυσ καπνοβιομηχ καταγαλ κλιβ κοιλαρφ λιβ μεγλοβιομηχ μικροβιομηχ νταβ ξηροκλιβ ολιγοδαμ ολογαλ πενταρφ περηφ περιτρ πλατ πολυδαπ πολυμηχ στεφ ταβ τετ υπερηφ υποκοπ χαμηλοδαπ ψηλοταβ", consumed)
}
func (g *greekState) step5c() {
	g.cut("ησετε", true)
	if !g.cut("ετε", true) {
		return
	}
	consumed := 0
	if g.vowel(true) {
		consumed = 1
	} else {
		consumed = greekMatch(g.w, "οδ αιρ φορ ταθ διαθ σχ ενδ ευρ τιθ υπερθ ραθ ενθ ροθ σθ πυρ αιν συνδ συν συνθ χωρ πον βρ καθ ευθ εκθ νετ ρον αρκ βαρ βολ ωφελ", false)
	}
	g.restoreAtCursor("ετ", "αβαρ βεν εναρ αβρ αδ αθ αν απλ βαρον ντρ σκ κοπ μπορ νιφ παγ παρακαλ σερπ σκελ συρφ τοκ υ δ εμ θαρρ θ", consumed)
}
func (g *greekState) step5d() {
	if !g.cut("οντασ ωντασ", true) {
		return
	}
	if g.has("αρχ", true) {
		g.add("οντ")
	} else if g.has("κρε", false) {
		g.add("ωντ")
	}
}
func (g *greekState) step5e() {
	if g.cut("ομαστε ιομαστε", true) && g.has("ον", true) {
		g.add("ομαστ")
	}
}
func (g *greekState) step5f() {
	if g.cut("ιεστε", true) && g.has("π απ συμπ ασυμπ ακαταπ αμεταμφ", true) {
		g.add("ιεστ")
	}
	if g.cut("εστε", true) && g.has("αλ αρ εκτελ ζ μ ξ παρακαλ προ νισ", true) {
		g.add("ιεστ")
	}
}
func (g *greekState) step5g() {
	g.cut("ηθηκα ηθηκεσ ηθηκε", true)
	if g.cut("ηκα ηκεσ ηκε", true) && (g.has("σκωλ σκουλ ναρθ σφ οθ πιθ", false) || g.has("διαθ θ παρακαταθ προσθ συνθ", true)) {
		g.add("ηκ")
	}
}
func (g *greekState) step5h() {
	if g.cut("ουσα ουσεσ ουσε", true) && (g.has("ποδαρ βλεπ πανταχ φρυδ μαντιλ μαλλ κυματ λαχ ληγ φαγ ομ πρωτ", false) || g.has("φαρμακ χαδ αγκ αναρρ βρομ εκλιπ λαμπιδ λεχ μ πατ ρ λ μεδ μεσαζ υποτειν αμ αιθ ανηκ δεσποζ ενδιαφερ δε δευτερευ καθαρευ πλε τσα", true)) {
		g.add("ουσ")
	}
}
func (g *greekState) step5i() {
	if !g.cut("αγα αγεσ αγε", true) {
		return
	}
	if g.has("κολλ", false) {
		g.add("αγ")
		return
	}
	// Empty among arms are successful blockers, not a request to try a
	// shorter suffix (ψοφ must not fall through to οφ).
	if g.has("ψοφ ναυλοχ", false) {
		return
	}
	if g.has("οφ πελ χορτ λλ σφ ρπ φρ πρ λοχ σμην", false) || g.has("αβαστ πολυφ αδηφ παμφ ρ ασπ αφ αμαλ αμαλλι ανυστ απερ ασπαρ αχαρ δερβεν δροσοπ ξεφ νεοπ νομοτ ολοπ ομοτ προστ προσωποπ συμπ συντ τ υποτ χαρ αειπ αιμοστ ανυπ αποτ αρτιπ διατ εν επιτ κροκαλοπ σιδηροπ λ ναυ ουλαμ ουρ π τρ μ", true) {
		g.add("αγ")
	}
}
func (g *greekState) step5j() {
	if g.cut("ησε ησου ησα", true) && g.has("ν χερσον δωδεκαν ερημον μεγαλον επταν", true) {
		g.add("ησ")
	}
}
func (g *greekState) step5k() {
	if g.cut("ηστε", true) && g.has("ασβ σβ αχρ χρ απλ αειμν δυσχρ ευχρ κοινοχρ παλιμψ", true) {
		g.add("ηστ")
	}
}
func (g *greekState) step5l() {
	if g.cut("ουνε ησουνε ηθουνε", true) && g.has("ν ρ σπι στραβομουτσ κακομουτσ εξων", true) {
		g.add("ουν")
	}
}
func (g *greekState) step5m() {
	if g.cut("ουμε ησουμε ηθουμε", true) && g.has("παρασουσ φ χ ωριοπλ αζ αλλοσουσ ασουσ", true) {
		g.add("ουμ")
	}
}
func (g *greekState) step6() {
	if g.cut("ματα ματων ματοσ", false) {
		g.add("μα")
	}
	if g.test1 {
		g.cut("α αγατε αγαν αει αμαι αν ασ ασαι αται αω ε ει εισ ειτε εσαι εσ εται ι ιεμαι ιεμαστε ιεται ιεσαι ιεσαστε ιομασταν ιομουν ιομουνα ιονταν ιοντουσαν ιοσασταν ιοσαστε ιοσουν ιοσουνα ιοταν ιουμα ιουμαστε ιουνται ιουνταν η ηδεσ ηδων ηθει ηθεισ ηθειτε ηθηκατε ηθηκαν ηθουν ηθω ηκατε ηκαν ησ ησαν ησατε ησει ησεσ ησουν ησω ο οι ομαι ομασταν ομουν ομουνα ονται ονταν οντουσαν οσ οσασταν οσαστε οσουν οσουνα οταν ου ουμαι ουμαστε ουν ουνται ουνταν ουσ ουσαν ουσατε υ υσ ω ων", false)
	}
}
func (g *greekState) step7() {
	g.cut("εστερ εστατ οτερ οτατ υτερ υτατ ωτερ ωτατ", false)
}
