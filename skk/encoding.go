package skk

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
)

var codingRe = regexp.MustCompile(`coding:\s*([A-Za-z0-9_-]+)`)

// Decode は辞書ファイルのバイト列を UTF-8 文字列にする。
// UTF-16 の BOM があれば UTF-16 として読む (CorvusSKK のユーザー辞書は UTF-16LE)。
// それ以外は 1 行目の "coding:" 指定を優先し、指定がなければ UTF-8 として妥当かどうかで EUC-JP と判別する。
func Decode(b []byte) (string, error) {
	if bytes.HasPrefix(b, []byte("\xff\xfe")) || bytes.HasPrefix(b, []byte("\xfe\xff")) {
		s, err := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder().String(string(b))
		if err != nil {
			return "", fmt.Errorf("文字コードの変換に失敗しました: %w", err)
		}
		if n := strings.Count(s, "�"); n > 0 {
			return "", fmt.Errorf("UTF-16 として解釈できない文字が %d 箇所あります", n)
		}
		return normalizeNewlines(s), nil
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	first, _, _ := bytes.Cut(b, []byte("\n"))
	coding := ""
	if m := codingRe.FindSubmatch(first); m != nil {
		coding = strings.ToLower(string(m[1]))
		for _, suffix := range []string{"-unix", "-dos", "-mac"} {
			coding = strings.TrimSuffix(coding, suffix)
		}
	}

	var enc encoding.Encoding
	switch coding {
	case "utf-8", "utf8", "utf-8-with-signature":
		if !utf8.Valid(b) {
			return "", fmt.Errorf("coding: utf-8 と指定されていますが UTF-8 として不正なバイト列があります")
		}
	case "euc-jp", "euc-japan", "euc-jis-2004", "euc-jisx0213", "japanese-iso-8bit":
		enc = japanese.EUCJP
	case "shift_jis", "sjis", "cp932", "japanese-shift-jis":
		enc = japanese.ShiftJIS
	case "":
		if !utf8.Valid(b) {
			enc = japanese.EUCJP
		}
	default:
		return "", fmt.Errorf("未対応の文字コードです: %s", coding)
	}
	if enc == nil {
		return normalizeNewlines(string(b)), nil
	}
	s, err := enc.NewDecoder().String(string(b))
	if err != nil {
		return "", fmt.Errorf("文字コードの変換に失敗しました: %w", err)
	}
	if n := strings.Count(s, "�"); n > 0 {
		return "", fmt.Errorf("%s として解釈できない文字が %d 箇所あります", coding, n)
	}
	return normalizeNewlines(s), nil
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// EncodeEUCJP は辞書を EUC-JP で書き出す。EUC-JP で表せない候補は除き、その数を返す。
func EncodeEUCJP(d *Dict) ([]byte, int, error) {
	enc := japanese.EUCJP.NewEncoder()
	representable := func(s string) bool {
		_, err := enc.String(s)
		return err == nil
	}
	filtered, dropped := d.filter(representable)
	header := ";; -*- mode: fundamental; coding: euc-jp -*-"
	if dropped > 0 {
		header += fmt.Sprintf("\n;; EUC-JP で表せない候補 %d 件を省略しています", dropped)
	}
	out, err := enc.String(filtered.format(header))
	if err != nil {
		return nil, 0, err
	}
	return []byte(out), dropped, nil
}
