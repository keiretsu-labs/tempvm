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
	"syscall"
	"time"

	"github.com/keiretsu-labs/tempvm/internal/gateway"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
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
	flag.Parse()

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

	sshListener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen for SSH: %w", err)
	}
	defer sshListener.Close()

	healthServer := &http.Server{
		Addr:              cfg.HealthAddress,
		Handler:           healthHandler(),
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
	return gateway.New(cfg, backend, hostKey).Serve(ctx, sshListener)
}

func healthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", landingHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
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
