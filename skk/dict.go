// Package skk は SKK 辞書のパース・マージ・整形を扱う。
package skk

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

type Section int

const (
	OkuriAri Section = iota
	OkuriNasi
)

const (
	okuriAriMarker  = ";; okuri-ari entries."
	okuriNasiMarker = ";; okuri-nasi entries."
)

// Candidate は変換候補 1 つ。注釈は ';' 以降の部分を ';' なしで保持する。
type Candidate struct {
	Word       string
	Annotation string
}

func (c Candidate) String() string {
	if c.Annotation == "" {
		return c.Word
	}
	return c.Word + ";" + c.Annotation
}

// OkuriBlock は送りありエントリ内の "[る/走/]" のような送り仮名ごとの候補ブロック。
type OkuriBlock struct {
	Okuri      string
	Candidates []Candidate
}

type Entry struct {
	Headword   string
	Candidates []Candidate
	Blocks     []OkuriBlock
}

func (e *Entry) String() string {
	var b strings.Builder
	b.WriteString(e.Headword)
	b.WriteString(" /")
	for _, c := range e.Candidates {
		b.WriteString(c.String())
		b.WriteByte('/')
	}
	for _, blk := range e.Blocks {
		b.WriteByte('[')
		b.WriteString(blk.Okuri)
		b.WriteByte('/')
		for _, c := range blk.Candidates {
			b.WriteString(c.String())
			b.WriteByte('/')
		}
		b.WriteString("]/")
	}
	return b.String()
}

func (e *Entry) clone() *Entry {
	n := &Entry{Headword: e.Headword, Candidates: append([]Candidate(nil), e.Candidates...)}
	for _, blk := range e.Blocks {
		n.Blocks = append(n.Blocks, OkuriBlock{Okuri: blk.Okuri, Candidates: append([]Candidate(nil), blk.Candidates...)})
	}
	return n
}

// sort は候補を語順、送りブロックを送り仮名順に並べる。
func (e *Entry) sort() {
	sortCandidates(e.Candidates)
	sort.Slice(e.Blocks, func(i, j int) bool { return e.Blocks[i].Okuri < e.Blocks[j].Okuri })
	for _, blk := range e.Blocks {
		sortCandidates(blk.Candidates)
	}
}

func sortCandidates(cs []Candidate) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].Word < cs[j].Word })
}

// Dict は送りあり・送りなしそれぞれの見出し語 → エントリの対応を持つ。
type Dict struct {
	sections [2]map[string]*Entry
}

func New() *Dict {
	return &Dict{sections: [2]map[string]*Entry{{}, {}}}
}

func (d *Dict) Lookup(sec Section, headword string) *Entry {
	return d.sections[sec][headword]
}

// Len は見出し語の総数を返す。
func (d *Dict) Len() int {
	return len(d.sections[OkuriAri]) + len(d.sections[OkuriNasi])
}

// Entries は SKK の慣習どおり、送りありは降順・送りなしは昇順に並べたエントリを返す。
func (d *Dict) Entries(sec Section) []*Entry {
	es := make([]*Entry, 0, len(d.sections[sec]))
	for _, e := range d.sections[sec] {
		es = append(es, e)
	}
	sort.Slice(es, func(i, j int) bool {
		if sec == OkuriAri {
			return es[i].Headword > es[j].Headword
		}
		return es[i].Headword < es[j].Headword
	})
	return es
}

// add はエントリを取り込む。同じ見出し語が既にあれば候補を統合する。
// 取り込み順で候補の並びが変わらないよう、候補と送りブロックは常にソートしておく。
func (d *Dict) add(sec Section, e *Entry) {
	cur, ok := d.sections[sec][e.Headword]
	if !ok {
		cur = e.clone()
		d.sections[sec][e.Headword] = cur
		cur.sort()
		return
	}
	defer cur.sort()
	cur.Candidates = mergeCandidates(cur.Candidates, e.Candidates)
	for _, blk := range e.Blocks {
		merged := false
		for i := range cur.Blocks {
			if cur.Blocks[i].Okuri == blk.Okuri {
				cur.Blocks[i].Candidates = mergeCandidates(cur.Blocks[i].Candidates, blk.Candidates)
				merged = true
				break
			}
		}
		if !merged {
			cur.Blocks = append(cur.Blocks, OkuriBlock{Okuri: blk.Okuri, Candidates: append([]Candidate(nil), blk.Candidates...)})
		}
	}
}

// mergeCandidates は dst の順序を保ったまま src にしかない候補を末尾に足す。
// 同じ語で dst 側に注釈がなく src 側にある場合は注釈を引き継ぐ。
func mergeCandidates(dst, src []Candidate) []Candidate {
	for _, c := range src {
		found := false
		for i := range dst {
			if dst[i].Word == c.Word {
				if dst[i].Annotation == "" {
					dst[i].Annotation = c.Annotation
				}
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, c)
		}
	}
	return dst
}

// Merge は o の内容を d に取り込む。同じ語の注釈は d が優先される。
func (d *Dict) Merge(o *Dict) {
	for sec := range o.sections {
		for _, e := range o.sections[sec] {
			d.add(Section(sec), e)
		}
	}
}

// Parse は UTF-8 にデコード済みの辞書テキストを読む。
// ";; okuri-ari entries." などのマーカーがあればそれに従い、
// マーカーより前の行は見出し語の形から送りあり／なしを判定する。
func Parse(text string) (*Dict, error) {
	d := New()
	sec := Section(-1)
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r \t")
		switch {
		case line == okuriAriMarker:
			sec = OkuriAri
			continue
		case line == okuriNasiMarker:
			sec = OkuriNasi
			continue
		case line == "" || strings.HasPrefix(line, ";"):
			continue
		}
		idx := strings.Index(line, " /")
		if idx <= 0 || strings.ContainsAny(line[:idx], " \t") {
			return nil, fmt.Errorf("%d 行目: SKK 辞書のエントリとして読めません: %q", i+1, truncate(line, 40))
		}
		headword := line[:idx]
		s := sec
		if s < 0 {
			s = guessSection(headword)
		}
		// 送りブロック "[る/走/]" は送りありエントリにしか現れない
		e := parseEntry(headword, line[idx+1:], s == OkuriAri)
		if len(e.Candidates) == 0 && len(e.Blocks) == 0 {
			continue
		}
		d.add(s, e)
	}
	return d, nil
}

func parseEntry(headword, body string, allowBlocks bool) *Entry {
	e := &Entry{Headword: headword}
	s := strings.TrimPrefix(body, "/")
	for len(s) > 0 {
		if allowBlocks && s[0] == '[' {
			if end := strings.Index(s, "/]"); end > 0 {
				parts := strings.Split(s[1:end], "/")
				blk := OkuriBlock{Okuri: parts[0]}
				for _, p := range parts[1:] {
					if p != "" {
						blk.Candidates = mergeCandidates(blk.Candidates, []Candidate{parseCandidate(p)})
					}
				}
				if len(blk.Candidates) > 0 {
					e.Blocks = append(e.Blocks, blk)
				}
				s = strings.TrimPrefix(s[end+2:], "/")
				continue
			}
		}
		tok := s
		if i := strings.IndexByte(s, '/'); i >= 0 {
			tok, s = s[:i], s[i+1:]
		} else {
			s = ""
		}
		if tok != "" {
			e.Candidates = mergeCandidates(e.Candidates, []Candidate{parseCandidate(tok)})
		}
	}
	return e
}

func parseCandidate(s string) Candidate {
	if i := strings.IndexByte(s, ';'); i >= 0 {
		return Candidate{Word: s[:i], Annotation: s[i+1:]}
	}
	return Candidate{Word: s}
}

// guessSection は "かk" のように、かな等の後ろに ASCII 小文字が 1 文字付いた見出し語を送りありとみなす。
func guessSection(headword string) Section {
	last, n := utf8.DecodeLastRuneInString(headword)
	if last < 'a' || last > 'z' {
		return OkuriNasi
	}
	prev, _ := utf8.DecodeLastRuneInString(headword[:len(headword)-n])
	if prev < utf8.RuneSelf {
		return OkuriNasi
	}
	return OkuriAri
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// Format は UTF-8 の辞書テキストとして書き出す。
func (d *Dict) Format() string {
	return d.format(";; -*- mode: fundamental; coding: utf-8 -*-")
}

func (d *Dict) format(header string) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(okuriAriMarker)
	b.WriteByte('\n')
	for _, e := range d.Entries(OkuriAri) {
		b.WriteString(e.String())
		b.WriteByte('\n')
	}
	b.WriteString(okuriNasiMarker)
	b.WriteByte('\n')
	for _, e := range d.Entries(OkuriNasi) {
		b.WriteString(e.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// filter は keep が false を返す文字列を含む候補・エントリを除いた複製を返す。
// 2 つ目の戻り値は除いた候補の数。注釈だけが該当する場合は注釈のみ落とす。
func (d *Dict) filter(keep func(string) bool) (*Dict, int) {
	n := New()
	dropped := 0
	filterCands := func(cs []Candidate) []Candidate {
		var out []Candidate
		for _, c := range cs {
			if !keep(c.Word) {
				dropped++
				continue
			}
			if c.Annotation != "" && !keep(c.Annotation) {
				c.Annotation = ""
			}
			out = append(out, c)
		}
		return out
	}
	for sec := range d.sections {
		for h, e := range d.sections[sec] {
			if !keep(h) {
				dropped += len(e.Candidates)
				continue
			}
			ne := &Entry{Headword: h, Candidates: filterCands(e.Candidates)}
			for _, blk := range e.Blocks {
				if !keep(blk.Okuri) {
					dropped += len(blk.Candidates)
					continue
				}
				if cs := filterCands(blk.Candidates); len(cs) > 0 {
					ne.Blocks = append(ne.Blocks, OkuriBlock{Okuri: blk.Okuri, Candidates: cs})
				}
			}
			if len(ne.Candidates) > 0 {
				n.sections[sec][h] = ne
			} else {
				for _, blk := range ne.Blocks {
					dropped += len(blk.Candidates)
				}
			}
		}
	}
	return n, dropped
}
