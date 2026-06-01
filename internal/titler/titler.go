package titler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// maxTitleRunes caps a generated title so it stays a one-line headline. The
// prompt asks for <=20 chars; this is the hard backstop after sanitizing.
const maxTitleRunes = 20

var (
	thinkRegex    = regexp.MustCompile(`(?is)<think>.*?</think>`)
	spaceRegex    = regexp.MustCompile(`\s+`)
	labelPrefixRe = regexp.MustCompile(`^\s*(タイトル|Title|title|見出し)\s*[:：はが]\s*`)
	leadSymbolRe  = regexp.MustCompile(`^[#*\-・>→\s]+`)

	// metaWords are empty-of-content responses the model emits when it has
	// nothing useful to say; they should be discarded so the caller falls back.
	metaWords = map[string]bool{
		"セッション一覧": true, "セッション概要": true, "セッション": true,
		"役割": true, "タイトル": true, "無題": true,
	}
)

// promptTemplate is the instruction sent to ollama, selected by the
// kioku-titler-rnd bake-off (qwen3:8b/few-shot won). The literal "{ctx}" is
// replaced with the session's opening text. Override the model/endpoint with
// env vars, not this string.
const promptTemplate = `あなたはClaude Codeの作業セッションに日本語の短いタイトルを付けるアシスタントです。

以下のセッション内容を読み、その作業を最もよく表す日本語のタイトルを1つだけ出力してください。

ルール:
- 必ず自然な日本語で書く。英語・ローマ字・ファイルパスはそのまま使わず、内容を日本語に言い換える（固有の製品名・コマンド名はそのまま残してよい）。
- 20文字以内。長くしない。
- 体言止め（名詞句）で簡潔に。文末の句点や「〜する」は不要。
- 記号・引用符・絵文字・Markdown記法（#, *, - など）・矢印（→）を一切使わない。
- 「タイトル:」「タイトルは」などの前置きや説明、思考過程を出力しない。タイトルだけを1行で出力する。
- 内容が読み取れない、またはサブエージェント／計画用のプロンプトで作業実体が無い場合でも、内容から推測できる最も簡潔な名詞句を出す。

例:
内容: Roadmap.mdを読んで今後の開発方針を相談した
タイトル: 開発ロードマップの方針相談

内容: DC1申請書のWordファイルがどこにあるか探した
タイトル: DC1申請書のファイル探し

内容: にじいろダイバーシティへの寄付・支援方法を調べた
タイトル: 寄付支援方法の調査

ここからが対象のセッション内容です:
{ctx}

タイトル:`

// Generator talks to a local ollama instance to produce session titles.
type Generator struct {
	Model    string
	Endpoint string
	client   *http.Client
}

// New builds a Generator from the environment. KIOKU_OLLAMA_MODEL selects the
// model (default qwen3:8b, the bake-off winner); KIOKU_OLLAMA_URL the endpoint
// (default http://localhost:11434).
func New() *Generator {
	model := os.Getenv("KIOKU_OLLAMA_MODEL")
	if model == "" {
		model = "qwen3:8b"
	}
	endpoint := os.Getenv("KIOKU_OLLAMA_URL")
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	return &Generator{
		Model:    model,
		Endpoint: strings.TrimRight(endpoint, "/"),
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

// Available reports whether the ollama endpoint answers within a short timeout.
// When false, callers skip generation entirely (no error, no crash).
func (g *Generator) Available(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.Endpoint+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type genRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Stream  bool           `json:"stream"`
	Think   bool           `json:"think"` // disable reasoning output (qwen3 etc.); harmless for non-thinking models
	Options map[string]any `json:"options,omitempty"`
}

type genResponse struct {
	Response string `json:"response"`
	Error    string `json:"error"`
}

// Generate returns a short Japanese title for the given session context, or an
// error if ollama is unreachable, returns an error, or produces nothing usable.
func (g *Generator) Generate(ctx context.Context, contextText string) (string, error) {
	contextText = strings.TrimSpace(contextText)
	if contextText == "" {
		return "", fmt.Errorf("empty context")
	}

	prompt := strings.Replace(promptTemplate, "{ctx}", contextText, 1)

	body, err := json.Marshal(genRequest{
		Model:  g.Model,
		Prompt: prompt,
		Stream: false,
		Think:  false, // without this, qwen3 spends the whole token budget on reasoning
		Options: map[string]any{
			"temperature": 0.3,
			"num_predict": 64,
			"top_p":       0.9,
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.Endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("ollama status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var out genResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf("ollama: %s", out.Error)
	}

	title := sanitizeTitle(out.Response)
	if title == "" {
		return "", fmt.Errorf("no usable title")
	}
	return title, nil
}

// sanitizeTitle reduces a raw model response to a single clean headline line:
// it drops <think> reasoning (even unterminated), label prefixes, leading
// markdown/bullet/arrow symbols, surrounding quotes, and caps the length;
// then rejects empty/meta/garbage results so the caller can fall back.
func sanitizeTitle(raw string) string {
	s := thinkRegex.ReplaceAllString(raw, "")
	if i := strings.Index(s, "<think>"); i >= 0 {
		s = s[:i] // unterminated reasoning block: drop it and everything after
	}
	s = strings.ReplaceAll(s, "</think>", "")

	for _, line := range strings.Split(s, "\n") {
		if t := cleanTitleLine(line); isUsableTitle(t) {
			return t
		}
	}
	return ""
}

func cleanTitleLine(line string) string {
	t := strings.TrimSpace(line)
	t = labelPrefixRe.ReplaceAllString(t, "")
	t = leadSymbolRe.ReplaceAllString(t, "")
	t = strings.Trim(t, " \t\"'`「」『』【】")
	t = strings.TrimSpace(spaceRegex.ReplaceAllString(t, " "))
	t = strings.TrimRight(t, "。、.,!！?？　 ")
	if r := []rune(t); len(r) > maxTitleRunes {
		t = strings.TrimSpace(string(r[:maxTitleRunes]))
	}
	return t
}

// isUsableTitle rejects empties, content-free meta words, letter-less junk, and
// non-Japanese path/code leakage so the caller can fall back to aiTitle or the
// user's first message.
func isUsableTitle(t string) bool {
	if t == "" || metaWords[t] {
		return false
	}
	hasLetter, hasCJK := false, false
	for _, r := range t {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if isCJK(r) {
			hasCJK = true
		}
	}
	if !hasLetter {
		return false
	}
	// A title with no Japanese that still looks like a path or code fragment is
	// a generation failure for a Japanese-preferring user; drop it.
	if !hasCJK && strings.ContainsAny(t, "/\\") {
		return false
	}
	return true
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Han, r)
}
