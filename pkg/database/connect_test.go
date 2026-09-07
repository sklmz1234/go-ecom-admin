package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// stubSleep 把真实睡眠替换成瞬时返回，让重试时序可以纯逻辑地验证。
func stubSleep(t *testing.T, result bool) {
	t.Helper()
	orig := sleep
	sleep = func(context.Context, time.Duration) bool { return result }
	t.Cleanup(func() { sleep = orig })
}

func TestConnectWithRetry_SuccessAfterFailures(t *testing.T) {
	stubSleep(t, true)

	calls := 0
	open := func() (*gorm.DB, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("connection refused")
		}
		return &gorm.DB{}, nil
	}

	db, err := ConnectWithRetry(context.Background(), open, ConnectConfig{})
	if err != nil {
		t.Fatalf("expected success on 3rd attempt, got error: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}
	if calls != 3 {
		t.Fatalf("expected 3 open calls, got %d", calls)
	}
}

func TestConnectWithRetry_GivesUpAfterMaxWait(t *testing.T) {
	stubSleep(t, true)

	lastErr := errors.New("no such host")
	open := func() (*gorm.DB, error) { return nil, lastErr }

	_, err := ConnectWithRetry(context.Background(), open, ConnectConfig{MaxWait: 2 * time.Millisecond})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, lastErr) {
		t.Fatalf("error should wrap last error, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "unreachable") || !strings.Contains(msg, "no such host") {
		t.Fatalf("error should mention attempts and last cause, got: %v", err)
	}
}

func TestConnectWithRetry_ContextCanceledDuringBackoff(t *testing.T) {
	// sleep 返回 false 模拟"退避等待期间收到取消信号"——
	// K8s 滚动更新发 SIGTERM 时，启动重试必须立刻让位，不能拖满 30s 宽限期。
	stubSleep(t, false)

	open := func() (*gorm.DB, error) { return nil, errors.New("connection refused") }

	_, err := ConnectWithRetry(context.Background(), open, ConnectConfig{})
	if err == nil {
		t.Fatal("expected cancel error, got nil")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("error should mention cancellation, got: %v", err)
	}
}

func TestConnectWithRetry_DefaultsFilled(t *testing.T) {
	cfg := ConnectConfig{}
	cfg.fillDefaults()
	if cfg.MaxWait != 2*time.Minute {
		t.Errorf("default MaxWait = %v, want 2m", cfg.MaxWait)
	}
	if cfg.MinDelay != time.Second {
		t.Errorf("default MinDelay = %v, want 1s", cfg.MinDelay)
	}
	if cfg.MaxDelay != 10*time.Second {
		t.Errorf("default MaxDelay = %v, want 10s", cfg.MaxDelay)
	}
}
