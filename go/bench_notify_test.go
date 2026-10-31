package isureal

import (
	"testing"
)

func TestBenchNotifyEnabled(t *testing.T) {
	t.Setenv("BENCH_NOTIFY_ENABLED", "")
	if !benchNotifyEnabled() {
		t.Fatal("empty env should enable")
	}
	t.Setenv("BENCH_NOTIFY_ENABLED", "false")
	if benchNotifyEnabled() {
		t.Fatal("false should disable")
	}
	t.Setenv("BENCH_NOTIFY_ENABLED", "OFF")
	if benchNotifyEnabled() {
		t.Fatal("OFF should disable")
	}
}

func TestBenchEndDelaySec(t *testing.T) {
	t.Setenv("BENCH_END_NOTIFY_AFTER_SEC", "")
	if got := benchEndDelaySec(); got != 60 {
		t.Fatalf("default got %d", got)
	}
	t.Setenv("BENCH_END_NOTIFY_AFTER_SEC", "10")
	if got := benchEndDelaySec(); got != 10 {
		t.Fatalf("10 got %d", got)
	}
	t.Setenv("BENCH_END_NOTIFY_AFTER_SEC", "0")
	if got := benchEndDelaySec(); got != 60 {
		t.Fatalf("0 should fall back, got %d", got)
	}
}

func TestBenchEndHintSlackText(t *testing.T) {
	got := benchEndHintSlackText(60)
	want := "<!here> ✅ ベンチマーク終了（目安） 開始から60秒経過・計測終了想定"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
