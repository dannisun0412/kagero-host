package host

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const DirectPort = 2223

// Public metadata only. DNS names may be maintained by an existing DDNS client.
type Endpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func ParseEndpoint(value string) (Endpoint, error) {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return Endpoint{}, errors.New("地址格式应为 domain:port 或 [IPv6]:port")
	}
	n, err := strconv.Atoi(port)
	e := Endpoint{Host: strings.ToLower(host), Port: n}
	if err != nil || !e.valid() {
		return Endpoint{}, errors.New("直连地址或端口无效")
	}
	return e, nil
}
func (e Endpoint) valid() bool {
	if e.Port < 1 || e.Port > 65535 || len(e.Host) == 0 || len(e.Host) > 253 {
		return false
	}
	if ip, err := netip.ParseAddr(e.Host); err == nil {
		if ip.Is4() && ip.As4()[0] == 0 {
			return false
		}
		return !ip.Is4In6() && ip.Zone() == "" && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
	}
	if !strings.Contains(e.Host, ".") || strings.Trim(e.Host, "0123456789.") == "" {
		return false
	}
	for _, label := range strings.Split(e.Host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return !strings.EqualFold(e.Host, "localhost.localdomain")
}

type DirectConfig struct {
	Disabled bool       `json:"disabled"`
	Port     int        `json:"port"`
	Public   []Endpoint `json:"public"`
}

func (c DirectConfig) validate() error {
	if c.Port < 1 || c.Port > 65535 || len(c.Public) > 2 {
		return errors.New("直连端口须为 1–65535，公网入口最多两个")
	}
	for _, e := range c.Public {
		if !e.valid() {
			return errors.New("公网直连地址无效")
		}
	}
	return nil
}

// Reconfiguration replaces only the listener, never live SSH/tmux sessions.
type directListener struct {
	mu        sync.Mutex
	dir       string
	config    DirectConfig
	listener  net.Listener
	closed    bool
	lastError string
	handle    func(net.Conn)
	listen    func(string, string) (net.Listener, error)
}

func newDirectListener(dir string, handle func(net.Conn)) (*directListener, error) {
	d := &directListener{dir: dir, handle: handle, listen: net.Listen, config: DirectConfig{Port: DirectPort}}
	data, err := os.ReadFile(filepath.Join(dir, "network.json"))
	if err == nil {
		if len(data) > 4096 || json.Unmarshal(data, &d.config) != nil {
			return nil, errors.New("network.json 配置无效")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := d.config.validate(); err != nil {
		return nil, err
	}
	return d, nil
}
func (d *directListener) configure(c DirectConfig, persist bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return net.ErrClosed
	}
	if err := c.validate(); err != nil {
		return err
	}
	var next net.Listener
	if !c.Disabled {
		if d.listener != nil && c.Port == d.config.Port {
			next = d.listener
		} else {
			var err error
			next, err = d.listen("tcp", net.JoinHostPort("", strconv.Itoa(c.Port)))
			if err != nil {
				d.lastError = "直连端口无法监听，请检查端口占用"
				return err
			}
		}
	}
	if persist {
		data, err := json.MarshalIndent(c, "", "  ")
		if err == nil {
			err = AtomicWrite(filepath.Join(d.dir, "network.json"), data, 0600)
		}
		if err != nil {
			if next != nil && next != d.listener {
				next.Close()
			}
			return err
		}
	}
	previous := d.listener
	d.config, d.listener, d.lastError = c, next, ""
	if previous != nil && previous != next {
		previous.Close()
	}
	if next != nil && next != previous {
		go func() {
			for {
				conn, err := next.Accept()
				if err != nil {
					return
				}
				go d.handle(conn)
			}
		}()
	}
	return nil
}
func (d *directListener) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.listener != nil {
		d.listener.Close()
		d.listener = nil
	}
}
func (d *directListener) status() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return map[string]any{"enabled": !d.config.Disabled, "listening": d.listener != nil, "port": d.config.Port, "public": d.config.Public, "error": d.lastError}
}
func (d *directListener) endpoints() []Endpoint {
	d.mu.Lock()
	if d.listener == nil {
		d.mu.Unlock()
		return []Endpoint{}
	}
	c := d.config
	d.mu.Unlock()
	// Advertise bounded physical-interface hints; the client ranks them for its own network.
	result := append([]Endpoint{}, c.Public...)
	interfaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	var v4, v6 []string
	for _, iface := range interfaces {
		// macOS physical Ethernet/Wi-Fi and bridges; never advertise VPN/utun addresses.
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || !(strings.HasPrefix(iface.Name, "en") || strings.HasPrefix(iface.Name, "bridge")) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr()
			if !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
				continue
			}
			if ip.Is4() {
				v4 = append(v4, ip.String())
			} else {
				v6 = append(v6, ip.String())
			}
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return collectEndpoints(result, v4, v6, c.Port)
}

// Interleave both families so a Mac with many IPv6 privacy addresses still advertises IPv4.
func collectEndpoints(result []Endpoint, v4, v6 []string, port int) []Endpoint {
	seen := map[Endpoint]bool{}
	for _, e := range result {
		seen[e] = true
	}
	for i := 0; (i < len(v4) || i < len(v6)) && len(result) < 8; i++ {
		for _, family := range [][]string{v4, v6} {
			if i >= len(family) || len(result) >= 8 {
				continue
			}
			e := Endpoint{family[i], port}
			if e.valid() && !seen[e] {
				result = append(result, e)
				seen[e] = true
			}
		}
	}
	return result
}

// Keep only numeric globally routable UDP endpoints; a discovered mapping's
// port must be preserved, not replaced with the TCP listener's 2223.
func publicUDPEndpoints(addresses []string) []Endpoint {
	result := []Endpoint{}
	seen := map[Endpoint]bool{}
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	for _, raw := range addresses {
		ap, err := netip.ParseAddrPort(raw)
		if err != nil {
			continue
		}
		ip := ap.Addr()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Is4In6() || cgnat.Contains(ip) {
			continue
		}
		e := Endpoint{ip.String(), int(ap.Port())}
		if e.valid() && !seen[e] {
			result = append(result, e)
			seen[e] = true
		}
		if len(result) == 8 {
			break
		}
	}
	return result
}
