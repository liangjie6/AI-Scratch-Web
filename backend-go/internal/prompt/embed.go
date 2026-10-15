package prompt

import _ "embed"

// EmbeddedKnowledgeBase is a build-time copy of the ProgramIR generation
// prompt document. The runtime prefers the repository document when it is
// present so the knowledge base can be edited without rebuilding.
//
//go:embed assets/ProgramIR-v1生成提示词.md
var EmbeddedKnowledgeBase string
