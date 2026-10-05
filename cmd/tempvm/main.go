package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/keiretsu-labs/tempvm/internal/gateway"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == "time" {
				return slog.Attr{}
			}
			return attr
		},
	})))
	if err := run(); err != nil {
		slog.Error("tempvm stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := gateway.DefaultConfig()
	flag.StringVar(&cfg.ListenAddress, "listen", envOr("TEMPVM_LISTEN", cfg.ListenAddress), "SSH listen address")
	flag.StringVar(&cfg.HealthAddress, "health-listen", envOr("TEMPVM_HEALTH_LISTEN", cfg.HealthAddress), "health HTTP listen address")
	flag.StringVar(&cfg.Namespace, "namespace", envOr("TEMPVM_NAMESPACE", cfg.Namespace), "namespace for ephemeral resources")
	flag.StringVar(&cfg.GuestImage, "guest-image", envOr("TEMPVM_GUEST_IMAGE", cfg.GuestImage), "Linux guest OCI image")
	flag.StringVar(&cfg.RuntimeClass, "runtime-class", envOr("TEMPVM_RUNTIME_CLASS", cfg.RuntimeClass), "Kubernetes RuntimeClass for guests")
	flag.StringVar(&cfg.HostKeySecret, "host-key-secret", envOr("TEMPVM_HOST_KEY_SECRET", cfg.HostKeySecret), "Secret holding the stable SSH host key")
	flag.DurationVar(&cfg.BootTimeout, "boot-timeout", durationEnvOr("TEMPVM_BOOT_TIMEOUT", cfg.BootTimeout), "maximum time to boot a guest")
	flag.DurationVar(&cfg.MaxSessionTTL, "max-session-ttl", durationEnvOr("TEMPVM_MAX_SESSION_TTL", cfg.MaxSessionTTL), "maximum session lifetime; 0 disables the TTL")
	flag.IntVar(&cfg.MaxSessions, "max-sessions", intEnvOr("TEMPVM_MAX_SESSIONS", cfg.MaxSessions), "maximum concurrent sessions; 0 disables the cap")
	flag.StringVar(&cfg.TailscaleSocket, "tailscale-socket", envOr("TEMPVM_TAILSCALE_SOCKET", cfg.TailscaleSocket), "tailscaled LocalAPI Unix socket for optional WhoIs identity")
	var allowLogins, allowTags stringListFlag
	flag.Var(&allowLogins, "allow-login", "allow a Tailscale login name; may be repeated or comma-separated")
	flag.Var(&allowTags, "allow-tag", "allow a Tailscale node tag; may be repeated or comma-separated")
	flag.Parse()
	cfg.AllowLogins = append(envList("TEMPVM_ALLOW_LOGIN"), allowLogins...)
	cfg.AllowTags = append(envList("TEMPVM_ALLOW_TAG"), allowTags...)

	if err := cfg.Validate(); err != nil {
		return err
	}

	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes configuration: %w", err)
	}
	coreClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create dynamic Kubernetes client: %w", err)
	}

	backend := gateway.NewKubernetesBackend(cfg, coreClient, dynamicClient)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := backend.RemoveOrphans(ctx); err != nil {
		return fmt.Errorf("remove orphaned sessions: %w", err)
	}
	hostKey, err := backend.HostKey(ctx)
	if err != nil {
		return fmt.Errorf("load SSH host key: %w", err)
	}
	server := gateway.New(cfg, backend, hostKey)

	sshListener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen for SSH: %w", err)
	}
	defer sshListener.Close()

	healthServer := &http.Server{
		Addr:              cfg.HealthAddress,
		Handler:           healthHandler(server),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("health server stopped", "error", err)
			stop()
		}
	}()
	defer healthServer.Shutdown(context.Background())

	slog.Info("tempvm ready", "ssh", cfg.ListenAddress, "runtimeClass", cfg.RuntimeClass)
	return server.Serve(ctx, sshListener)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationEnvOr(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func intEnvOr(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envList(name string) []string {
	return splitList(os.Getenv(name))
}

func splitList(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, splitList(value)...)
	return nil
}
