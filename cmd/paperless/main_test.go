package main

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	"paperless/internal/buildinfo"
)

func TestVersion(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		args := []string{"version"}
		if asJSON {
			args = append(args, "--json")
		}
		output, err := os.CreateTemp(t.TempDir(), "stdout")
		if err != nil {
			t.Fatal(err)
		}
		original := os.Stdout
		os.Stdout = output
		err = run(args)
		os.Stdout = original
		if err != nil {
			t.Fatal(err)
		}
		if _, err := output.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(output)
		output.Close()
		if err != nil {
			t.Fatal(err)
		}
		if asJSON {
			var manifest buildinfo.Manifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest != buildinfo.Current() {
				t.Fatalf("manifest = %#v, want %#v", manifest, buildinfo.Current())
			}
		} else if string(data) != "paperless "+buildinfo.Version+"\n" {
			t.Fatalf("version output = %q", data)
		}
	}
}
