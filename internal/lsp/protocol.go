package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

const maxMessageBytes = 16 << 20

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type connection struct {
	reader    *bufio.Reader
	writer    io.Writer
	writeMu   sync.Mutex
	cancelMu  sync.Mutex
	cancelled map[string]bool
}

func newConnection(reader io.Reader, writer io.Writer) *connection {
	return &connection{reader: bufio.NewReader(reader), writer: writer, cancelled: make(map[string]bool)}
}

func requestIDKey(id json.RawMessage) string {
	return string(id)
}

func (c *connection) cancel(id json.RawMessage) {
	if len(id) == 0 {
		return
	}
	c.cancelMu.Lock()
	c.cancelled[requestIDKey(id)] = true
	c.cancelMu.Unlock()
}

func (c *connection) isCancelled(id json.RawMessage) bool {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	return c.cancelled[requestIDKey(id)]
}

func (c *connection) cancelledResponse(value any) any {
	responseValue, ok := value.(response)
	if !ok || len(responseValue.ID) == 0 {
		return value
	}
	key := requestIDKey(responseValue.ID)
	c.cancelMu.Lock()
	cancelled := c.cancelled[key]
	delete(c.cancelled, key)
	c.cancelMu.Unlock()
	if !cancelled {
		return value
	}
	return response{
		JSONRPC: "2.0",
		ID:      responseValue.ID,
		Error:   &responseError{Code: -32800, Message: "Request cancelled"},
	}
}

func (c *connection) read() ([]byte, error) {
	contentLength := -1
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		length, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || length < 0 || length > maxMessageBytes {
			return nil, fmt.Errorf("invalid Content-Length %q", strings.TrimSpace(value))
		}
		contentLength = length
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *connection) write(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	value = c.cancelledResponse(value)
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.writer.Write(body)
	return err
}
