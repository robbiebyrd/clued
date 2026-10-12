// Package hookguard holds the rules behind the Claude Code hooks the plugin
// serves from the mind-palace binary. The Stop guard reads the Stop hook's
// input and blocks the assistant from stopping when its last message dodges
// ownership of a failure, defers work to a later session or asks whether to
// continue work it was already asked to do. Entrypoints (the CLI) only
// translate stdin and stdout; the rules live here.
package hookguard
