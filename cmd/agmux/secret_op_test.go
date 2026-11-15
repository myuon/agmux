package main

import "testing"

func TestBuildSecretAddBodyOP(t *testing.T) {
	body := buildSecretAddBody("DB_PASSWORD", "d", "1password", "", "op://v/i/f", []string{})
	if body["ref"] != "op://v/i/f" || body["backend"] != "1password" {
		t.Errorf("body = %v", body)
	}
	if _, ok := body["value"]; ok {
		t.Error("value must not be sent for --op")
	}
	body = buildSecretAddBody("X", "", "", "val", "", nil)
	if body["value"] != "val" {
		t.Errorf("body = %v", body)
	}
	if _, ok := body["ref"]; ok {
		t.Error("ref must not be sent without --op")
	}
}

func TestSecretAddOPFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"X", "--op", "bad"},
		{"X", "--op", "op://v/i/f", "--backend", "file"},
		{"X", "--backend", "1password"},
	} {
		cmd := secretAddCmd()
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("args %v: want error", args)
		}
	}
}
