// Package ubuntusources owns the explicit Ubuntu 24.04 package source contract,
// shared by offline image builders and per-VM maintenance. It owns no cache data.
package ubuntusources

import (
	_ "embed"
	"fmt"
)

//go:embed configure.sh
var Script string

func Validate(source string) error {
	if source != "official" && source != "aliyun" {
		return fmt.Errorf("package source must be official or aliyun")
	}
	return nil
}
