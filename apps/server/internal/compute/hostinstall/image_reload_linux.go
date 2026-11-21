//go:build linux && amd64

package hostinstall

import (
	"encoding/json"
	"os"

	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

const imageReloadFile = binDir + "/.axv-default-image-reload.json"

// ReadImageReload consumes the installer authorization only on its standard
// socket and only from a directory and file owned by root.
func ReadImageReload(socket, hostID, fingerprint string) (ImageReload, bool) {
	if socket != standardSocket {
		return ImageReload{}, false
	}
	return (installFS{root: "/", uid: 0}).readImageReload(hostID, fingerprint)
}

func (fs installFS) readImageReload(hostID, fingerprint string) (ImageReload, bool) {
	var change ImageReload
	if hostID == "" || fingerprint == "" || fs.directories(binDir, false) != nil {
		return change, false
	}
	raw, err := fs.read(imageReloadFile, 0644)
	if err != nil || json.Unmarshal(raw, &change) != nil {
		return ImageReload{}, false
	}
	return change, change.HostID == hostID && change.Previous == fingerprint &&
		change.Fingerprint != "" && change.Fingerprint != fingerprint && change.ImageID != ""
}

func (fs installFS) recordImageReload(change ImageReload) error {
	if _, err := fs.read(imageReloadFile, 0644); err != nil && !os.IsNotExist(err) {
		return err
	}
	return jsonfile.Write(fs.path(imageReloadFile), change, 0644)
}
