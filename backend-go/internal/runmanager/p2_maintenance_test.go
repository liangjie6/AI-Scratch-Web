package runmanager

import (
	"context"
	"errors"
	"github.com/ai-scratch-web/backend/internal/runstore"
	"sync"
	"testing"
	"time"
)

func TestP2MaintenanceRemovesIdleWakeupsButKeepsWaiters(t *testing.T) {
	f := newTestManager(t)
	m := f.manager
	for i := 0; i < 1000; i++ {
		m.signal("finished-" + time.Unix(int64(i), 0).String())
	}
	if err := m.Maintain(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(m.wakeups) != 0 {
		t.Fatal(len(m.wakeups))
	}
	done := make(chan struct{})
	go func() { m.waitForEvents(context.Background(), "waiting", time.Second); close(done) }()
	for i := 0; i < 100; i++ {
		m.guard.Lock()
		n := m.waiters["waiting"]
		m.guard.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := m.Maintain(time.Now()); err != nil {
		t.Fatal(err)
	}
	<-done
	if err := m.Maintain(time.Now()); err != nil {
		t.Fatal(err)
	}
	m.guard.Lock()
	defer m.guard.Unlock()
	if len(m.wakeups) != 0 || len(m.waiters) != 0 {
		t.Fatalf("maps %d %d", len(m.wakeups), len(m.waiters))
	}
}

func TestP2SaturatedQueueRejectsBeforeNewTaskAndCancelsWithoutProvider(t *testing.T) {
	f := newTestManager(t)
	m := f.manager
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	var wg sync.WaitGroup
	accepted := make(chan string, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := m.Start(context.Background(), StartParams{Request: testUnifiedRequest(), OwnerScopeHash: testOwnerScopeHash, Provider: f.provider, Semaphore: sem, Orchestrator: f.orchestrator})
			if err == nil {
				accepted <- r["run_id"].(string)
			} else if !errors.Is(err, runstore.ErrRunCapacity) {
				t.Errorf("start %v", err)
			}
		}()
	}
	wg.Wait()
	close(accepted)
	ids := []string{}
	for id := range accepted {
		ids = append(ids, id)
	}
	if len(ids) != 8 {
		t.Fatalf("accepted %d", len(ids))
	}
	m.guard.Lock()
	if len(m.tasks) > 8 || len(m.requests) > 8 {
		t.Error("unbounded maps")
	}
	m.guard.Unlock()
	for _, id := range ids {
		_, err := m.Cancel(context.Background(), CancelParams{RunID: id, OwnerScopeHash: testOwnerScopeHash, ExpectedProjectEpoch: testProjectEpoch, IdempotencyKey: "cancel-" + id})
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m.Shutdown(ctx)
	if err := acquireSemaphoreWithin(context.Background(), sem, 10*time.Millisecond); !errors.Is(err, errRunQueueTimeout) {
		t.Fatal(err)
	}
	cancelled, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if err := acquireSemaphoreWithin(cancelled, sem, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
