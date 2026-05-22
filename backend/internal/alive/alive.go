package alive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// 常用 TCP 测活端口
var defaultTCPPorts = []string{"80", "443", "22", "3389", "21", "25", "110", "8080", "8000", "445"}

const (
	icmpTimeout = 2 * time.Second
	tcpTimeout  = 2 * time.Second
)

// Resolve 解析目标:IP/IPv4 直接返回,域名取第一个 IPv4。
func Resolve(target string) (net.IP, error) {
	ips, err := ResolveAll(target)
	if err != nil {
		return nil, err
	}
	return ips[0], nil
}

// ResolveAll 解析目标:IP/IPv4 直接返回,域名返回所有 IPv4。域名测活时逐个尝试,
// 避免 DNS 返回的第一个地址不可达导致整个域名被误判为不存活。
func ResolveAll(target string) ([]net.IP, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("empty target")
	}
	if ip := net.ParseIP(target); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return []net.IP{v4}, nil
		}
		return nil, errors.New("not ipv4")
	}
	ips, err := net.LookupIP(target)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, v4)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no ipv4 for %s", target)
	}
	return out, nil
}

// ICMPAlive 发送一个 ICMP Echo 包,需要 root/raw socket;失败回退 false。
func ICMPAlive(ip net.IP) bool {
	c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return false
	}
	defer c.Close()

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xffff,
			Seq:  1,
			Data: []byte("PING"),
		},
	}
	data, err := msg.Marshal(nil)
	if err != nil {
		return false
	}
	dst := &net.IPAddr{IP: ip}
	if _, err := c.WriteTo(data, dst); err != nil {
		return false
	}
	_ = c.SetReadDeadline(time.Now().Add(icmpTimeout))
	reply := make([]byte, 1500)
	n, _, err := c.ReadFrom(reply)
	if err != nil {
		return false
	}
	rm, err := icmp.ParseMessage(ipv4.ICMPTypeEchoReply.Protocol(), reply[:n])
	if err != nil {
		return false
	}
	return rm.Type == ipv4.ICMPTypeEchoReply
}

// TCPAlive 用常见端口尝试 TCP DialTimeout;ECONNREFUSED 也视为存活。
func TCPAlive(ip net.IP) bool {
	host := ip.String()
	for _, port := range defaultTCPPorts {
		address := net.JoinHostPort(host, port)
		conn, err := net.DialTimeout("tcp", address, tcpTimeout)
		if err == nil {
			conn.Close()
			return true
		}
		if err != nil && strings.Contains(err.Error(), "refused") {
			return true
		}
	}
	return false
}

// IsAlive 任一方式成功即认为主机存活。
func IsAlive(target string) bool {
	ips, err := ResolveAll(target)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ICMPAlive(ip) {
			return true
		}
		if TCPAlive(ip) {
			return true
		}
	}
	return false
}

// Result 测活结果
type Result struct {
	Target string
	Alive  bool
}

// CheckAll 并发测活,返回保持原顺序的结果列表。
func CheckAll(ctx context.Context, targets []string, concurrency int) []Result {
	if concurrency <= 0 {
		concurrency = 32
	}
	results := make([]Result, len(targets))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t string) {
			defer wg.Done()
			defer func() { <-sem }()
			select {
			case <-ctx.Done():
				results[i] = Result{Target: t, Alive: false}
				return
			default:
			}
			results[i] = Result{Target: t, Alive: IsAlive(t)}
		}(i, t)
	}
	wg.Wait()
	return results
}
