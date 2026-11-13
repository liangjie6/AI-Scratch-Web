package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/ai-scratch-web/backend/internal/runmanager"
	"github.com/ai-scratch-web/backend/internal/runstore"
)

// runDeps holds the lazily created durable-run singletons, mirroring the
// functools.lru_cache(maxsize=1) providers in backend/app/routes/ai_runs.py.
type runDeps struct {
	once              sync.Once
	store             *runstore.RunStore
	storeErr          error
	manager           *runmanager.AiRunManager
	maintenanceCancel context.CancelFunc
	maintenanceDone   chan struct{}
}

func (s *Server) runDeps() *runDeps {
	s.runs.once.Do(func() {
		settings := s.Settings
		store, err := runstore.NewStore(runstore.Config{
			DatabasePath:        settings.AIRunsDatabasePath,
			EventRetentionHours: settings.AIRunsEventRetentionHours,
			RunRetentionDays:    settings.AIRunsRetentionDays,
			MaxActiveRuns:       settings.AIRunsMaxActive, MaxActiveRunsPerOwner: settings.AIRunsMaxActivePerScope,
		})
		if err != nil {
			s.runs.storeErr = err
			return
		}
		if err := store.Initialize(); err != nil {
			s.runs.storeErr = err
			return
		}
		s.runs.store = store
		s.runs.manager = runmanager.NewAiRunManager(store, settings)
	})
	return &s.runs
}

// RunStore returns the shared durable run store.
func (s *Server) RunStore() *runstore.RunStore { return s.runDeps().store }

// runManager returns the shared durable-run manager, or nil when the store
// could not be initialized. Callers must guard against nil.
func (s *Server) runManager() *runmanager.AiRunManager {
	return s.runDeps().manager
}

// Startup mirrors the FastAPI lifespan startup hook.
func (s *Server) Startup() error {
	dependencies := s.runDeps()
	if dependencies.storeErr != nil {
		return dependencies.storeErr
	}
	interrupted, err := dependencies.store.FailIncompleteRuns()
	if err != nil {
		return err
	}
	if interrupted > 0 {
		s.log().Warn("marked_interrupted_ai_runs", "count", interrupted)
	}
	if _, err := dependencies.store.PurgeExpired(); err != nil {
		return err
	}
	if err := dependencies.manager.Maintain(time.Now()); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	dependencies.maintenanceCancel = cancel
	dependencies.maintenanceDone = make(chan struct{})
	go func() {
		defer close(dependencies.maintenanceDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := dependencies.manager.Maintain(now); err != nil {
					s.log().Error("ai_run_maintenance_failed", "error", err)
				}
			}
		}
	}()
	return nil
}

// Shutdown mirrors the FastAPI lifespan shutdown hook.
func (s *Server) Shutdown(ctx context.Context) {
	dependencies := s.runDeps()
	if dependencies.maintenanceCancel != nil {
		dependencies.maintenanceCancel()
		<-dependencies.maintenanceDone
	}
	if dependencies.manager != nil {
		dependencies.manager.Shutdown(ctx)
	}
	if dependencies.store != nil {
		_ = dependencies.store.Close()
	}
}

var _ = sync.Once{}
