// Package ping 提供 /ping 延迟检测。
package ping

import (
	"context"
	"errors"
	"net"
	"regexp"
	"time"

	"shizukubot/sdk"
)

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)

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
	if !hostPattern.MatchString(host) {
		return c.Reply(ctx, "请输入正确的 IP 或域名，如 /ping example.com")
	}
	addr, latency, err := probeHost(ctx, host)
	if err != nil {
		return c.Reply(ctx, "ping "+host+" 失败: "+err.Error())
	}
	return c.Reply(ctx, "ping "+addr+" 成功，耗时 "+latency.Round(time.Millisecond).String())
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
