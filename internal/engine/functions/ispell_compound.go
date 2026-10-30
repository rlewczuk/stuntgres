package functions

import "strings"

type ispellVariant struct {
	stems []string
	next  *ispellVariant
}
type ispellSplitFrame struct {
	node              *ispellNode
	start, level, min int
	probed            []bool
	head, tail        *ispellVariant
	affix             int
	phase             uint8 // 0: new level, 1: affix alternatives, 2: longer-word return.
}

func (r *ispellNormalization) splitFrame(word string, node *ispellNode, stems []string, start, min int) ispellSplitFrame {
	level := min
	if node == nil {
		node = &r.d.root
		level = start
	}
	owned := append([]string(nil), stems...)
	variant := &ispellVariant{stems: owned}
	return ispellSplitFrame{node: node, start: start, level: level, min: min, probed: make([]bool, len(word)), head: variant, tail: variant}
}

// split ports SplitToVariants' DFS and list insertion order. Explicit frames
// replace C recursion, so an arbitrarily long input cannot consume the Go call
// stack. Greedy stems remain the first variant; longer-word and epenthetic
// alternatives are appended in released trie/affix order, never sorted/deduped.
func (r *ispellNormalization) split(word string) (*ispellVariant, error) {
	if word == "" {
		return nil, r.ctx.Err()
	}
	stack := []ispellSplitFrame{r.splitFrame(word, nil, nil, 0, -1)}
	var completed *ispellVariant
	for len(stack) > 0 {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		f := &stack[len(stack)-1]
		if completed != nil {
			f.tail.next = completed
			for f.tail.next != nil {
				if err := r.ctx.Err(); err != nil {
					return nil, err
				}
				f.tail = f.tail.next
			}
			completed = nil
			if f.phase == 2 {
				f.level++
				f.head.stems = append(f.head.stems, word[f.start:f.level])
				f.start = f.level
				f.node = &r.d.root
				f.phase = 0
			}
		}
		if f.level >= len(word) {
			f.head.stems = append(f.head.stems, word[f.start:])
			completed = f.head
			stack = stack[:len(stack)-1]
			continue
		}
		if f.phase == 0 {
			f.affix = 0
			f.phase = 1
		}
		pushed := false
		for f.level > f.start && f.affix < len(r.d.compoundAffixes) {
			if err := r.ctx.Err(); err != nil {
				return nil, err
			}
			a := r.d.compoundAffixes[f.affix]
			f.affix++
			rest := word[f.level:]
			if len(rest) <= len(a.text) {
				continue
			}
			at := 0
			if f.node != nil {
				if !strings.HasPrefix(rest, a.text) {
					continue
				}
			} else {
				at = strings.Index(rest, a.text)
				if at < 0 {
					continue
				}
			}
			length := 0
			if a.suffix {
				length = len(a.text) + at
			}
			length += f.level - f.start
			end := f.start + length
			if end <= 0 || end > len(word) || f.probed[end-1] || f.level+length-1 <= f.min || length >= ispellMaxWord {
				continue
			}
			position := ispellMiddle
			if f.level == 0 {
				position = ispellBegin
			} else if f.level == len(word)-1 {
				position = ispellEnd
			}
			forms, err := r.subword(word[f.start:end], position)
			if err != nil {
				return nil, err
			}
			if len(forms) == 0 {
				continue
			}
			f.probed[end-1] = true
			child := r.splitFrame(word, nil, nil, end, end)
			child.head.stems = make([]string, len(f.head.stems), len(f.head.stems)+len(forms))
			copy(child.head.stems, f.head.stems)
			child.head.stems = append(child.head.stems, forms...)
			stack = append(stack, child)
			pushed = true
			break
		}
		if pushed {
			continue
		}
		if f.node == nil {
			f.head.stems = append(f.head.stems, word[f.start:])
			completed = f.head
			stack = stack[:len(stack)-1]
			continue
		}
		child := f.node.children[word[f.level]]
		if child != nil {
			position := ispellMiddle
			if f.start == 0 {
				position = ispellBegin
			} else if f.level == len(word)-1 {
				position = ispellEnd
			}
			if child.word && child.compound&position != 0 && !f.probed[f.level] && f.level > f.min {
				if f.level == len(word)-1 {
					f.head.stems = append(f.head.stems, word[f.start:])
					completed = f.head
					stack = stack[:len(stack)-1]
					continue
				}
				f.phase = 2
				alternative := r.splitFrame(word, f.node, f.head.stems, f.start, f.level)
				stack = append(stack, alternative)
				continue
			}
		}
		f.node = child
		f.level++
		f.phase = 0
	}
	return completed, r.ctx.Err()
}
