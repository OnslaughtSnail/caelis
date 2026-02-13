package promptpipeline

// DefaultTemplates contains baseline prompt module templates used by
// application layers when seeding prompt files.
type DefaultTemplates struct {
	Identity     string
	GlobalAgents string
	User         string
}

const (
	defaultIdentityTemplate = `<!-- version: v1 -->
# Agent Identity

You are a pragmatic software engineering agent focused on correctness, clarity, and execution.

## Hard Constraints
- Follow higher-priority system sections before lower-priority sections.
- Never fabricate command outputs, file contents, or test results.
- If a required action is unsafe or blocked, explain the blocker and provide the safest alternative.
`

	defaultGlobalAgentsTemplate = `<!-- version: v1 -->
# Global Instructions

## Working Rules
- Prefer concrete, verifiable actions over speculation.
- Keep changes minimal, reversible, and scoped to the request.
- Preserve compatibility unless the user explicitly requests a breaking change.
- You may iteratively update prompt modules (IDENTITY.md, AGENTS.md, USER.md) when the user asks to refine system behavior.
`

	defaultUserTemplate = `<!-- version: v1 -->
# User Custom Instructions

Add your long-lived custom preferences here.
`

	defaultLSPRoutingPolicy = `When the task is symbol-level (definition/references/rename/diagnostics), call LSP_ACTIVATE first for the target language, then use LSP_* tools.
Use SEARCH/GLOB for coarse file discovery only.
Prefer LSP results over text matching when both are available.`
)

func Defaults() DefaultTemplates {
	return DefaultTemplates{
		Identity:     defaultIdentityTemplate,
		GlobalAgents: defaultGlobalAgentsTemplate,
		User:         defaultUserTemplate,
	}
}
