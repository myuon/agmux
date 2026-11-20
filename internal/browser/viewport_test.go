package browser

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordedCall struct {
	method string
	params map[string]any
}

func newViewportTestTab(t *testing.T) (*fakeChrome, *UserTab) {
	t.Helper()
	f := newFakeChrome(t, filepath.Join(t.TempDir(), "profile"))
	m := testManager(t, f, nil)
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	tab, err := m.UserTab(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return f, tab
}

// takeCalls returns the page-target calls recorded since the last take.
func takeCalls(f *fakeChrome) []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedCall
	for i, m := range f.calls {
		out = append(out, recordedCall{m, f.callParams[i]})
	}
	f.calls, f.callParams = nil, nil
	return out
}

func methods(cs []recordedCall) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.method)
	}
	return out
}

func TestViewportMobileThenDesktop(t *testing.T) {
	f, tab := newViewportTestTab(t)
	ctx := context.Background()
	takeCalls(f)

	if tab.ViewportMode() != ViewportDesktop {
		t.Fatalf("initial mode = %s", tab.ViewportMode())
	}
	if err := tab.HandleInput(ctx, InputMessage{Type: "viewport", Mode: "mobile"}); err != nil {
		t.Fatal(err)
	}
	calls := takeCalls(f)
	want := []string{"Emulation.setDeviceMetricsOverride", "Emulation.setUserAgentOverride", "Emulation.setTouchEmulationEnabled", "Page.reload"}
	if got := methods(calls); !reflect.DeepEqual(got, want) {
		t.Fatalf("mobile calls = %v, want %v", got, want)
	}
	dm := calls[0].params
	if dm["width"] != float64(mobileWidth) || dm["height"] != float64(mobileDefaultHeight) || dm["mobile"] != true || dm["deviceScaleFactor"] != float64(mobileDeviceScaleFactor) {
		t.Fatalf("bad device metrics %v", dm)
	}
	if ua, _ := calls[1].params["userAgent"].(string); !strings.Contains(ua, "iPhone") || !strings.Contains(ua, "Mobile") {
		t.Fatalf("bad mobile UA %q", ua)
	}
	if calls[2].params["enabled"] != true {
		t.Fatalf("touch emulation not enabled: %v", calls[2].params)
	}
	if tab.ViewportMode() != ViewportMobile {
		t.Fatalf("mode = %s", tab.ViewportMode())
	}

	if err := tab.HandleInput(ctx, InputMessage{Type: "viewport", Mode: "desktop"}); err != nil {
		t.Fatal(err)
	}
	calls = takeCalls(f)
	want = []string{"Emulation.clearDeviceMetricsOverride", "Emulation.setUserAgentOverride", "Emulation.setTouchEmulationEnabled", "Page.reload"}
	if got := methods(calls); !reflect.DeepEqual(got, want) {
		t.Fatalf("desktop calls = %v, want %v", got, want)
	}
	if calls[1].params["userAgent"] != "" {
		t.Fatalf("UA override not cleared: %v", calls[1].params)
	}
	if calls[2].params["enabled"] != false {
		t.Fatalf("touch emulation not disabled: %v", calls[2].params)
	}
	if tab.ViewportMode() != ViewportDesktop {
		t.Fatalf("mode = %s", tab.ViewportMode())
	}
}

// Clients re-send their choice on every (re)connect: that must not reload.
func TestViewportSameModeIsNoop(t *testing.T) {
	f, tab := newViewportTestTab(t)
	ctx := context.Background()
	takeCalls(f)

	if err := tab.SetViewport(ctx, ViewportDesktop, 0); err != nil {
		t.Fatal(err)
	}
	if got := takeCalls(f); len(got) != 0 {
		t.Fatalf("desktop on a fresh tab sent %v", methods(got))
	}

	if err := tab.SetViewport(ctx, ViewportMobile, 700); err != nil {
		t.Fatal(err)
	}
	takeCalls(f)
	if err := tab.SetViewport(ctx, ViewportMobile, 700); err != nil {
		t.Fatal(err)
	}
	if got := takeCalls(f); len(got) != 0 {
		t.Fatalf("same mobile viewport sent %v", methods(got))
	}

	// A different height only updates the metrics: no UA change, no reload.
	if err := tab.SetViewport(ctx, ViewportMobile, 760); err != nil {
		t.Fatal(err)
	}
	calls := takeCalls(f)
	if got := methods(calls); !reflect.DeepEqual(got, []string{"Emulation.setDeviceMetricsOverride"}) {
		t.Fatalf("height change calls = %v", got)
	}
	if calls[0].params["height"] != float64(760) {
		t.Fatalf("height = %v", calls[0].params["height"])
	}
}

func TestViewportRejectsUnknownMode(t *testing.T) {
	f, tab := newViewportTestTab(t)
	takeCalls(f)
	err := tab.HandleInput(context.Background(), InputMessage{Type: "viewport", Mode: "tablet"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
	if got := takeCalls(f); len(got) != 0 {
		t.Fatalf("unknown mode sent %v", methods(got))
	}
}

func TestMobileHeightClamp(t *testing.T) {
	for in, want := range map[int]int{0: mobileDefaultHeight, -5: mobileDefaultHeight, 100: mobileMinHeight, 700: 700, 5000: mobileMaxHeight} {
		if got := mobileHeight(in); got != want {
			t.Errorf("mobileHeight(%d) = %d, want %d", in, got, want)
		}
	}
}
