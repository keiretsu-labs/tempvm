package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
)

type Backend interface {
	Create(context.Context, io.Writer) (*Session, error)
	Delete(context.Context, *Session) error
	Dial(context.Context, *Session) (net.Conn, error)
}

type Gateway struct {
	config    Config
	backend   Backend
	sshConfig *ssh.ServerConfig
	wg        sync.WaitGroup
}

func New(config Config, backend Backend, hostKey ssh.Signer) *Gateway {
	sshConfig := &ssh.ServerConfig{
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
	return &Gateway{config: config, backend: backend, sshConfig: sshConfig}
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
			slog.Warn("accept SSH connection", "error", err)
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
		slog.Debug("SSH handshake failed", "remote", raw.RemoteAddr(), "error", err)
		return
	}
	defer front.Close()
	slog.Info("SSH session connected", "remote", front.RemoteAddr())

	waitFront := make(chan error, 1)
	go func() {
		waitFront <- front.Wait()
		cancel()
	}()

	var first ssh.NewChannel
	select {
	case <-ctx.Done():
		return
	case ch, ok := <-frontChannels:
		if !ok {
			return
		}
		first = ch
	}

	frontChannel, frontChannelRequests, err := first.Accept()
	if err != nil {
		slog.Warn("accept first SSH channel", "error", err)
		return
	}
	defer frontChannel.Close()
	progress := frontChannel.Stderr()
	_, _ = io.WriteString(progress, "Creating Linux microVM...\r\n")

	session, err := g.backend.Create(ctx, progress)
	if err != nil {
		_, _ = fmt.Fprintf(progress, "Creation failed: %v\r\n", err)
		return
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), g.config.BootTimeout)
		defer cleanupCancel()
		if err := g.backend.Delete(cleanupCtx, session); err != nil {
			slog.Warn("delete microVM", "name", session.Name, "error", err)
		}
	}()

	_, _ = io.WriteString(progress, "Linux microVM ready. Connecting...\r\n")
	backRaw, err := g.backend.Dial(ctx, session)
	if err != nil {
		_, _ = fmt.Fprintf(progress, "Connection failed: %v\r\n", err)
		return
	}
	defer backRaw.Close()

	backConfig := &ssh.ClientConfig{
		User:            "ubuntu",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(session.Signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	back, backChannels, backRequests, err := ssh.NewClientConn(backRaw, session.Address, backConfig)
	if err != nil {
		_, _ = fmt.Fprintf(progress, "Guest SSH handshake failed: %v\r\n", err)
		return
	}
	defer back.Close()

	backChannel, backChannelRequests, err := back.OpenChannel(first.ChannelType(), first.ExtraData())
	if err != nil {
		_, _ = fmt.Fprintf(progress, "Guest rejected SSH channel: %v\r\n", err)
		return
	}
	defer backChannel.Close()

	bridgeChannel(frontChannel, frontChannelRequests, backChannel, backChannelRequests)
	go bridgeGlobalRequests(frontRequests, back)
	go bridgeGlobalRequests(backRequests, front)
	go bridgeNewChannels(frontChannels, back)
	go bridgeNewChannels(backChannels, front)

	waitBack := make(chan error, 1)
	go func() {
		waitBack <- back.Wait()
	}()

	select {
	case <-ctx.Done():
	case <-waitFront:
	case <-waitBack:
	}
	slog.Info("SSH session disconnected", "remote", front.RemoteAddr(), "microVM", session.Name)
}
