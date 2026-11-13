package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ai-scratch-web/backend/internal/authoring"
)

func TestSourceArtifactCannotChangeIDsToResetBudget(t *testing.T) {
	r := sourceTestRequest()
	opts, err := authoring.OptionsFromRequest(r, sourceTestIntent(1))
	if err != nil {
		t.Fatal(err)
	}
	c, err := authoring.Freeze(sourceTestIntent(1), opts)
	if err != nil {
		t.Fatal(err)
	}
	request := &SourceProgramRequest{Request: *r, SourceContract: *c, TargetIndex: 0, Target: r.Target}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	request.Request.RequestID = "e8fc2b49-411b-4e9d-8b4e-f7f44b9e2c78"
	if request.Validate() == nil {
		t.Fatal("same artifact changed request identity and escaped its shared budget")
	}
	request.Request = *r
	request.Request.SessionID = "another-session"
	if request.Validate() == nil {
		t.Fatal("same artifact changed session identity and escaped its shared budget")
	}
}

func TestSourceBudgetRestoreNeverLowersUsageOrExtendsExpiry(t *testing.T) {
	o := NewGenerationOrchestrator(nil, 0, 2)
	r := sourceTestRequest()
	expires := time.Now().Add(time.Minute)
	if err := o.RestoreSourceBudget(r, "restore", SourceBudgetSnapshot{Calls: 8, Tokens: 1234, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	if err := o.RestoreSourceBudget(r, "restore", SourceBudgetSnapshot{Calls: 2, Tokens: 100, ExpiresAt: expires.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	snapshot := o.SnapshotSourceBudget(r, "restore")
	if snapshot.Calls != 8 || snapshot.Tokens != 1234 || !snapshot.ExpiresAt.Equal(expires) {
		t.Fatalf("restore lowered or extended budget: %#v", snapshot)
	}
}

func TestExpiredSourceArtifactCannotRestartAfterBudgetCacheCleanup(t *testing.T) {
	r := sourceTestRequest()
	opts, err := authoring.OptionsFromRequest(r, sourceTestIntent(1))
	if err != nil {
		t.Fatal(err)
	}
	opts.ExpiresAtUnix = time.Now().Unix() - 1
	c, err := authoring.Freeze(sourceTestIntent(1), opts)
	if err != nil {
		t.Fatal(err)
	}
	request := &SourceProgramRequest{Request: *r, SourceContract: *c, TargetIndex: 0, Target: r.Target}
	if request.Validate() == nil {
		t.Fatal("expired frozen artifact was accepted")
	}
	o := NewGenerationOrchestrator(nil, 0, 2)
	old := o.sourceBudgetFor(sourceKey(r, c.Hash))
	old.mu.Lock()
	old.expires = time.Now().Add(-time.Second)
	old.mu.Unlock()
	o.sourceBudgetFor("trigger-expired-entry-cleanup")
	p := newCannedProvider(nil)
	if err := o.generateSourceTarget(context.Background(), request, p, func(e GenerationEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if p.callCount() != 0 {
		t.Fatal("cache cleanup revived an expired contract and made a model call")
	}
}

func TestSourceFailedLaterSlotNeverStreamsEarlierPartialScripts(t *testing.T) {
	r := sourceTestRequest()
	intent, _ := json.Marshal(sourceTestIntent(2))
	p := newCannedProvider(map[string][]string{"default": {string(intent), sourceSlot("move"), `{"slot_id":"key_move","status":"ready","body":[{"opcode":"does_not_exist"}],"revision":null}`}})
	o := NewGenerationOrchestrator(nil, 0, 2)
	failed := false
	if err := o.generateSourceUnified(context.Background(), r, p, func(e GenerationEvent) error {
		if e.Type == "script_ready" || e.Type == "metadata" || e.Type == "shared_state" || e.Type == "complete" {
			t.Errorf("partially validated target leaked an output frame: %s", e.Type)
		}
		failed = failed || e.Type == "error"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !failed || p.callCount() != 3 {
		t.Fatalf("later slot failure did not stop the target: failed=%t calls=%d", failed, p.callCount())
	}
}

func TestSourceBudgetExpiryRestoreAndMapCleanupAreRaceFree(t *testing.T) {
	o := NewGenerationOrchestrator(nil, 0, 2)
	r := sourceTestRequest()
	expiry := time.Now().Add(time.Minute)
	if err := o.RestoreSourceBudget(r, "contract", SourceBudgetSnapshot{ExpiresAt: expiry}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := o.RestoreSourceBudget(r, "contract", SourceBudgetSnapshot{ExpiresAt: expiry.Add(-time.Duration(i) * time.Nanosecond)}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			o.sourceBudgetFor(fmt.Sprintf("new-key-%d", i))
		}
	}()
	wg.Wait()
	snapshot := o.SnapshotSourceBudget(r, "contract")
	if !snapshot.ExpiresAt.Before(expiry) {
		t.Fatal("restore extended the budget expiry")
	}
}
