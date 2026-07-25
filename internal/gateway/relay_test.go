package gateway

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestBridgeChannelSignalsGuestCompletion(t *testing.T) {
	frontOutput := &bytes.Buffer{}
	front := &testChannel{reader: strings.NewReader("input"), writer: frontOutput}
	guest := &testChannel{reader: strings.NewReader("output"), writer: io.Discard}
	frontRequests := make(chan *ssh.Request)
	guestRequests := make(chan *ssh.Request)
	close(frontRequests)
	close(guestRequests)

	done := bridgeChannel(front, frontRequests, guest, guestRequests)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridge did not report the guest channel closing")
	}

	if got := frontOutput.String(); got != "output" {
		t.Fatalf("front output = %q, want output", got)
	}
}

type testChannel struct {
	reader io.Reader
	writer io.Writer
}

func (c *testChannel) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *testChannel) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *testChannel) Close() error                { return nil }
func (c *testChannel) CloseWrite() error           { return nil }
func (c *testChannel) SendRequest(string, bool, []byte) (bool, error) {
	return true, nil
}
func (c *testChannel) Stderr() io.ReadWriter { return &emptyReadWriter{} }

type emptyReadWriter struct{}

func (*emptyReadWriter) Read([]byte) (int, error) { return 0, io.EOF }
func (*emptyReadWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
