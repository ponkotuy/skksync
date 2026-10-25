package skk

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
)

func mustParse(t *testing.T, text string) *Dict {
	t.Helper()
	d, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseAndFormat(t *testing.T) {
	d := mustParse(t, strings.Join([]string{
		";; -*- coding: utf-8 -*-",
		";; okuri-ari entries.",
		"はしr /走;run/奔/[る/走/]/[ら/走/奔/]/",
		"あいs /愛/",
		";; okuri-nasi entries.",
		"かっこ /(/[/「/",
		"かんじ /漢字;[注釈]/感じ/漢字/",
		"",
	}, "\n"))

	want := strings.Join([]string{
		";; -*- mode: fundamental; coding: utf-8 -*-",
		";; okuri-ari entries.",
		"はしr /奔/走;run/[ら/奔/走/]/[る/走/]/",
		"あいs /愛/",
		";; okuri-nasi entries.",
		"かっこ /(/[/「/",
		"かんじ /感じ/漢字;[注釈]/",
		"",
	}, "\n")
	if got := d.Format(); got != want {
		t.Errorf("Format mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseWithoutMarkersAndUnsorted(t *testing.T) {
	d := mustParse(t, "わたしw /私/\nうえ /上/\nあb /亜/\nabc /ABC/\nあいう /藍宇/\n")
	want := strings.Join([]string{
		";; -*- mode: fundamental; coding: utf-8 -*-",
		";; okuri-ari entries.",
		"わたしw /私/",
		"あb /亜/",
		";; okuri-nasi entries.",
		"abc /ABC/",
		"あいう /藍宇/",
		"うえ /上/",
		"",
	}, "\n")
	if got := d.Format(); got != want {
		t.Errorf("Format mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseError(t *testing.T) {
	if _, err := Parse(";; okuri-nasi entries.\nこれは壊れた行\n"); err == nil || !strings.Contains(err.Error(), "2 行目") {
		t.Errorf("expected line error, got %v", err)
	}
}

func TestMergeOrderAndAnnotation(t *testing.T) {
	local := mustParse(t, ";; okuri-nasi entries.\nかんじ /感じ/漢字/\n")
	gist := mustParse(t, ";; okuri-nasi entries.\nかんじ /漢字;注釈/幹事/\nほげ /保下/\n")
	merged := New()
	merged.Merge(local)
	merged.Merge(gist)

	if got, want := merged.Lookup(OkuriNasi, "かんじ").String(), "かんじ /幹事/感じ/漢字;注釈/"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if merged.Lookup(OkuriNasi, "ほげ") == nil {
		t.Error("gist only entry is missing")
	}
	// マージ元を変更していないこと
	if got := local.Lookup(OkuriNasi, "かんじ").String(); got != "かんじ /感じ/漢字/" {
		t.Errorf("source dict was modified: %q", got)
	}
}

func TestMergeBlocks(t *testing.T) {
	a := mustParse(t, ";; okuri-ari entries.\nはしr /走/[る/走/]/\n")
	b := mustParse(t, ";; okuri-ari entries.\nはしr /奔/[る/奔/]/[ら/走/]/\n")
	a.Merge(b)
	if got, want := a.Lookup(OkuriAri, "はしr").String(), "はしr /奔/走/[ら/走/]/[る/奔/走/]/"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMergeOrderIndependent(t *testing.T) {
	a := mustParse(t, ";; okuri-ari entries.\nはしr /走/奔/[る/走/]/[ら/走/]/\n;; okuri-nasi entries.\nかんじ /漢字/感じ/\n")
	b := mustParse(t, ";; okuri-ari entries.\nはしr /奔/走/[ら/走/]/[る/走/]/\n;; okuri-nasi entries.\nかんじ /感じ/漢字/幹事/\n")
	ab, ba := New(), New()
	ab.Merge(a)
	ab.Merge(b)
	ba.Merge(b)
	ba.Merge(a)
	if ab.Format() != ba.Format() {
		t.Errorf("merge result depends on order\n a+b:\n%s\n b+a:\n%s", ab.Format(), ba.Format())
	}
}

func TestSubtract(t *testing.T) {
	base := mustParse(t, ";; okuri-ari entries.\nはしr /走;注釈/\n;; okuri-nasi entries.\nかんじ /漢字/感じ/\nあい /愛/\n")
	d := mustParse(t, ";; okuri-ari entries.\nはしr /走/[る/走/]/\n;; okuri-nasi entries.\nかんじ /幹事/漢字/\nあい /愛;love/\nほげ /保下/\n")

	if removed := d.Subtract(base); removed != 4 {
		t.Errorf("removed = %d, want 4", removed)
	}
	want := strings.Join([]string{
		";; -*- mode: fundamental; coding: utf-8 -*-",
		";; okuri-ari entries.",
		";; okuri-nasi entries.",
		"かんじ /幹事/",
		"ほげ /保下/",
		"",
	}, "\n")
	if got := d.Format(); got != want {
		t.Errorf("Format mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCompare(t *testing.T) {
	before := mustParse(t, ";; okuri-nasi entries.\nあ /亜/\nい /位/\n")
	after := mustParse(t, ";; okuri-nasi entries.\nあ /亜/阿/\nう /宇/\n")
	diff := Compare(before, after)
	if len(diff.Added) != 1 || diff.Added[0] != "う /宇/" {
		t.Errorf("Added = %v", diff.Added)
	}
	if len(diff.Changed) != 1 || diff.Changed[0] != (Change{Before: "あ /亜/", After: "あ /亜/阿/"}) {
		t.Errorf("Changed = %v", diff.Changed)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != "い /位/" {
		t.Errorf("Removed = %v", diff.Removed)
	}
}

func TestDecode(t *testing.T) {
	text := ";; okuri-nasi entries.\nかんじ /漢字/\n"
	euc, _ := japanese.EUCJP.NewEncoder().String(text)
	crlf := strings.ReplaceAll(text, "\n", "\r\n")
	utf16le, _ := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().String(crlf)
	utf16be, _ := unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewEncoder().String(crlf)

	cases := []struct {
		name  string
		input []byte
	}{
		{"utf-8 without header", []byte(text)},
		{"utf-8 with BOM and CRLF", []byte("\xef\xbb\xbf" + strings.ReplaceAll(text, "\n", "\r\n"))},
		{"euc-jp without header", []byte(euc)},
		{"euc-jp with header", []byte(";; -*- coding: euc-jp -*-\n" + euc)},
		{"utf-16le with BOM and CRLF", []byte(utf16le)},
		{"utf-16be with BOM and CRLF", []byte(utf16be)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Decode(c.input)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(got, text) {
				t.Errorf("got %q", got)
			}
		})
	}

	if _, err := Decode([]byte(";; -*- coding: utf-8 -*-\n" + euc)); err == nil {
		t.Error("expected error for EUC-JP bytes declared as utf-8")
	}
}

func TestEncodeEUCJP(t *testing.T) {
	d := mustParse(t, ";; okuri-nasi entries.\nかんじ /漢字;😀/😀/\nえもじ /😀/\n😀 /smile/\n")
	out, dropped, err := EncodeEUCJP(d)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 3 {
		t.Errorf("dropped = %d, want 3", dropped)
	}
	decoded, err := Decode(out)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		";; -*- mode: fundamental; coding: euc-jp -*-",
		";; EUC-JP で表せない候補 3 件を省略しています",
		";; okuri-ari entries.",
		";; okuri-nasi entries.",
		"かんじ /漢字/",
		"",
	}, "\n")
	if decoded != want {
		t.Errorf("got:\n%s\nwant:\n%s", decoded, want)
	}
}
