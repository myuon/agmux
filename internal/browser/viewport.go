package browser

import (
	"context"
	"fmt"
)

// Viewport modes of the user tab.
const (
	ViewportDesktop = "desktop"
	ViewportMobile  = "mobile"
)

// Mobile emulation parameters (iPhone 14/15 class device).
const (
	mobileWidth             = 390
	mobileDefaultHeight     = 844
	mobileMinHeight         = 480
	mobileMaxHeight         = 1200
	mobileDeviceScaleFactor = 2 // 3 would more than double screencast bandwidth for little gain
	mobileUserAgent         = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
)

// viewportState is the emulation applied to the user tab. The zero value is
// desktop (no overrides), which matches a freshly attached CDP session:
// Emulation overrides live as long as the CDP session that set them.
type viewportState struct {
	mobile bool
	height int  // CSS height, mobile only
	dirty  bool // a change failed half-way; the next one re-applies everything
}

// mobileHeight clamps a client-requested CSS height; 0 means the default.
func mobileHeight(h int) int {
	switch {
	case h <= 0:
		return mobileDefaultHeight
	case h < mobileMinHeight:
		return mobileMinHeight
	case h > mobileMaxHeight:
		return mobileMaxHeight
	}
	return h
}

// ViewportMode returns the currently applied viewport mode.
func (t *UserTab) ViewportMode() string {
	t.viewportMu.Lock()
	defer t.viewportMu.Unlock()
	if t.viewport.mobile {
		return ViewportMobile
	}
	return ViewportDesktop
}

// SetViewport switches the tab between desktop and mobile emulation. The page
// is reloaded only when the mode actually changes (so the new user agent takes
// effect). Re-sending the current mode, as clients do on every (re)connect, is
// a no-op, except that a different mobile height updates the metrics without
// a reload.
func (t *UserTab) SetViewport(ctx context.Context, mode string, height int) error {
	var want viewportState
	switch mode {
	case ViewportDesktop:
	case ViewportMobile:
		want = viewportState{mobile: true, height: mobileHeight(height)}
	default:
		return fmt.Errorf("%w: unknown viewport mode", ErrInvalidInput)
	}

	t.viewportMu.Lock()
	defer t.viewportMu.Unlock()
	if t.viewport == want {
		return nil
	}
	// After a partial failure re-apply everything, including the reload.
	modeChanged := t.viewport.mobile != want.mobile || t.viewport.dirty

	var calls []cdpCall
	if want.mobile {
		calls = append(calls, cdpCall{"Emulation.setDeviceMetricsOverride", map[string]any{
			"width": mobileWidth, "height": want.height,
			"deviceScaleFactor": mobileDeviceScaleFactor, "mobile": true,
		}})
		if modeChanged {
			calls = append(calls,
				cdpCall{"Emulation.setUserAgentOverride", map[string]any{"userAgent": mobileUserAgent, "platform": "iPhone"}},
				cdpCall{"Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}},
			)
		}
	} else {
		calls = append(calls,
			cdpCall{"Emulation.clearDeviceMetricsOverride", nil},
			// An empty user agent removes the override (DevTools does the same).
			cdpCall{"Emulation.setUserAgentOverride", map[string]any{"userAgent": ""}},
			cdpCall{"Emulation.setTouchEmulationEnabled", map[string]any{"enabled": false}},
		)
	}
	if modeChanged {
		// Reload so the page sees the new user agent and lays out again.
		calls = append(calls, cdpCall{"Page.reload", nil})
	}

	for i, c := range calls {
		if _, err := t.cdp.Call(ctx, c.method, c.params); err != nil {
			if i > 0 {
				// Partially applied: the actual state is unknown.
				t.viewport.dirty = true
			}
			return err
		}
	}
	t.viewport = want
	return nil
}

type cdpCall struct {
	method string
	params any
}
