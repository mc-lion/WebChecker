package telegram

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in     string
		want   Command
		wantOK bool
	}{
		{"list", Command{Name: "list"}, true},
		{"/list", Command{Name: "list"}, true},
		{"/list@webchecker_bot", Command{Name: "list"}, true},
		{"STAT", Command{Name: "stat"}, true},
		{"stat 12", Command{Name: "stat", Arg: "12"}, true},
		{"/stat@bot 7", Command{Name: "stat", Arg: "7"}, true},
		{"/stat1", Command{Name: "stat", Arg: "1"}, true},
		{"stat_3", Command{Name: "stat", Arg: "3"}, true},
		{"/stat id=4", Command{Name: "stat", Arg: "4"}, true},
		{"stat", Command{Name: "stat"}, true},
		{"help", Command{Name: "help"}, true},
		{"/start", Command{Name: "start"}, true},
		{"hello", Command{}, false},
		{"", Command{}, false},
	}
	for _, tc := range cases {
		got, ok := ParseCommand(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Fatalf("%q: got (%+v, %v), want (%+v, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestSameChatID(t *testing.T) {
	if !sameChatID(12345, "12345") || !sameChatID(-10012, "-10012") {
		t.Fatal("expected matching chat ids")
	}
	if sameChatID(1, "2") || sameChatID(1, "") {
		t.Fatal("did not expect match")
	}
}

func TestSplitMessage(t *testing.T) {
	parts := splitMessage("aaa\nbbb\nccc", 7)
	if len(parts) != 2 {
		t.Fatalf("got %#v", parts)
	}
}

func TestSplitMessageKeepsRunesIntact(t *testing.T) {
	// Кириллица: 2 байта на символ, поэтому побайтовая резка ломала символы.
	long := strings.Repeat("я", 25)
	parts := splitMessage(long, 10)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %#v", parts)
	}
	for _, p := range parts {
		if !utf8.ValidString(p) {
			t.Fatalf("part is not valid utf8: %q", p)
		}
		if utf8.RuneCountInString(p) > 10 {
			t.Fatalf("part longer than limit: %d runes", utf8.RuneCountInString(p))
		}
	}
	if joined := strings.Join(parts, ""); joined != long {
		t.Fatalf("content lost: %q", joined)
	}
}

func TestSplitMessageCountsRunesNotBytes(t *testing.T) {
	// 100 кириллических символов — это 200 байт, но лимит в 150 символов
	// не должен приводить к разбиению.
	text := strings.Repeat("о", 100)
	if parts := splitMessage(text, 150); len(parts) != 1 {
		t.Fatalf("expected single part, got %d", len(parts))
	}
}
