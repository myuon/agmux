package browser

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateLoginURL accepts only absolute http/https URLs with a host. It is
// used for request_browser_login so the user is never sent to javascript:,
// file: or other schemes.
func ValidateLoginURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("url must include a host")
	}
	return nil
}
