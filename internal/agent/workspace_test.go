package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLockRepoQueuesAndCancels(t *testing.T) {
	w := &Workspace{repoLocks: map[string]chan struct{}{}}
	unlock, err := w.lockRepo(context.Background(), "Acme/Widgets", func() { t.Error("空闲时不应排队") })
	if err != nil {
		t.Fatal(err)
	}

	// 仓库名大小写不同也是同一把锁；等待期间取消应立即返回。
	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := w.lockRepo(ctx, "acme/widgets", func() { close(waited) })
		result <- err
	}()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("第二个任务应进入排队")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后应返回 context.Canceled，得到 %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后没有及时返回")
	}

	// 释放后其他任务可以拿到锁。
	unlock()
	done := make(chan struct{})
	go func() {
		release, err := w.lockRepo(context.Background(), "acme/widgets", nil)
		if err == nil {
			release()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("释放后应能重新加锁")
	}
}
