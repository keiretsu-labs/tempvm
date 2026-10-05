package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type Backend interface {
	Create(context.Context, io.Writer) (*Session, error)
	Delete(context.Context, *Session) error
	Dial(context.Context, *Session) (net.Conn, error)
}

type Gateway struct {
	config           Config
	backend          Backend
	sshConfig        *ssh.ServerConfig
	logger           *slog.Logger
	identityResolver IdentityResolver
	sessions         *sessionLimiter
	wg               sync.WaitGroup
}

func New(config Config, backend Backend, hostKey ssh.Signer) *Gateway {
	return NewWithOptions(config, backend, hostKey, slog.Default(), newTailscaleIdentityResolver(config.TailscaleSocket))
}

func NewWithOptions(config Config, backend Backend, hostKey ssh.Signer, logger *slog.Logger, resolver IdentityResolver) *Gateway {
	if logger == nil {
		logger = slog.Default()
	}
	sshConfig := &ssh.ServerConfig{
		NoClientAuth: true,
		NoClientAuthCallback: func(meta ssh.ConnMetadata) (*ssh.Permissions, error) {
			if meta.User() != "linux" {
				return nil, fmt.Errorf("unsupported user %q; connect as linux", meta.User())
			}
			return nil, nil
		},
		ServerVersion: "SSH-2.0-tempvm",
		BannerCallback: func(_ ssh.ConnMetadata) string {
			return "Ephemeral Linux microVM. This machine exists only for the life of this SSH connection.\r\n"
		},
	}
	sshConfig.AddHostKey(hostKey)
	return &Gateway{
		config:           config,
		backend:          backend,
		sshConfig:        sshConfig,
		logger:           logger,
		identityResolver: resolver,
		sessions:         newSessionLimiter(config.MaxSessions),
	}
}

func (g *Gateway) Status() Status {
	if g.sessions == nil {
		return Status{MaxSessionTTL: g.config.MaxSessionTTL}
	}
	active, max := g.sessions.Snapshot()
	return Status{
		ActiveSessions: active,
		MaxSessions:    max,
		MaxSessionTTL:  g.config.MaxSessionTTL,
	}
}

func (g *Gateway) Serve(ctx context.Context, listener net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				g.wg.Wait()
				return nil
			}
			g.logger.Warn("accept SSH connection", "error", err)
			continue
		}
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			g.handleConnection(ctx, conn)
		}()
	}
}

func (g *Gateway) handleConnection(parent context.Context, raw net.Conn) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer raw.Close()

	go func() {
		<-ctx.Done()
		_ = raw.Close()
	}()

	front, frontChannels, frontRequests, err := ssh.NewServerConn(raw, g.sshConfig)
	if err != nil {
		g.logger.Debug("SSH handshake failed", "source_ip", sourceIPFromAddr(raw.RemoteAddr()), "error", err)
		return
	}
	defer front.Close()

	sourceIP := sourceIPFromAddr(front.RemoteAddr())
	user := front.User()
	identity := g.resolveIdentity(ctx, front.RemoteAddr())
	frontDone := make(chan struct{})
	go func() {
		_ = front.Wait()
		close(frontDone)
		cancel()
	}()

	var first ssh.NewChannel
	select {
	case <-parent.Done():
		return
	case <-frontDone:
		return
	case ch, ok := <-frontChannels:
		if !ok {
			return
		}
		first = ch
	}

	frontChannel, frontChannelRequests, err := first.Accept()
	if err != nil {
		g.logger.Warn("accept first SSH channel", "source_ip", sourceIP, "error", err)
		return
	}
	frontOutput := newChannelOutput(frontChannel)
	defer frontOutput.Close()
	progress := frontOutput.StderrWriter()

	if !identity.allowed(g.config) {
		g.logSessionEvent(
			"session_refused", "unknown", identity, sourceIP, user, "unknown", "",
			0, nil, slog.String("reason", "allow_list"),
		)
		refuseSession(frontChannel, progress, "tempvm: connection refused; tailnet identity is not on the allow-list\r\n")
		return
	}

	if g.sessions != nil && !g.sessions.TryAcquire() {
		active, max := g.sessions.Snapshot()
		message := fmt.Sprintf("tempvm: %d of %d VMs in use; try again when one closes (max session %s)\r\n", active, max, formatTTL(g.config.MaxSessionTTL))
		g.logSessionEvent(
			"session_refused", "unknown", identity, sourceIP, user, "unknown", "",
			0, nil,
			slog.String("reason", "cap"),
			slog.Int("active_sessions", active),
			slog.Int("max_sessions", max),
		)
		refuseSession(frontChannel, progress, message)
		return
	}

	attemptName, nameErr := randomName()
	if nameErr != nil {
		attemptName = "unknown"
	}
	startedAt := time.Now()
	sessionName := attemptName
	vmName := "unknown"
	stopReason := "client_disconnect"
	bootFailureLogged := false
	ttlExpiredLogged := false

	var session *Session
	var warningTimer *time.Timer
	var ttlTimer *time.Timer
	deadline := time.Time{}
	stopTimers := func() {
		if warningTimer != nil {
			warningTimer.Stop()
		}
		if ttlTimer != nil {
			ttlTimer.Stop()
		}
	}
	defer func() {
		duration := time.Since(startedAt)
		stopTimers()
		if session != nil {
			cleanupTimeout := g.config.BootTimeout
			if cleanupTimeout <= 0 {
				cleanupTimeout = 5 * time.Minute
			}
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupTimeout)
			if err := g.backend.Delete(cleanupCtx, session); err != nil {
				g.logger.Warn("delete microVM", "name", session.Name, "error", err)
			}
			cleanupCancel()
		}
		if g.sessions != nil {
			g.sessions.Release()
		}
		g.logSessionEvent("session_stop", sessionName, identity, sourceIP, user, vmName, stopReason, duration, nil)
	}()

	if nameErr != nil {
		bootFailureLogged = true
		g.logSessionEvent("session_boot_failed", sessionName, identity, sourceIP, user, vmName, "", 0, nameErr)
		stopReason = stopReasonForConnection(parent, frontDone, "boot_failure")
		_, _ = fmt.Fprintf(progress, "Creation failed: %v\r\n", nameErr)
		return
	}
	vmName = attemptName
	g.logSessionEvent("session_start", sessionName, identity, sourceIP, user, vmName, "", 0, nil)

	operationCtx := ctx
	var operationCancel context.CancelFunc
	if g.config.MaxSessionTTL > 0 {
		deadline = startedAt.Add(g.config.MaxSessionTTL)
		operationCtx, operationCancel = context.WithDeadline(ctx, deadline)
		defer operationCancel()
	}
	operationCtx = withSessionMetadata(operationCtx, attemptName, startedAt)

	writeExpiryWarning := func() {
		if g.config.MaxSessionTTL > 0 {
			remaining := time.Until(deadline).Round(time.Second)
			if remaining < time.Second {
				remaining = time.Second
			}
			_, _ = fmt.Fprintf(progress, "Warning: this Linux microVM will expire in about %s.\r\n", remaining)
		}
	}
	if g.config.MaxSessionTTL > 0 {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		if remaining > 5*time.Minute {
			warningTimer = time.AfterFunc(remaining-5*time.Minute, writeExpiryWarning)
		} else {
			writeExpiryWarning()
		}
		ttlTimer = time.NewTimer(remaining)
	}

	recordTTLExpired := func() {
		if ttlExpiredLogged {
			return
		}
		ttlExpiredLogged = true
		g.logSessionEvent("session_ttl_expired", sessionName, identity, sourceIP, user, vmName, "ttl", 0, nil)
	}
	connectionFailureReason := func() string {
		if parent.Err() != nil {
			return "gateway_shutdown"
		}
		select {
		case <-frontDone:
			return "client_disconnect"
		default:
			return "boot_failure"
		}
	}
	recordBootFailure := func(err error) {
		if bootFailureLogged {
			return
		}
		bootFailureLogged = true
		g.logSessionEvent("session_boot_failed", sessionName, identity, sourceIP, user, vmName, "", 0, err)
	}
	handleBootFailure := func(err error, progressMessage string) {
		if operationCtx.Err() == context.DeadlineExceeded && parent.Err() == nil {
			recordTTLExpired()
			stopReason = "ttl"
		} else {
			stopReason = connectionFailureReason()
			recordBootFailure(err)
		}
		_, _ = fmt.Fprintf(progress, "%s: %v\r\n", progressMessage, err)
	}

	_, _ = io.WriteString(progress, "Creating Linux microVM...\r\n")
	session, err = g.backend.Create(operationCtx, progress)
	if err != nil {
		handleBootFailure(err, "Creation failed")
		return
	}
	if session == nil {
		err = errors.New("backend returned a nil session")
		handleBootFailure(err, "Creation failed")
		return
	}
	if strings.TrimSpace(session.Name) == "" {
		session.Name = attemptName
	}
	sessionName = session.Name
	vmName = session.Name

	_, _ = io.WriteString(progress, "Linux microVM ready. Connecting...\r\n")
	backRaw, err := g.backend.Dial(operationCtx, session)
	if err != nil {
		handleBootFailure(err, "Connection failed")
		return
	}
	defer backRaw.Close()
	stopCloseBackOnCancel := context.AfterFunc(operationCtx, func() { _ = backRaw.Close() })
	defer stopCloseBackOnCancel()

	backConfig := &ssh.ClientConfig{
		User:            "ubuntu",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(session.Signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	back, backChannels, backRequests, err := ssh.NewClientConn(backRaw, session.Address, backConfig)
	if err != nil {
		handleBootFailure(err, "Guest SSH handshake failed")
		return
	}
	defer back.Close()

	backChannel, backChannelRequests, err := back.OpenChannel(first.ChannelType(), first.ExtraData())
	if err != nil {
		handleBootFailure(err, "Guest rejected SSH channel")
		return
	}
	backOutput := newChannelOutput(backChannel)
	defer backOutput.Close()

	channelDone := bridgeSessionChannel(frontOutput, frontChannelRequests, backOutput, backChannelRequests)
	go bridgeGlobalRequests(frontRequests, back)
	go bridgeGlobalRequests(backRequests, front)
	go bridgeNewChannels(frontChannels, back)
	go bridgeNewChannels(backChannels, front)

	waitBack := make(chan error, 1)
	go func() {
		waitBack <- back.Wait()
	}()

	var ttlC <-chan time.Time
	if ttlTimer != nil {
		ttlC = ttlTimer.C
	}
	select {
	case <-parent.Done():
		stopReason = "gateway_shutdown"
	case <-frontDone:
		stopReason = g.terminalStopReason(parent, deadline, "client_disconnect")
	case <-channelDone:
		stopReason = g.terminalStopReason(parent, deadline, "shell_exit")
	case <-waitBack:
		stopReason = g.terminalStopReason(parent, deadline, "shell_exit")
	case <-ttlC:
		stopReason = "ttl"
	}
	if stopReason == "ttl" {
		_, _ = io.WriteString(progress, "Session TTL expired; closing this microVM.\r\n")
		cancel()
		_ = frontOutput.Close()
		_ = backOutput.Close()
		_ = front.Close()
		_ = back.Close()
		recordTTLExpired()
	}
}

func refuseSession(channel ssh.Channel, progress io.Writer, message string) {
	_, _ = io.WriteString(progress, message)
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 1}))
}

func (g *Gateway) terminalStopReason(parent context.Context, deadline time.Time, fallback string) string {
	if parent.Err() != nil {
		return "gateway_shutdown"
	}
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return "ttl"
	}
	return fallback
}

func (g *Gateway) resolveIdentity(ctx context.Context, remote net.Addr) Identity {
	if g.identityResolver == nil || remote == nil {
		return Identity{}
	}
	identityCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	identity, err := g.identityResolver.Resolve(identityCtx, remote.String())
	if err != nil {
		g.logger.Debug("resolve Tailscale identity", "source_ip", sourceIPFromAddr(remote), "error", err)
		return Identity{}
	}
	return identity
}

func stopReasonForConnection(parent context.Context, frontDone <-chan struct{}, fallback string) string {
	if parent.Err() != nil {
		return "gateway_shutdown"
	}
	select {
	case <-frontDone:
		return "client_disconnect"
	default:
		return fallback
	}
}

func formatTTL(ttl time.Duration) string {
	if ttl <= 0 {
		return "disabled"
	}
	return ttl.String()
}
