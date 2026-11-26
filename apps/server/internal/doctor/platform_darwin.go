//go:build darwin

package doctor

import "context"

func checkPlatform(_ context.Context, r *Report, home, _ string) {
	checkDarwinLimaPath(r, home)
}
