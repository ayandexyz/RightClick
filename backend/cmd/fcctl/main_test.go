package main

import (
	"strings"
	"testing"
)

func TestParseConvertAcceptsToAlias(t *testing.T) {
	command, err := parseArgs([]string{"convert", "photo.png", "--to", "jpeg", "--output-dir", "/tmp/out", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if command.preset != "image-to-jpeg" || command.outputDir != "/tmp/out" || !command.jsonOutput {
		t.Fatalf("unexpected command: %+v", command)
	}
	if len(command.files) != 1 || command.files[0] != "photo.png" {
		t.Fatalf("unexpected files: %#v", command.files)
	}
}

func TestFormatEventHumanReadable(t *testing.T) {
	message := wireEvent{Event: "job.updated", Data: jobData{ID: "job-1", State: "running", Progress: 0.5}}
	line := formatHumanEvent(message)
	if !strings.Contains(line, "job-1") || !strings.Contains(line, "running") || !strings.Contains(line, "50%") {
		t.Fatalf("unexpected output: %q", line)
	}
}
