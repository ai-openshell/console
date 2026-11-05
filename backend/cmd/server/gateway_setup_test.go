package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/ai-openshell/console/backend/pkg/models"
)

// captureLogs swaps the default logger for the duration of a test and returns
// the buffer it writes to.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// The BFF knows its line with nothing set. An override it cannot read must
// never stop it starting and must never be swapped for another line: there is
// no verdict, and the operator is told why.
func TestGatewayReleaseLine(t *testing.T) {
	builtIn := models.BuiltInGatewayReleaseLine
	// A line that is certainly not the built-in one, whatever that becomes.
	other := "9.9"

	tests := []struct {
		name     string
		override string
		wantLine string
		wantWarn bool
		wantInfo bool
	}{
		{name: "nothing set uses the built-in line", override: "", wantLine: builtIn},
		{name: "a blank value is no override", override: "  ", wantLine: builtIn},
		{name: "the built-in line spelled out is silent", override: builtIn, wantLine: builtIn},
		{name: "another line replaces it and is logged", override: other, wantLine: other, wantInfo: true},
		{name: "a full version is not a line", override: "0.1.3", wantLine: "", wantWarn: true},
		{name: "the way the line is shown to people", override: "0.1.x", wantLine: "", wantWarn: true},
		{name: "not a version", override: "latest", wantLine: "", wantWarn: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)

			line := gatewayReleaseLine(tc.override)

			if got := line.String(); got != tc.wantLine {
				t.Errorf("line = %q, want %q", got, tc.wantLine)
			}
			if warned := strings.Contains(logs.String(), "level=WARN"); warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v; logs: %s", warned, tc.wantWarn, logs.String())
			}
			if informed := strings.Contains(logs.String(), "level=INFO"); informed != tc.wantInfo {
				t.Errorf("logged an override = %v, want %v; logs: %s", informed, tc.wantInfo, logs.String())
			}
			if tc.wantWarn {
				// The warning has to name the variable, the value it could
				// not read and what a line looks like.
				for _, want := range []string{"GATEWAY_RELEASE_LINE", strings.TrimSpace(tc.override), "major.minor"} {
					if !strings.Contains(logs.String(), want) {
						t.Errorf("warning does not mention %q: %s", want, logs.String())
					}
				}
			}
		})
	}
}
