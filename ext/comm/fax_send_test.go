package comm

import "testing"

// TestFaxSendNumber は、打たれた FAX の番号を数字だけにし、使えない文字や長さを断ることを固定します
// （sendfax の引数に人の書いた文字をそのまま渡さないため）。
func TestFaxSendNumber(t *testing.T) {
	for in, want := range map[string]string{
		"06-1234-5678":     "0612345678",
		"０６－１２３４－５６７８":     "0612345678",
		"(06) 1234 5678":   "0612345678",
		"06-1234-5678;rm":  "",
		"12345":            "",
		"0612345678901234": "",
		"":                 "",
	} {
		if got := faxSendNumber(in); got != want {
			t.Errorf("faxSendNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFaxJobState は、faxstat の出力から仕事の様子を読むことを固定します（2026-10-25 に業務サーバーで見た形）。
func TestFaxJobState(t *testing.T) {
	out := `HylaFAX scheduler on wcms-server: Running
Modem faxmodem (): Running and idle

JID  Pri S  Owner Number       Pages Dials     TTS Status
1    126 F   toho 0612345678    0:1   1:12         Job aborted by request
2    127 D   toho 0612345678    1:1   1:12
3    127 R   w-cms 0612345678   0:1   0:12
`
	for job, want := range map[string]string{"1": "failed", "2": "done", "3": "queued", "9": "unknown"} {
		if got, _ := faxJobState(out, job); got != want {
			t.Errorf("faxJobState(%s) = %s, want %s", job, got, want)
		}
	}
	if _, line := faxJobState(out, "1"); line == "" {
		t.Error("理由の行が返らない")
	}
}
