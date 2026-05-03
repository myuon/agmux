package config

import "testing"

func TestIsBackgroundTaskNotificationEnabled_DefaultsToTrue(t *testing.T) {
	d := DaemonConfig{}
	if !d.IsBackgroundTaskNotificationEnabled() {
		t.Fatalf("expected default (nil) to be enabled=true")
	}
}

func TestIsBackgroundTaskNotificationEnabled_ExplicitFalse(t *testing.T) {
	v := false
	d := DaemonConfig{BackgroundTaskNotificationEnabled: &v}
	if d.IsBackgroundTaskNotificationEnabled() {
		t.Fatalf("expected explicit false to be enabled=false")
	}
}

func TestIsBackgroundTaskNotificationEnabled_ExplicitTrue(t *testing.T) {
	v := true
	d := DaemonConfig{BackgroundTaskNotificationEnabled: &v}
	if !d.IsBackgroundTaskNotificationEnabled() {
		t.Fatalf("expected explicit true to be enabled=true")
	}
}
