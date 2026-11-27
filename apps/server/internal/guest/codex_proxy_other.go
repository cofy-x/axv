//go:build !linux

package guest

import "context"

type guestProxyBridge struct{}

func (b *guestProxyBridge) probe(context.Context) bool   { return false }
func (b *guestProxyBridge) env(context.Context) []string { return nil }
func (b *guestProxyBridge) close()                       {}
