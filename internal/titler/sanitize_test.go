package titler

import (
	"strings"
	"testing"
)

func TestSanitizeTitle(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "DC1申請書のファイル探し", "DC1申請書のファイル探し"},
		{"label prefix", "タイトル: 開発ロードマップの方針相談", "開発ロードマップの方針相談"},
		{"label prefix wa", "タイトルは寄付支援方法の調査", "寄付支援方法の調査"},
		{"markdown heading", "# セッション概要のまとめ", "セッション概要のまとめ"},
		{"think block then answer", "<think>ユーザーは…と考えている</think>\nVR風呂体験の検討", "VR風呂体験の検討"},
		{"unterminated think", "<think>考え込んで答えが無い", ""},
		{"surrounding quotes", "「寄付支援方法の調査」", "寄付支援方法の調査"},
		{"trailing punct", "プロジェクト確認と実施。", "プロジェクト確認と実施"},
		{"metaword discarded", "セッション一覧", ""},
		{"empty discarded", "", ""},
		{"romaji path leak discarded", "/Users/eiyuto/Documents/", ""},
		{"arrow lead stripped", "→ Next.jsのProxy設定", "Next.jsのProxy設定"},
		{"first non-empty line", "\n\n  寄付支援方法の調査\n余計な行", "寄付支援方法の調査"},
		{"over-length trimmed", strings.Repeat("あ", 25), strings.Repeat("あ", 20)},
	}
	for _, c := range cases {
		if got := sanitizeTitle(c.in); got != c.want {
			t.Errorf("%s:\n  sanitizeTitle(%q)\n  = %q\n  want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestIsUsableTitle(t *testing.T) {
	usable := []string{"VR風呂体験の検討", "Vozの用途確認", "Next.jsのProxy設定", "Voz"}
	for _, s := range usable {
		if !isUsableTitle(s) {
			t.Errorf("expected usable: %q", s)
		}
	}
	unusable := []string{"", "役割", "セッション一覧", "====", "/Users/eiyuto/Documents/", `C:\Users\x`}
	for _, s := range unusable {
		if isUsableTitle(s) {
			t.Errorf("expected unusable: %q", s)
		}
	}
}
