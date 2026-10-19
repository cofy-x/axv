package jsonunicode

import "testing"

func TestValid(t *testing.T) {
	for _, data := range []string{
		`{"content":"plain text and �"}`,
		`{"content":"\ud83d\ude00"}`,
		`{"content":"\uD83D\uDE00\u000a"}`,
		`{"content":"\\ud800"}`,
		`{"content":"escaped quote \" and slash \\"}`,
		`{"\ud83d\ude00":"\u0061"}`,
	} {
		if !Valid([]byte(data)) {
			t.Errorf("valid Unicode rejected: %s", data)
		}
	}
	for _, data := range []string{
		"{\"content\":\"\xff\"}",
		"{\"path\":\"file\xc0\xafname\"}",
		`{"content":"\ud800"}`,
		`{"content":"\udc00"}`,
		`{"content":"\ud800\u0061"}`,
		`{"content":"\ud800\\udc00"}`,
		`{"content":"\ud800\ud800"}`,
		`{"content":"\ud800\udc0"}`,
		`{"\ud800":"value"}`,
	} {
		if Valid([]byte(data)) {
			t.Errorf("invalid Unicode accepted: %q", data)
		}
	}
}
