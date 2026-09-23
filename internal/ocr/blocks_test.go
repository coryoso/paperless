package ocr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBlockDocumentNativeTextAndSourceChanges(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "document.txt")
	os.WriteFile(textPath, []byte("First paragraph\n\nSecond paragraph\fNext page"), 0600)
	d, err := ReadBlockDocument(dir, textPath, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 3 || d.Blocks[2].Page != 2 || d.Blocks[0].Position != nil || d.Blocks[0].Size != nil {
		t.Fatal(d)
	}
	again, err := ReadBlockDocument(dir, textPath, 2, 2)
	if err != nil || again.SourceHash != d.SourceHash {
		t.Fatal("multiplier changed OCR identity", err)
	}
	os.WriteFile(textPath, []byte("Changed OCR"), 0600)
	changed, err := ReadBlockDocument(dir, textPath, 2, 1)
	if err != nil || changed.SourceHash == d.SourceHash {
		t.Fatal("stale source identity", err)
	}
}
