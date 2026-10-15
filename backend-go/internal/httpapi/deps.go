package httpapi

import (
	"log/slog"
	"sync"

	"github.com/ai-scratch-web/backend/internal/imageservice"
	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/prompt"
	"github.com/ai-scratch-web/backend/internal/provider"
)

// deps holds the process-wide singletons. The Python reference builds these
// with functools.lru_cache(maxsize=1), so they must be created exactly once.
type deps struct {
	once            sync.Once
	promptBuilder   *prompt.PromptBuilder
	orchestrator    *orchestration.GenerationOrchestrator
	blocksSemaphore chan struct{}
	textProvider    *provider.TextProvider
	imageService    *imageservice.Service
}

func (s *Server) deps() *deps {
	s.dependencies.once.Do(func() {
		settings := s.Settings
		s.dependencies.promptBuilder = prompt.NewPromptBuilder(
			nil,
			nil,
			settings.EnabledAIExtensions(),
			settings.AIDestructiveResourceOpsEnabled,
		)
		s.dependencies.orchestrator = orchestration.NewGenerationOrchestrator(
			s.dependencies.promptBuilder,
			settings.AIBlocksMaxRepairRounds,
			settings.AIBlocksMaxConcurrency,
		)
		concurrency := settings.AIBlocksMaxConcurrency
		if concurrency < 1 {
			concurrency = 1
		}
		s.dependencies.blocksSemaphore = make(chan struct{}, concurrency)
		s.dependencies.textProvider = provider.NewTextProvider(settings, "agnes")
		s.dependencies.imageService = imageservice.NewService(settings, nil)
	})
	return &s.dependencies
}

// log returns the server logger, defaulting to the process logger.
func (s *Server) log() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}
