package browser

import "testing"

func TestValidateLoginURL(t *testing.T) {
	ok := []string{"http://localhost:8080/login", "https://example.com/a?b=c"}
	bad := []string{"", "javascript:alert(1)", "file:///etc/passwd", "data:text/html,x", "ftp://x.com", "//example.com", "example.com", "http://", "chrome://settings"}
	for _, u := range ok {
		if err := ValidateLoginURL(u); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := ValidateLoginURL(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}
