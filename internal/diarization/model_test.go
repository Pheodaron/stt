package diarization

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMissingSDKFailsWithoutFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := New(filepath.Join(t.TempDir(), "absent.so"), path)
	if err == nil || !strings.Contains(err.Error(), "load NeMo SDK") {
		t.Fatalf("%v", err)
	}
}

func TestInstalledSDKRejectsInvalidModel(t *testing.T) {
	library := os.Getenv("NEMOTRON_TEST_LIBRARY")
	if library == "" {
		t.Skip("set NEMOTRON_TEST_LIBRARY to test the installed native C ABI")
	}
	path := filepath.Join(t.TempDir(), "invalid.gguf")
	if err := os.WriteFile(path, []byte("invalid model"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := New(library, path)
	if err == nil || !strings.Contains(err.Error(), "NeMo status") {
		t.Fatalf("native model error was not propagated: %v", err)
	}
}

func TestRealNemotron(t *testing.T) {
	path, library, pcm := os.Getenv("NEMOTRON_TEST_MODEL"), os.Getenv("NEMOTRON_TEST_LIBRARY"), os.Getenv("NEMOTRON_TEST_PCM")
	if path == "" || library == "" || pcm == "" {
		t.Skip("set NEMOTRON_TEST_MODEL, NEMOTRON_TEST_LIBRARY and NEMOTRON_TEST_PCM for actual diarization")
	}
	m, err := New(library, path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	first, err := m.Diarize(ctx, pcm)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Diarize(ctx, pcm)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("speaker state leaked between recordings")
	}
	if len(first) == 0 {
		t.Fatal("test recording produced no speech")
	}
	m.Close()
	m.Close()
	if _, err := m.Diarize(ctx, pcm); err == nil {
		t.Fatal("closed model accepted audio")
	}
	t.Logf("diarization intervals: %d", len(first))
}
