package gateway

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"
)

func (g *Gateway) logSessionEvent(
	event string,
	session string,
	identity Identity,
	sourceIP string,
	user string,
	vmName string,
	stopReason string,
	duration time.Duration,
	err error,
	extra ...slog.Attr,
) {
	login, node, tags, nodeIP := identity.auditValues()
	attrs := []slog.Attr{
		slog.Time("ts", time.Now().UTC()),
		slog.String("event", event),
		slog.String("session", unknownValue(session)),
		slog.String("login_name", login),
		slog.String("node_name", node),
		slog.String("node_tags", tags),
		slog.String("node_ip", nodeIP),
		slog.String("source_ip", unknownValue(sourceIP)),
		slog.String("user", unknownValue(user)),
		slog.String("vm_namespace", unknownValue(g.config.Namespace)),
		slog.String("vm_name", unknownValue(vmName)),
	}
	if g.sessions != nil {
		active, maximum := g.sessions.Snapshot()
		attrs = append(attrs,
			slog.Int("active_sessions", active),
			slog.Int("max_sessions", maximum),
		)
	}
	if stopReason != "" {
		attrs = append(attrs, slog.String("stop_reason", stopReason))
	}
	if event == "session_stop" {
		attrs = append(attrs, slog.Float64("duration_seconds", duration.Seconds()))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	attrs = append(attrs, extra...)
	logger := g.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, event, attrs...)
}

func sourceIPFromAddr(address net.Addr) string {
	if address == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return "unknown"
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.String()
	}
	return "unknown"
}
