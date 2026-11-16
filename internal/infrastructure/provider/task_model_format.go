package provider

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// This is a new, bounded model generation, never local text repair or a retry
// of an uncertain provider request. No validator text or new authority is supplied.
const taskFormatCorrection = "Your previous final answer did not match the required JSON output contract. Return exactly one JSON object matching the original output schema, with no Markdown or explanation. Correct only JSON syntax, field structure, types, enum values and declared size limits, preserving the meaning and facts already supplied and source records already read. Do not add facts, execute tools, or repeat any action."

// Valid JSON with the wrong shape belongs to schema validation. Empty,
// oversized or invalid UTF-8 output is a failed provider contract, not a reason
// to enlarge a request or start another generation.
func taskFormatRecoverable(content string, maxBytes int) bool {
	raw := strings.TrimSpace(content)
	return len(raw) > 0 && len(raw) <= maxBytes && utf8.ValidString(raw) && !json.Valid([]byte(raw))
}
