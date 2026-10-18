package guest

import (
	"encoding/json"
	"io"
	"os"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func readConfig(path string) (protocol.Config, error) {
	var config protocol.Config
	file, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer file.Close()
	err = json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&config)
	return config, err
}
