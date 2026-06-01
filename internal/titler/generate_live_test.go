package titler

import (
	"context"
	"testing"
)

// TestGenerateLive hits a real local ollama if one is reachable, so the chosen
// model+prompt can be eyeballed against representative inputs. It is skipped
// when ollama is unavailable, so CI without ollama stays green.
func TestGenerateLive(t *testing.T) {
	g := New()
	if !g.Available(context.Background()) {
		t.Skip("ollama not reachable; skipping live generation test")
	}
	t.Logf("model=%s endpoint=%s", g.Model, g.Endpoint)

	samples := []string{
		"去年のDC1の申請書のワードファイル、どっかにないかな？多分Googleドライブかローカルのどっちかにあると思う。探してみてくれない？",
		"GhosttyをCmd Qで閉じる時に警告が出るようにしておいて",
		"VRゴーグルつけながら風呂入ったらたのしそうじゃね？",
		"Affilicode 取得ロジックと toridori ad との結合仕様を固める。schema.prisma / DDL を切る。",
		"claudenosaisinnkinounoeffortnosaidaidannkanoyatukttedorekuraidugoino",
	}
	for _, s := range samples {
		title, err := g.Generate(context.Background(), s)
		if err != nil {
			t.Errorf("generate %q: %v", s, err)
			continue
		}
		t.Logf("IN : %s\nOUT: %s\n", s, title)
	}
}
