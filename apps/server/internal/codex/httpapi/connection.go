package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/codex"
)

func dimensions(raw string) (int, int, bool) {
	query, err := url.ParseQuery(raw)
	cols, rows := 80, 24
	if err != nil {
		return 0, 0, false
	}
	for key, values := range query {
		if (key != "cols" && key != "rows") || len(values) != 1 {
			return 0, 0, false
		}
		n, err := strconv.Atoi(values[0])
		if err != nil {
			return 0, 0, false
		}
		if key == "cols" {
			cols = n
		} else {
			rows = n
		}
	}
	return cols, rows, validSize(cols, rows)
}

func validSize(cols, rows int) bool {
	return cols >= 2 && cols <= 500 && rows >= 1 && rows <= 200
}

func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r, true) {
		return
	}
	cols, rows, ok := dimensions(r.URL.RawQuery)
	if !ok {
		respond(w, nil, requestFailure(400, "invalid_terminal_size", "Use cols 2–500 and rows 1–200; no other query parameters are accepted."))
		return
	}
	sessionID, runID := r.PathValue("id"), r.PathValue("run")
	lease, err := h.manager.PrepareAttachment(r.Context(), sessionID, runID, cols, rows)
	if err != nil {
		respond(w, nil, err)
		return
	}
	defer lease.Close()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	if err := lease.Open(r.Context()); err != nil {
		message := "Could not attach to the persistent Codex session."
		var service *codex.Error
		if errors.As(err, &service) {
			message = service.Message
		}
		_ = writeControl(r.Context(), conn, map[string]string{"error": message, "code": "attach_failed"})
		return
	}
	bridge(r.Context(), conn, lease, runID, func(ctx context.Context) (codex.Session, error) {
		return lease.Inspect(ctx)
	})
}

// The transport consumes only the attachment capabilities needed by its wire
// protocol. Capacity and run ownership remain with the Codex service.
type attachment interface {
	Read([]byte) (int, error)
	Write(context.Context, []byte) error
	Resize(context.Context, int, int) error
	Scroll(context.Context, int) error
	Done() <-chan struct{}
	ExitError() error
	Detach()
}

type inputFrame struct {
	data       []byte
	cols, rows int
	scroll     *int
}

type outputResult struct {
	err         error
	fromProcess bool
}

func readInput(ctx context.Context, conn *websocket.Conn, queue chan<- inputFrame) error {
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		frame := inputFrame{data: data}
		if kind == websocket.MessageText {
			var control struct {
				Resize []int `json:"resize"`
				Scroll *int  `json:"scroll"`
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			var trailing any
			if decoder.Decode(&control) != nil || decoder.Decode(&trailing) != io.EOF {
				return fmt.Errorf("Send one valid resize or scroll control.")
			}
			if control.Scroll != nil {
				if control.Resize != nil || *control.Scroll < -200 || *control.Scroll > 200 {
					return fmt.Errorf("Scroll must be an integer from -200 to 200 without resize.")
				}
				frame = inputFrame{scroll: control.Scroll}
			} else {
				if len(control.Resize) != 2 || !validSize(control.Resize[0], control.Resize[1]) {
					return fmt.Errorf("Use columns 2–500 and rows 1–200.")
				}
				frame = inputFrame{cols: control.Resize[0], rows: control.Resize[1]}
			}
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case queue <- frame:
			timer.Stop()
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("Codex input is arriving faster than the terminal can accept it.")
		}
	}
}

func applyInput(ctx context.Context, p attachment, queue <-chan inputFrame) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame := <-queue:
			var err error
			switch {
			case frame.cols != 0:
				err = p.Resize(ctx, frame.cols, frame.rows)
			case frame.scroll != nil:
				err = p.Scroll(ctx, *frame.scroll)
			default:
				err = p.Write(ctx, frame.data)
			}
			if err != nil {
				return err
			}
		}
	}
}

func writeControl(ctx context.Context, conn *websocket.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeMessage(ctx, conn, websocket.MessageText, data)
}

func writeMessage(ctx context.Context, conn *websocket.Conn, kind websocket.MessageType, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(ctx, kind, data)
}

func final(status string) bool {
	return status == "ended" || status == "lost" || status == "failed"
}

func bridge(parent context.Context, conn *websocket.Conn, p attachment, runID string, inspect func(context.Context) (codex.Session, error)) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer p.Detach()
	conn.SetReadLimit(32 << 10)
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); <-ctx.Done(); _ = conn.CloseNow() }()
	defer func() { cancel(); <-watchDone }()
	if writeControl(ctx, conn, map[string]bool{"ready": true}) != nil {
		return
	}
	queue := make(chan inputFrame, 8)
	reader, writer := make(chan error, 1), make(chan error, 1)
	output := make(chan outputResult, 1)
	go func() { reader <- readInput(ctx, conn, queue) }()
	go func() { writer <- applyInput(ctx, p, queue) }()
	go func() {
		buffer := make([]byte, 16<<10)
		for {
			n, err := p.Read(buffer)
			if n > 0 {
				if writeErr := writeMessage(ctx, conn, websocket.MessageBinary, buffer[:n]); writeErr != nil {
					output <- outputResult{err: writeErr}
					return
				}
			}
			if err != nil {
				output <- outputResult{err: err, fromProcess: true}
				return
			}
		}
	}()
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	var inputFinished, writeFinished, outputFinished bool
	var result, attachmentErr error
	message := ""
loop:
	for {
		select {
		case result = <-reader:
			inputFinished = true
			break loop
		case result = <-writer:
			writeFinished = true
			break loop
		case event := <-output:
			outputFinished = true
			result = event.err
			if event.fromProcess {
				attachmentErr = event.err
			}
			break loop
		case <-p.Done():
			result = fmt.Errorf("The tmux attachment exited.")
			attachmentErr = result
			break loop
		case <-ctx.Done():
			break loop
		case <-ping.C:
			pingCtx, done := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			done()
			if err != nil {
				result = err
				break loop
			}
		case <-ticker.C:
			session, err := inspect(ctx)
			if err != nil {
				result = err
				break loop
			}
			if final(session.Status) {
				message = session.Reason
				break loop
			}
		}
	}
	if ctx.Err() == nil {
		active := false
		if message == "" {
			session, err := inspect(ctx)
			if err == nil && final(session.Status) {
				message = session.Reason
			} else if err == nil {
				active = true
			} else if result == nil {
				result = err
			}
		}
		if message != "" {
			if !outputFinished {
				select {
				case <-output:
					outputFinished = true
				case <-time.After(5 * time.Second):
				}
			}
			_ = writeControl(ctx, conn, map[string]any{"ended": true, "message": message})
		} else if result != nil && websocket.CloseStatus(result) == -1 {
			detail := result.Error()
			if attachmentErr != nil {
				log.Printf("Codex terminal attachment ended: run=%s io_error=%v exit_error=%v", runID, attachmentErr, p.ExitError())
				detail = "The terminal connection closed. Reconnect to check the Codex run."
				if active {
					detail = "The terminal connection closed while this Codex run is still active. Reconnect to continue."
				}
			}
			_ = writeControl(ctx, conn, map[string]string{"error": detail, "code": "connection_failed"})
		}
	}
	cancel()
	_ = conn.CloseNow()
	p.Detach()
	if !inputFinished {
		<-reader
	}
	if !writeFinished {
		<-writer
	}
	if !outputFinished {
		<-output
	}
}
