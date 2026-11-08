// Package ping 提供 /ping 延迟检测。
package ping

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"time"

	"shizukubot/sdk"
)

// hostPattern 校验域名（也接受 IPv4 字面量）；IPv6 由 net.ParseIP 单独识别。
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// Definition registers /ping.
func Definition() sdk.Definition {
	return sdk.CommandPlugin(sdk.Meta{
		ID:          "ping",
		Name:        "Ping",
		Version:     "1.0.0",
		Description: "不带参数回复 pong；带主机名时检测 TCP 连通延迟",
	}, "ping", Handle)
}

// Handle replies pong or probes the requested host over TCP. TCP 探测无需
// ICMP 权限，因此 Windows 下也不需要管理员身份。
func Handle(ctx context.Context, c sdk.Call) error {
	if len(c.Args) == 0 {
		return c.Reply(ctx, "pong")
	}
	host := c.Args[0]
	normalized, ok := normalizeHost(host)
	if !ok {
		return c.Reply(ctx, "请输入正确的 IP 或域名，如 /ping example.com")
	}
	addr, latency, err := probeHost(ctx, normalized)
	if err != nil {
		return c.Reply(ctx, "ping "+host+" 失败: "+err.Error())
	}
	return c.Reply(ctx, "ping "+addr+" 成功，耗时 "+latency.Round(time.Millisecond).String())
}

// normalizeHost 校验主机名/IPv4/IPv6 字面量（可带端口），并去掉 IPv6 的方括号，
// 便于后续 net.JoinHostPort 正确拼接。上游 isValidateInput 同样接受 IPv6。
func normalizeHost(host string) (string, bool) {
	if host == "" || strings.ContainsAny(host, "/ \t") {
		return "", false
	}
	if h, port, err := net.SplitHostPort(host); err == nil {
		if validPort(port) && validHostName(h) {
			return host, true
		}
		return "", false
	}
	trimmed := host
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	if net.ParseIP(trimmed) != nil || hostPattern.MatchString(trimmed) {
		return trimmed, true
	}
	return "", false
}

func validHostName(host string) bool {
	return net.ParseIP(host) != nil || hostPattern.MatchString(host)
}

func validPort(port string) bool {
	if port == "" || len(port) > 5 {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func probeHost(ctx context.Context, host string) (string, time.Duration, error) {
	ports := []string{"443", "80"}
	if _, _, err := net.SplitHostPort(host); err == nil {
		ports = []string{""}
	}
	var last error
	for _, port := range ports {
		addr := host
		if port != "" {
			addr = net.JoinHostPort(host, port)
		}
		start := time.Now()
		dialer := net.Dialer{Timeout: 3 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			last = err
			continue
		}
		_ = conn.Close()
		return addr, time.Since(start), nil
	}
	if last == nil {
		last = errors.New("no probe address")
	}
	return host, 0, last
}
