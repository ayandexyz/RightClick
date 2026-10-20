package main

import (
	"encoding/json"
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

func TestParseConvertRejectsUnknownToTarget(t *testing.T) {
	if _, err := parseArgs([]string{"convert", "photo.png", "--to", "pdf"}); err == nil {
		t.Fatal("expected unknown target to be rejected")
	}
}

func TestFormatEventHumanReadable(t *testing.T) {
	message := wireEvent{Event: "job.updated", Data: jobData{ID: "job-1", State: "running", Progress: 0.5}}
	line := formatHumanEvent(message)
	if !strings.Contains(line, "job-1") || !strings.Contains(line, "running") || !strings.Contains(line, "50%") {
		t.Fatalf("unexpected output: %q", line)
	}
}

func TestFormatFailedEventIncludesPerFileError(t *testing.T) {
	message := wireEvent{Event: "job.finished", Data: jobData{ID: "job-1", State: "failed", Files: []fileData{{Source: "/tmp/missing.png", State: "failed", Error: &fileError{Code: "unsupported", Message: "file not found"}}}}}
	line := formatHumanEvent(message)
	if !strings.Contains(line, "file not found") || !strings.Contains(line, "/tmp/missing.png") {
		t.Fatalf("unexpected output: %q", line)
	}
}

func TestParseConvertImageOptions(t *testing.T) {
	cmd, err := parseArgs([]string{"convert", "/a.png", "--to", "jpeg", "--quality", "80", "--max-size", "2048", "--background", "#fff"})
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal(commandRequest(cmd).Params, &params); err != nil {
		t.Fatal(err)
	}
	if params["quality"] != float64(80) || params["max_size"] != float64(2048) || params["background"] != "#fff" {
		t.Fatalf("params = %#v", params)
	}
}

func TestParseImageOptionsRejectBadValuesAndNonConvert(t *testing.T) {
	for _, args := range [][]string{
		{"convert", "/a.png", "--to", "jpeg", "--quality", "high"},
		{"convert", "/a.png", "--to", "jpeg", "--max-size"},
		{"targets", "/a.png", "--quality", "80"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("parseArgs(%v) succeeded", args)
		}
	}
}

func TestParseEnginesCommand(t *testing.T) {
	cmd, err := parseArgs([]string{"engines", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.name != "engines" || !cmd.jsonOutput {
		t.Fatalf("unexpected command: %+v", cmd)
	}
	if req := commandRequest(cmd); req.Method != "engines" {
		t.Fatalf("method = %q", req.Method)
	}
	if _, err := parseArgs([]string{"engines", "file.png"}); err == nil {
		t.Fatal("engines must not accept files")
	}
}

func TestFormatEnginesHumanReadable(t *testing.T) {
	value := map[string]any{"result": map[string]any{"engines": []any{
		map[string]any{"id": "magick", "available": true, "reason": ""},
		map[string]any{"id": "ffmpeg", "available": false, "reason": "ffmpeg not found"},
	}}}
	got := formatHuman(value)
	for _, want := range []string{"magick", "available", "ffmpeg", "unavailable: ffmpeg not found"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q missing %q", got, want)
		}
	}
}
