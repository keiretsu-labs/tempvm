package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type stubBackend struct {
	createCalls atomic.Int64
	deleteCalls atomic.Int64
	create      func(context.Context, io.Writer) (*Session, error)
	dial        func(context.Context, *Session) (net.Conn, error)
	delete      func(context.Context, *Session) error
}

func (b *stubBackend) Create(ctx context.Context, progress io.Writer) (*Session, error) {
	b.createCalls.Add(1)
	if b.create == nil {
		return nil, errors.New("unexpected Create call")
	}
	return b.create(ctx, progress)
}

func (b *stubBackend) Dial(ctx context.Context, session *Session) (net.Conn, error) {
	if b.dial == nil {
		return nil, errors.New("unexpected Dial call")
	}
	return b.dial(ctx, session)
}

func (b *stubBackend) Delete(ctx context.Context, session *Session) error {
	b.deleteCalls.Add(1)
	if b.delete == nil {
		return nil
	}
	return b.delete(ctx, session)
}

func TestConnectionRefusedAtCapDoesNotCreateVM(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GuestImage = "example.invalid/tempvm-linux@sha256:test"
	cfg.MaxSessions = 1
	backend := &stubBackend{
		create: func(context.Context, io.Writer) (*Session, error) {
			return nil, errors.New("Create called for a refused session")
		},
	}
	var logs bytes.Buffer
	gateway := NewWithOptions(cfg, backend, testSigner(t), testAuditLogger(&logs), nil)
	if !gateway.sessions.TryAcquire() {
		t.Fatal("failed to reserve the existing session")
	}
	defer gateway.sessions.Release()

	client, channel, requests, done := openGatewaySession(t, gateway)
	defer client.Close()
	waitForGatewayHandler(t, done)

	message, err := io.ReadAll(channel.Stderr())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(message); !strings.Contains(got, "1 of 1 VMs in use") || !strings.Contains(got, "max session 4h0m0s") {
		t.Fatalf("refusal message = %q", got)
	}
	status, ok := readExitStatus(t, requests)
	if !ok || status == 0 {
		t.Fatalf("exit status = %d, present=%t; want nonzero", status, ok)
	}
	if got := backend.createCalls.Load(); got != 0 {
		t.Fatalf("Create calls = %d, want 0", got)
	}

	events := decodeAuditEvents(t, logs.String())
	if len(events) != 1 || events[0]["event"] != "session_refused" || events[0]["reason"] != "cap" {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestBootFailureReleasesSessionReservation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GuestImage = "example.invalid/tempvm-linux@sha256:test"
	cfg.MaxSessions = 1
	backend := &stubBackend{
		create: func(context.Context, io.Writer) (*Session, error) {
			return nil, errors.New("test boot failure")
		},
	}
	var logs bytes.Buffer
	gateway := NewWithOptions(cfg, backend, testSigner(t), testAuditLogger(&logs), nil)

	client, channel, _, done := openGatewaySession(t, gateway)
	defer client.Close()
	waitForGatewayHandler(t, done)
	_, _ = io.ReadAll(channel.Stderr())

	status := gateway.Status()
	if status.ActiveSessions != 0 {
		t.Fatalf("active sessions = %d after boot failure, want 0", status.ActiveSessions)
	}
	if backend.createCalls.Load() != 1 || backend.deleteCalls.Load() != 0 {
		t.Fatalf("Create/Delete calls = %d/%d, want 1/0", backend.createCalls.Load(), backend.deleteCalls.Load())
	}
	assertAuditSequence(t, decodeAuditEvents(t, logs.String()), "session_start", "session_boot_failed", "session_stop")
}

func TestSessionTTLClosesRelayAndDeletesVM(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GuestImage = "example.invalid/tempvm-linux@sha256:test"
	cfg.MaxSessionTTL = 30 * time.Second
	cfg.MaxSessions = 1
	guestHostKey := testSigner(t)
	guestClientKey := testSigner(t)
	guestResult := make(chan error, 1)
	guestReady := make(chan string, 1)
	backend := &stubBackend{}
	backend.create = func(ctx context.Context, _ io.Writer) (*Session, error) {
		metadata := sessionMetadataFrom(ctx)
		return &Session{Name: metadata.Name, Address: "pipe", Signer: guestClientKey}, nil
	}
	backend.dial = func(ctx context.Context, _ *Session) (net.Conn, error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		go func() {
			guestSide, acceptErr := listener.Accept()
			if acceptErr != nil {
				guestResult <- acceptErr
				return
			}
			defer listener.Close()
			_ = guestSide.SetDeadline(time.Now().Add(60 * time.Second))
			guestResult <- serveTestGuest(guestSide, guestHostKey, guestReady)
		}()
		gatewaySide, err := (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
		if err != nil {
			_ = listener.Close()
		}
		return gatewaySide, err
	}

	var logs bytes.Buffer
	gateway := NewWithOptions(cfg, backend, testSigner(t), testAuditLogger(&logs), nil)
	client, channel, _, done := openGatewaySession(t, gateway)
	defer client.Close()
	select {
	case result := <-guestReady:
		if result != "SSH handshake complete" {
			t.Fatalf("guest did not finish SSH handshake: %s", result)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("guest SSH handshake did not complete")
	}
	waitForGatewayHandlerTimeout(t, done, 60*time.Second)
	message, _ := io.ReadAll(channel.Stderr())
	if !strings.Contains(string(message), "Session TTL expired") {
		t.Fatalf("session output does not report TTL expiry: %q", message)
	}

	if backend.createCalls.Load() != 1 || backend.deleteCalls.Load() != 1 {
		t.Fatalf("Create/Delete calls = %d/%d, want 1/1", backend.createCalls.Load(), backend.deleteCalls.Load())
	}
	if active := gateway.Status().ActiveSessions; active != 0 {
		t.Fatalf("active sessions after TTL = %d, want 0", active)
	}
	select {
	case err := <-guestResult:
		if err != nil {
			t.Fatalf("guest server: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("guest server did not stop after TTL")
	}

	events := decodeAuditEvents(t, logs.String())
	assertAuditSequence(t, events, "session_start", "session_ttl_expired", "session_stop")
	if got := events[len(events)-1]["stop_reason"]; got != "ttl" {
		t.Fatalf("session_stop stop_reason = %#v, want ttl", got)
	}
}

func openGatewaySession(t *testing.T, gateway *Gateway) (ssh.Conn, ssh.Channel, <-chan *ssh.Request, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for test gateway: %v", err)
	}
	done := make(chan struct{})
	go func() {
		serverSide, err := listener.Accept()
		if err == nil {
			_ = serverSide.SetDeadline(time.Now().Add(60 * time.Second))
			gateway.handleConnection(context.Background(), serverSide)
		}
		close(done)
	}()

	clientSide, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatalf("dial test gateway: %v", err)
	}
	_ = clientSide.SetDeadline(time.Now().Add(60 * time.Second))
	client, channels, requests, err := ssh.NewClientConn(clientSide, listener.Addr().String(), &ssh.ClientConfig{
		User:            "linux",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		_ = listener.Close()
		t.Fatalf("connect to gateway: %v", err)
	}
	_ = listener.Close()
	go ssh.DiscardRequests(requests)
	go func() {
		for channel := range channels {
			_ = channel.Reject(ssh.UnknownChannelType, "unexpected gateway channel")
		}
	}()
	channel, channelRequests, err := client.OpenChannel("session", nil)
	if err != nil {
		_ = client.Close()
		t.Fatalf("open gateway session: %v", err)
	}
	return client, channel, channelRequests, done
}

func serveTestGuest(raw net.Conn, hostKey ssh.Signer, ready chan<- string) error {
	defer raw.Close()
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	config.AddHostKey(hostKey)
	connection, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		ready <- "SSH handshake failed: " + err.Error()
		return err
	}
	defer connection.Close()
	ready <- "SSH handshake complete"
	go ssh.DiscardRequests(requests)

	incoming, ok := <-channels
	if !ok {
		return errors.New("gateway did not open a guest channel")
	}
	channel, channelRequests, err := incoming.Accept()
	if err != nil {
		return err
	}
	defer channel.Close()
	go func() {
		for request := range channelRequests {
			_ = request.Reply(true, nil)
		}
	}()
	_, _ = io.Copy(io.Discard, channel)
	return nil
}

func waitForGatewayHandler(t *testing.T, done <-chan struct{}) {
	waitForGatewayHandlerTimeout(t, done, 5*time.Second)
}

func waitForGatewayHandlerTimeout(t *testing.T, done <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("gateway handler did not stop")
	}
}

func readExitStatus(t *testing.T, requests <-chan *ssh.Request) (uint32, bool) {
	t.Helper()
	for request := range requests {
		if request.Type != "exit-status" {
			continue
		}
		var payload struct{ Status uint32 }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
			t.Fatalf("decode exit status: %v", err)
		}
		return payload.Status, true
	}
	return 0, false
}

func testAuditLogger(output io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	}))
}

func decodeAuditEvents(t *testing.T, output string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode audit line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func assertAuditSequence(t *testing.T, events []map[string]any, want ...string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("audit event count = %d, want %d: %#v", len(events), len(want), events)
	}
	for index, event := range events {
		if got := event["event"]; got != want[index] {
			t.Fatalf("event %d = %#v, want %q", index, got, want[index])
		}
	}
}
