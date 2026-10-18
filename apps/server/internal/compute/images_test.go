package compute

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestImageInspectionRejectsExternalStorageReferences(t *testing.T) {
	for _, test := range []struct {
		name     string
		offset   uint64
		length   uint32
		features uint64
		valid    bool
	}{
		{name: "self contained", valid: true},
		{name: "backing offset", offset: 4096},
		{name: "backing length", length: 20},
		{name: "external data file", features: 1 << 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := make([]byte, 1024)
			copy(data, "QFI\xfb")
			binary.BigEndian.PutUint32(data[4:8], 3)
			binary.BigEndian.PutUint64(data[8:16], test.offset)
			binary.BigEndian.PutUint32(data[16:20], test.length)
			binary.BigEndian.PutUint64(data[24:32], 3500<<20)
			binary.BigEndian.PutUint64(data[72:80], test.features)
			path := filepath.Join(t.TempDir(), "image")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			minimum, err := InspectImage(file)
			if (err == nil) != test.valid {
				t.Fatalf("image validation: %v", err)
			}
			if test.valid && minimum != 4 {
				t.Fatalf("wrong minimum root size: %d", minimum)
			}
		})
	}
}
