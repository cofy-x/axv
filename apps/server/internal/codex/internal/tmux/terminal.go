package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Scroll changes copy mode on the exact target and returns the input gate's copy
// mode hint. The owner serializes this operation with writes and resizes.
func (b Backend) Scroll(ctx context.Context, path string, target Target, lines int) (bool, error) {
	mode, err := b.Command(ctx, path, target, "display-message", "-p", "-t", target.PaneTarget(), "#{pane_in_mode}")
	if err != nil {
		return false, fmt.Errorf("Could not inspect Codex scrollback.")
	}
	if lines == 0 {
		if mode == "0" {
			return false, nil
		}
		_, err = b.Command(ctx, path, target, "send-keys", "-X", "-t", target.PaneTarget(), "cancel")
	} else {
		if mode == "0" {
			if lines < 0 {
				return false, nil
			}
			if _, err = b.Command(ctx, path, target, "copy-mode", "-t", target.PaneTarget()); err != nil {
				return false, fmt.Errorf("Could not open Codex scrollback.")
			}
		}
		direction, count := "scroll-up", lines
		if lines < 0 {
			direction, count = "scroll-down", -lines
		}
		_, err = b.Command(ctx, path, target, "send-keys", "-X", "-N", strconv.Itoa(count), "-t", target.PaneTarget(), direction)
		if err == nil && lines < 0 {
			position, inspectErr := b.Command(ctx, path, target, "display-message", "-p", "-t", target.PaneTarget(), "#{scroll_position}")
			if inspectErr == nil && strings.TrimSpace(position) == "0" {
				_, err = b.Command(ctx, path, target, "send-keys", "-X", "-t", target.PaneTarget(), "cancel")
			}
		}
	}
	if err != nil {
		return false, fmt.Errorf("Could not scroll the Codex terminal.")
	}
	return lines != 0, nil
}

// WaitSize waits for tmux to consume SIGWINCH before forwarding following
// input. tmux updates its screen before its rate-limited kernel PTY resize.
func (b Backend) WaitSize(ctx context.Context, path string, target Target, cols, rows int) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for {
		tty, err := b.Command(ctx, path, target, "display-message", "-p", "-t", target.PaneTarget(), "#{pane_tty}")
		if err != nil {
			return fmt.Errorf("Could not confirm the Codex terminal size.")
		}
		width, height, err := terminalSize(tty)
		if err != nil {
			return fmt.Errorf("Could not inspect the Codex terminal size.")
		}
		if width == cols && height == rows {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Could not confirm the Codex terminal size.")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
