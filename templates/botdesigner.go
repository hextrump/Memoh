package templates

import _ "embed"

// BotDesignerPersona is the AGENTS.md written into the built-in Bot Designer
// bot's workspace when it is seeded.
//
//go:embed bot-designer/AGENTS.md
var BotDesignerPersona string
