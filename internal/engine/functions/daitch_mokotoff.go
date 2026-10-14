package functions

// Daitch–Mokotoff Soundex, derived from PostgreSQL REL_18_0
// contrib/fuzzystrmatch/daitch_mokotoff.c and daitch_mokotoff_header.pl.
// Copyright (c) 2023-2025, PostgreSQL Global Development Group.
// Originally implemented by Dag Lem, sponsored by Finance Norway /
// Trafikkforsikringsforeningen. Chart adapted from
// https://www.jewishgen.org/InfoFiles/Soundex.html.
//
// Portions Copyright (c) 1996-2025, PostgreSQL Global Development Group
// Portions Copyright (c) 1994, The Regents of the University of California
//
// Permission to use, copy, modify, and distribute this software and its
// documentation for any purpose, without fee, and without a written agreement
// is hereby granted, provided that the above copyright notice and this
// paragraph and the following two paragraphs appear in all copies.
//
// IN NO EVENT SHALL THE UNIVERSITY OF CALIFORNIA BE LIABLE TO ANY PARTY FOR
// DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES, INCLUDING
// LOST PROFITS, ARISING OUT OF THE USE OF THIS SOFTWARE AND ITS
// DOCUMENTATION, EVEN IF THE UNIVERSITY OF CALIFORNIA HAS BEEN ADVISED OF THE
// POSSIBILITY OF SUCH DAMAGE.
//
// THE UNIVERSITY OF CALIFORNIA SPECIFICALLY DISCLAIMS ANY WARRANTIES,
// INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY
// AND FITNESS FOR A PARTICULAR PURPOSE. THE SOFTWARE PROVIDED HEREUNDER IS
// ON AN "AS IS" BASIS, AND THE UNIVERSITY OF CALIFORNIA HAS NO OBLIGATIONS TO
// PROVIDE MAINTENANCE, SUPPORT, UPDATES, ENHANCEMENTS, OR MODIFICATIONS.

import (
	"context"
	"strings"
	"unicode/utf8"
)

type dmCodes [2][3]string

type dmLetter struct {
	children    [29]int // A..], index zero means absent
	hasChildren bool
	codes       dmCodes
}

// Complete release chart, including UE's intentional unofficial 0/1/X rule.
var dmLetters = func() []dmLetter {
	tree := []dmLetter{{}}
	for _, rule := range []struct {
		letters string
		codes   dmCodes
	}{
		{"AI,AJ,AY", dmCodes{{"0", "1", "X"}}}, {"AU", dmCodes{{"0", "7", "X"}}},
		{"[", dmCodes{{"X", "X", "6"}, {"X", "X", "X"}}}, {"A", dmCodes{{"0", "X", "X"}}},
		{"B", dmCodes{{"7", "7", "7"}}}, {"CHS", dmCodes{{"5", "54", "54"}}},
		{"CH", dmCodes{{"5", "5", "5"}, {"4", "4", "4"}}}, {"CK", dmCodes{{"5", "5", "5"}, {"45", "45", "45"}}},
		{"CZ,CS,CSZ,CZS", dmCodes{{"4", "4", "4"}}}, {"C", dmCodes{{"5", "5", "5"}, {"4", "4", "4"}}},
		{"DRZ,DRS", dmCodes{{"4", "4", "4"}}}, {"DS,DSH,DSZ", dmCodes{{"4", "4", "4"}}},
		{"DZ,DZH,DZS", dmCodes{{"4", "4", "4"}}}, {"D,DT", dmCodes{{"3", "3", "3"}}},
		{"EI,EJ,EY", dmCodes{{"0", "1", "X"}}}, {"EU", dmCodes{{"1", "1", "X"}}},
		{"\\", dmCodes{{"X", "X", "6"}, {"X", "X", "X"}}}, {"E", dmCodes{{"0", "X", "X"}}},
		{"FB,F", dmCodes{{"7", "7", "7"}}}, {"G", dmCodes{{"5", "5", "5"}}}, {"H", dmCodes{{"5", "5", "X"}}},
		{"IA,IE,IO,IU", dmCodes{{"1", "X", "X"}}}, {"I", dmCodes{{"0", "X", "X"}}},
		{"J", dmCodes{{"1", "X", "X"}, {"4", "4", "4"}}}, {"KS", dmCodes{{"5", "54", "54"}}},
		{"KH,K", dmCodes{{"5", "5", "5"}}}, {"L", dmCodes{{"8", "8", "8"}}},
		{"MN", dmCodes{{"66", "66", "66"}}}, {"M", dmCodes{{"6", "6", "6"}}},
		{"NM", dmCodes{{"66", "66", "66"}}}, {"N", dmCodes{{"6", "6", "6"}}},
		{"OI,OJ,OY", dmCodes{{"0", "1", "X"}}}, {"O", dmCodes{{"0", "X", "X"}}},
		{"P,PF,PH", dmCodes{{"7", "7", "7"}}}, {"Q", dmCodes{{"5", "5", "5"}}},
		{"RZ,RS", dmCodes{{"94", "94", "94"}, {"4", "4", "4"}}}, {"R", dmCodes{{"9", "9", "9"}}},
		{"SCHTSCH,SCHTSH,SCHTCH", dmCodes{{"2", "4", "4"}}}, {"SCH", dmCodes{{"4", "4", "4"}}},
		{"SHTCH,SHCH,SHTSH", dmCodes{{"2", "4", "4"}}}, {"SHT,SCHT,SCHD", dmCodes{{"2", "43", "43"}}},
		{"SH", dmCodes{{"4", "4", "4"}}}, {"STCH,STSCH,SC", dmCodes{{"2", "4", "4"}}},
		{"STRZ,STRS,STSH", dmCodes{{"2", "4", "4"}}}, {"ST", dmCodes{{"2", "43", "43"}}},
		{"SZCZ,SZCS", dmCodes{{"2", "4", "4"}}}, {"SZT,SHD,SZD,SD", dmCodes{{"2", "43", "43"}}},
		{"SZ,S", dmCodes{{"4", "4", "4"}}}, {"TCH,TTCH,TTSCH", dmCodes{{"4", "4", "4"}}},
		{"TH", dmCodes{{"3", "3", "3"}}}, {"TRZ,TRS", dmCodes{{"4", "4", "4"}}},
		{"TSCH,TSH", dmCodes{{"4", "4", "4"}}}, {"TS,TTS,TTSZ,TC", dmCodes{{"4", "4", "4"}}},
		{"TZ,TTZ,TZS,TSZ", dmCodes{{"4", "4", "4"}}}, {"]", dmCodes{{"3", "3", "3"}, {"4", "4", "4"}}},
		{"T", dmCodes{{"3", "3", "3"}}}, {"UI,UJ,UY,UE", dmCodes{{"0", "1", "X"}}},
		{"U", dmCodes{{"0", "X", "X"}}}, {"V,W", dmCodes{{"7", "7", "7"}}},
		{"X", dmCodes{{"5", "54", "54"}}}, {"Y", dmCodes{{"1", "X", "X"}}},
		{"ZDZ,ZDZH,ZHDZH", dmCodes{{"2", "4", "4"}}}, {"ZD,ZHD", dmCodes{{"2", "43", "43"}}},
		{"ZH,ZS,ZSCH,ZSH,Z", dmCodes{{"4", "4", "4"}}},
	} {
		for alias := range strings.SplitSeq(rule.letters, ",") {
			n := 0
			for i := range len(alias) {
				c := alias[i] - 'A'
				if tree[n].children[c] == 0 {
					tree[n].hasChildren = true
					tree[n].children[c] = len(tree)
					tree = append(tree, dmLetter{})
				}
				n = tree[n].children[c]
			}
			tree[n].codes = rule.codes
		}
	}
	return tree
}()

const dmLatin1 = "`ABCDEFGHIJKLMNOPQRSTUVWXYZ{|}~                                  !                             ?AAAAAAECEEEEIIIIDNOOOOO*OUUUUYDSAAAAAAECEEEEIIIIDNOOOOO/OUUUUYDY"

func dmReadValid(ctx context.Context, word string, cursor *int) (byte, error) {
	for *cursor < len(word) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		r, size := utf8.DecodeRuneInString(word[*cursor:])
		*cursor += size
		var c byte
		switch {
		case r >= '[' && r <= ']':
			continue
		case r < 0x60:
			c = byte(r)
		case r < 0x100:
			c = dmLatin1[r-0x60]
		default:
			switch r {
			case 'Ą', 'ą':
				c = '['
			case 'Ę', 'ę':
				c = '\\'
			case 'Ţ', 'ţ', 'Ț', 'ț':
				c = ']'
			default:
				continue
			}
		}
		if c >= 'A' && c <= ']' {
			return c, nil
		}
	}
	return 0, nil
}

func dmReadLetter(ctx context.Context, word string, cursor *int) (*dmCodes, error) {
	c, err := dmReadValid(ctx, word, cursor)
	if err != nil || c == 0 {
		return nil, err
	}
	n := dmLetters[0].children[c-'A']
	codes := &dmLetters[n].codes
	i := *cursor
	for {
		// A terminal rule needs no lookahead (and does not rescan skipped text).
		children := &dmLetters[n].children
		if !dmLetters[n].hasChildren {
			break
		}
		c, err = dmReadValid(ctx, word, &i)
		if err != nil {
			return nil, err
		}
		if c == 0 || children[c-'A'] == 0 {
			break
		}
		n = children[c-'A']
		if dmLetters[n].codes[0][0] != "" {
			codes = &dmLetters[n].codes
			*cursor = i
		}
	}
	return codes, nil
}

type dmNode struct {
	code                   [6]byte
	length                 int
	children               [10]int
	next                   [2]int
	lastUpdate             int
	leaf                   bool
	prevDigits, nextDigits [2]byte
	prevIndex, nextIndex   int
}

type dmState struct {
	ctx         context.Context
	nodes       []dmNode // index zero is the absent link
	first, last [2]int
	output      []string
}

func (s *dmState) initialize(n, letter int) {
	p := &s.nodes[n]
	if p.lastUpdate < letter {
		p.prevDigits, p.nextDigits = p.nextDigits, [2]byte{}
		p.prevIndex, p.nextIndex = p.nextIndex, 0
		p.leaf, p.lastUpdate = false, letter
	}
}

func (s *dmState) child(parent int, digit byte) int {
	if n := s.nodes[parent].children[digit-'0']; n != 0 {
		if s.nodes[n].length < 6 {
			return n
		}
		return 0
	}
	p := &s.nodes[parent]
	n := dmNode{code: p.code, length: p.length, nextIndex: p.prevIndex}
	n.code[n.length] = digit
	n.length++
	index := len(s.nodes)
	p.children[digit-'0'] = index
	s.nodes = append(s.nodes, n)
	if n.length == 6 {
		s.output = append(s.output, string(n.code[:]))
		return 0
	}
	return index
}

func (s *dmState) update(node, ix, letter, prevIndex, nextIndex int, digits string, digitNo int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.initialize(node, letter)
	p := &s.nodes[node]
	if p.prevIndex != 0 && p.prevIndex&prevIndex == 0 {
		return nil
	}
	digit := digits[digitNo]
	var dirty [2]int
	count := 0
	if digit == 'X' || (digitNo == 0 && (p.prevDigits[0] == digit || p.prevDigits[1] == digit)) {
		dirty[count] = node
		count++
	}
	if digit != 'X' && (digitNo > 0 || p.prevDigits[0] != digit || p.prevDigits[1] != 0) {
		if child := s.child(node, digit); child != 0 {
			s.initialize(child, letter)
			dirty[count] = child
			count++
		}
	}
	for _, n := range dirty[:count] {
		p = &s.nodes[n]
		p.nextIndex |= nextIndex
		if p.nextDigits[0] == 0 {
			p.nextDigits[0] = digit
		} else if p.nextDigits[0] != digit {
			p.nextDigits[1] = digit
		}
		digitNo++ // Same sequential-digit advancement as the release algorithm.
		if digitNo < len(digits) {
			if err := s.update(n, ix, letter, prevIndex, nextIndex, digits, digitNo); err != nil {
				return err
			}
		} else if !p.leaf {
			p.leaf, p.next[ix] = true, 0
			if s.first[ix] == 0 {
				s.first[ix] = n
			} else {
				s.nodes[s.last[ix]].next[ix] = n
			}
			s.last[ix] = n
		}
	}
	return nil
}

// DaitchMokotoff returns complete ordered six-digit codes, or nil when no
// encodable phoneme exists (SQL NULL, not an empty array). State is call-local.
func DaitchMokotoff(ctx context.Context, word string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cursor := 0
	codes, err := dmReadLetter(ctx, word, &cursor)
	if err != nil || codes == nil {
		return nil, err
	}
	s := dmState{ctx: ctx, nodes: []dmNode{{}, {code: [6]byte{'0', '0', '0', '0', '0', '0'}}}, first: [2]int{1, 0}}
	ix := 0
	end := dmCodes{{"X", "X", "X"}}
	for letter := 0; codes != nil && s.first[ix] != 0; letter++ {
		next, err := dmReadLetter(ctx, word, &cursor)
		if err != nil {
			return nil, err
		}
		nextCodes := next
		if nextCodes == nil {
			nextCodes = &end
		}
		nextIX := (ix + 1) & 1
		s.first[nextIX], s.last[nextIX] = 0, 0
		for n := s.first[ix]; n != 0; n = s.nodes[n].next[ix] {
			for _, code := range codes {
				if code[0] == "" {
					break
				}
				prevIndex := 1
				if code[0][0] > '1' {
					prevIndex = 2
				}
				for _, nextCode := range nextCodes {
					if nextCode[0] == "" {
						break
					}
					column := 0
					if letter != 0 {
						column = 2
						if nextCode[0][0] <= '1' {
							column = 1
						}
					}
					if err := s.update(n, nextIX, letter, prevIndex, column, code[column], 0); err != nil {
						return nil, err
					}
				}
			}
		}
		ix, codes = nextIX, next
	}
	for n := s.first[ix]; n != 0; n = s.nodes[n].next[ix] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.output = append(s.output, string(s.nodes[n].code[:]))
	}
	return s.output, nil
}
