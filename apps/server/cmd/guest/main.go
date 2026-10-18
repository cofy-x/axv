// axv-guest is a small Linux companion; it does not embed the desktop or providers.
package main

import (
	"fmt"
	"os"

	"github.com/cofy-x/axv/apps/server/internal/guest"
)

func main() {
	if err := guest.Run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
