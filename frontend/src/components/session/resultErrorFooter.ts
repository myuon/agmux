// Footer under an error result. subtype "success" with is_error=true (e.g. an
// API error such as 529 surfaced as the final result) is misleading next to a
// red error box, so only show subtypes that describe the error.
export function resultErrorFooter(subtype?: string, terminalReason?: string): string {
  return [subtype === "success" ? undefined : subtype, terminalReason].filter(Boolean).join(" · ");
}
