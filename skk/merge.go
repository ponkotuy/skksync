package skk

import "sort"

// Subtract は base（SKK-JISYO.L など）に同じ見出し語・同じ語で載っている候補を d から取り除く。
// 候補がなくなったエントリは見出し語ごと消す。戻り値は取り除いた候補の数。
func (d *Dict) Subtract(base *Dict) int {
	removed := 0
	for sec := range d.sections {
		for h, e := range d.sections[sec] {
			be := base.sections[sec][h]
			if be == nil {
				continue
			}
			words := make(map[string]struct{}, len(be.Candidates))
			for _, c := range be.Candidates {
				words[c.Word] = struct{}{}
			}
			drop := func(cs []Candidate) []Candidate {
				out := cs[:0]
				for _, c := range cs {
					if _, ok := words[c.Word]; ok {
						removed++
						continue
					}
					out = append(out, c)
				}
				return out
			}
			e.Candidates = drop(e.Candidates)
			blocks := e.Blocks[:0]
			for _, blk := range e.Blocks {
				if blk.Candidates = drop(blk.Candidates); len(blk.Candidates) > 0 {
					blocks = append(blocks, blk)
				}
			}
			e.Blocks = blocks
			// 送りブロックは本体の候補の補足情報なので、本体が空になったら一緒に消す
			if len(e.Candidates) == 0 {
				for _, blk := range e.Blocks {
					removed += len(blk.Candidates)
				}
				delete(d.sections[sec], h)
			}
		}
	}
	return removed
}

type Change struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// Diff はエントリ単位の差分。各要素は辞書の 1 行。
type Diff struct {
	Added   []string `json:"added"`
	Changed []Change `json:"changed"`
	Removed []string `json:"removed"`
}

func Compare(before, after *Dict) Diff {
	var diff Diff
	for sec := range after.sections {
		for h, ae := range after.sections[sec] {
			be := before.sections[sec][h]
			switch {
			case be == nil:
				diff.Added = append(diff.Added, ae.String())
			case be.String() != ae.String():
				diff.Changed = append(diff.Changed, Change{Before: be.String(), After: ae.String()})
			}
		}
		for h, be := range before.sections[sec] {
			if after.sections[sec][h] == nil {
				diff.Removed = append(diff.Removed, be.String())
			}
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Slice(diff.Changed, func(i, j int) bool { return diff.Changed[i].After < diff.Changed[j].After })
	return diff
}
