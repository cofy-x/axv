package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/coder/websocket"
)

const (
	maxMessageBytes = 32 << 10
	writeTimeout    = 5 * time.Second
	inputQueueWait  = 500 * time.Millisecond
)

func runSession(parent context.Context, conn *websocket.Conn, shell process) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	conn.SetReadLimit(maxMessageBytes)
	inputDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	input := make(chan terminalInput, 8)
	outputDone := make(chan struct{})
	outputStart := make(chan struct{})
	outputError := make(chan error, 1)
	pingDone := make(chan struct{})
	// Exactly one reader owns PTY output, including during ConPTY shutdown.
	go func() {
		defer close(outputDone)
		<-outputStart
		buffer := make([]byte, 16<<10)
		forward := true
		for {
			n, err := shell.Read(buffer)
			if n > 0 && forward {
				if writeErr := writeMessage(ctx, conn, websocket.MessageBinary, buffer[:n]); writeErr != nil {
					outputError <- writeErr
					forward = false
				}
			}
			if err != nil {
				return
			}
		}
	}()
	// Ready is the first frame; start draining even if delivering it fails.
	ready := writeControl(ctx, conn, map[string]bool{"ready": true})
	close(outputStart)
	if !ready {
		cancel()
		shell.Close()
		<-outputDone
		return
	}
	go func() { inputDone <- readInput(ctx, conn, input) }()
	go func() { writeDone <- applyInput(ctx, shell, input) }()
	go func() {
		defer close(pingDone)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, done := context.WithTimeout(ctx, writeTimeout)
				err := conn.Ping(pingCtx)
				done()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	var inputFinished, writeFinished, natural bool
	select {
	case err := <-inputDone:
		inputFinished = true
		if err != nil && websocket.CloseStatus(err) == -1 && ctx.Err() == nil {
			writeControl(ctx, conn, map[string]string{"error": err.Error()})
		}
	case err := <-writeDone:
		writeFinished = true
		if err != nil && ctx.Err() == nil {
			writeControl(ctx, conn, map[string]string{"error": err.Error()})
		}
	case <-shell.Done():
		natural = true
	case <-outputDone:
		select {
		case <-shell.Done():
			natural = true
		case <-time.After(100 * time.Millisecond):
		}
	case <-outputError:
	case <-ctx.Done():
	}
	// Cancellation must actively close the PTY even if input is blocked writing.
	// Keep draining output during Close; older ConPTY versions require this.
	if natural {
		// Drain final output before closing the master. ConPTY may not produce EOF
		// until Close, so this grace period is bounded on every platform.
		select {
		case <-outputDone:
		case <-time.After(250 * time.Millisecond):
		}
		shell.Close()
		select {
		case <-outputDone:
		case <-time.After(writeTimeout):
		}
		closeSocket(conn, websocket.StatusNormalClosure, "Session ended")
	} else {
		cancel()
		_ = conn.CloseNow()
		shell.Close()
	}
	cancel()
	_ = conn.CloseNow()
	if !inputFinished {
		<-inputDone
	}
	if !writeFinished {
		<-writeDone
	}
	<-outputDone
	<-pingDone
}

type terminalInput struct {
	data []byte
	cols int
	rows int
}

func readInput(ctx context.Context, conn *websocket.Conn, input chan<- terminalInput) error {
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		frame := terminalInput{data: data}
		if kind == websocket.MessageText {
			var control struct {
				Resize []int `json:"resize"`
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&control) != nil || len(control.Resize) != 2 || !validSize(control.Resize[0], control.Resize[1]) {
				return fmt.Errorf("Send binary terminal input or a resize control with cols 2–500 and rows 1–200.")
			}
			var trailing any
			if decoder.Decode(&trailing) != io.EOF {
				return fmt.Errorf("Send one resize control at a time.")
			}
			frame = terminalInput{cols: control.Resize[0], rows: control.Resize[1]}
		}
		if err = enqueueInput(ctx, input, frame); err != nil {
			return err
		}
	}
}

func enqueueInput(ctx context.Context, input chan<- terminalInput, frame terminalInput) error {
	select {
	case input <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	// Character-sized browser frames can briefly outrun the PTY writer. Keep
	// the same bounded queue, but give healthy bursts time to drain before failing.
	timer := time.NewTimer(inputQueueWait)
	defer timer.Stop()
	select {
	case input <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("Terminal input is arriving faster than the shell can accept it.")
	}
}

func applyInput(ctx context.Context, shell process, input <-chan terminalInput) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame := <-input:
			if frame.cols != 0 {
				if err := shell.Resize(frame.cols, frame.rows); err != nil {
					return fmt.Errorf("Could not resize the host terminal.")
				}
				continue
			}
			for len(frame.data) > 0 {
				n, err := shell.Write(frame.data)
				if err != nil || n == 0 {
					return fmt.Errorf("The shell stopped accepting input.")
				}
				frame.data = frame.data[n:]
			}
		}
	}
}

func writeMessage(ctx context.Context, conn *websocket.Conn, kind websocket.MessageType, data []byte) error {
	deadline, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(deadline, kind, data)
}

func writeControl(ctx context.Context, conn *websocket.Conn, value any) bool {
	data, _ := json.Marshal(value)
	return writeMessage(ctx, conn, websocket.MessageText, data) == nil
}

func closeSocket(conn *websocket.Conn, code websocket.StatusCode, reason string) {
	// Bound even a peer that does not acknowledge the close handshake.
	timer := time.AfterFunc(time.Second, func() { _ = conn.CloseNow() })
	defer timer.Stop()
	_ = conn.Close(code, reason)
}
