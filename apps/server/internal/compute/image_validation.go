package compute

import (
	"encoding/binary"
	"fmt"
	"net/url"
	"os"
	"runtime"

	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
)

const MaxImageBytes = guestimage.MaxRootfsBytes

// InspectImage rejects backing-file references before Lima can open an imported
// image. Architecture remains an explicit catalog property, verified at boot.
func InspectImage(file *os.File) (minimumGiB int64, err error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("image must be a regular file")
	}
	header := make([]byte, 1024)
	if _, err = file.ReadAt(header, 0); err != nil {
		return 0, fmt.Errorf("image header is incomplete")
	}
	var virtualSize uint64
	if string(header[:4]) == "QFI\xfb" {
		version := binary.BigEndian.Uint32(header[4:8])
		if version != 2 && version != 3 {
			return 0, fmt.Errorf("unsupported qcow2 version")
		}
		if binary.BigEndian.Uint64(header[8:16]) != 0 || binary.BigEndian.Uint32(header[16:20]) != 0 {
			return 0, fmt.Errorf("qcow2 backing-file references are not accepted")
		}
		if version == 3 && binary.BigEndian.Uint64(header[72:80])&(1<<2) != 0 {
			return 0, fmt.Errorf("qcow2 external data files are not accepted")
		}
		virtualSize = binary.BigEndian.Uint64(header[24:32])
	} else {
		if header[510] != 0x55 || header[511] != 0xaa {
			return 0, fmt.Errorf("select a bootable Linux raw or qcow2 cloud image")
		}
		virtualSize = uint64(info.Size())
	}
	if virtualSize < 1<<20 || virtualSize > 1<<60 {
		return 0, fmt.Errorf("invalid image virtual size")
	}
	return int64((virtualSize + (1 << 30) - 1) >> 30), nil
}

func ValidateImageRequest(req ImageRequest, upload bool) error {
	if err := ValidateRequestID(req.RequestID); err != nil {
		return err
	}
	if err := ValidateResourceName(req.Name); err != nil {
		return err
	}
	if req.Arch != runtime.GOARCH {
		return Problem("unsupported", "Choose an image for this Mac's native %s architecture.", runtime.GOARCH)
	}
	if !digest(req.SHA256) {
		return Problem("invalid_request", "Supply the image's complete SHA-256 checksum.")
	}
	if upload {
		if req.URL != "" {
			return Problem("invalid_request", "Choose a file upload or an HTTPS URL.")
		}
	} else {
		parsed, err := url.Parse(req.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return Problem("invalid_request", "Use an HTTPS image URL without embedded credentials.")
		}
	}
	return nil
}
