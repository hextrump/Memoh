package bots

import "testing"

func TestMessagePreview(t *testing.T) {
	cases := []struct {
		name    string
		display string
		content string
		want    string
	}{
		{"display text wins", "hello  there\n", `{"content":"ignored"}`, "hello there"},
		{"string content", "", `{"role":"user","content":"plain body"}`, "plain body"},
		{"skips reasoning", "", `{"content":[{"type":"reasoning","text":"thinking"},{"type":"text","text":"\n\nanswer"}]}`, "answer"},
		{"no text parts", "", `{"content":[{"type":"tool-call","text":""}]}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := messagePreview(tc.display, []byte(tc.content)); got != tc.want {
				t.Fatalf("messagePreview() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMessagePreviewTruncates(t *testing.T) {
	long := make([]rune, activityPreviewMaxRunes+10)
	for i := range long {
		long[i] = '字'
	}
	got := []rune(messagePreview(string(long), nil))
	if len(got) != activityPreviewMaxRunes+1 {
		t.Fatalf("len = %d, want %d", len(got), activityPreviewMaxRunes+1)
	}
}
