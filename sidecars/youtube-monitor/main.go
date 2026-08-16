package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// StreamState represents the current state of streaming playback.
type StreamState string

const (
	StateIdle   StreamState = "IDLE"
	StateActive StreamState = "ACTIVE"
)

// WebhookPayload represents the JSON payload dispatched to the Speedrr webhook endpoint.
type WebhookPayload struct {
	Event         string    `json:"event"`               // "stream_started", "stream_stopped", or "stream_update"
	State         string    `json:"state"`               // "ACTIVE" or "IDLE"
	Service       string    `json:"service"`             // "youtube"
	TargetIP      string    `json:"target_ip"`           // Client IP triggering the event
	ActiveClients []string  `json:"active_clients"`      // List of all active streaming client IPs in the subnet
	ActiveCount   int       `json:"active_stream_count"` // Total number of active streams in the subnet
	Matched       string    `json:"matched"`             // Matched SNI or DNS domain query
	Protocol      string    `json:"protocol"`            // "TLS" or "DNS"
	Timestamp     time.Time `json:"timestamp"`
}

// StateManager manages thread-safe streaming states and debounce cooldown timers for multiple clients/subnets.
type StateManager struct {
	mu            sync.Mutex
	currentState  StreamState
	cooldownDur   time.Duration
	activeClients map[string]*time.Timer
	webhookURL    string
	httpClient    *http.Client
}

// NewStateManager creates a new StateManager instance.
func NewStateManager(webhookURL string, cooldown time.Duration) *StateManager {
	return &StateManager{
		currentState:  StateIdle,
		cooldownDur:   cooldown,
		activeClients: make(map[string]*time.Timer),
		webhookURL:    webhookURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// OnActivityDetected processes detected streaming activity from a specific client IP.
func (sm *StateManager) OnActivityDetected(clientIP, matchedDetail, protocol string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if clientIP == "" {
		clientIP = "UNKNOWN_CLIENT"
	}

	timer, exists := sm.activeClients[clientIP]
	if exists && timer != nil {
		timer.Stop()
	} else {
		sm.handleClientJoin(clientIP, matchedDetail, protocol)
	}

	sm.activeClients[clientIP] = time.AfterFunc(sm.cooldownDur, func() {
		sm.handleClientTimeout(clientIP)
	})
}

func (sm *StateManager) handleClientJoin(clientIP, matchedDetail, protocol string) {
	isInitialActive := sm.currentState == StateIdle
	sm.currentState = StateActive

	event := "stream_update"
	if isInitialActive {
		event = "stream_started"
		log.Printf("[STATE-TRANSITION] IDLE -> ACTIVE (Client: %s | Trigger: %s via %s)", clientIP, matchedDetail, protocol)
	} else {
		log.Printf("[STATE-UPDATE] New active client: %s (Total active: %d)", clientIP, len(sm.activeClients)+1)
	}

	activeList := sm.getActiveClientListWith(clientIP)
	sm.dispatchWebhook(event, StateActive, clientIP, activeList, len(activeList), matchedDetail, protocol)
}

func (sm *StateManager) handleClientTimeout(clientIP string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.activeClients, clientIP)
	log.Printf("[COOLDOWN-TIMEOUT] Client %s finished streaming.", clientIP)

	activeList := sm.getActiveClientList()
	activeCount := len(activeList)

	if activeCount == 0 {
		sm.currentState = StateIdle
		log.Printf("[STATE-TRANSITION] ACTIVE -> IDLE (All clients in subnet are now idle)")
		sm.dispatchWebhook("stream_stopped", StateIdle, clientIP, activeList, 0, "cooldown_timeout", "SYSTEM")
	} else {
		log.Printf("[STATE-UPDATE] Remaining active clients: %v (Total: %d)", activeList, activeCount)
		sm.dispatchWebhook("stream_update", StateActive, clientIP, activeList, activeCount, "client_expired", "SYSTEM")
	}
}

func (sm *StateManager) getActiveClientList() []string {
	list := make([]string, 0, len(sm.activeClients))
	for ip := range sm.activeClients {
		list = append(list, ip)
	}
	sort.Strings(list)
	return list
}

func (sm *StateManager) getActiveClientListWith(extraIP string) []string {
	set := make(map[string]struct{})
	for ip := range sm.activeClients {
		set[ip] = struct{}{}
	}
	set[extraIP] = struct{}{}

	list := make([]string, 0, len(set))
	for ip := range set {
		list = append(list, ip)
	}
	sort.Strings(list)
	return list
}

// dispatchWebhook asynchronously dispatches the JSON webhook payload.
func (sm *StateManager) dispatchWebhook(event string, state StreamState, targetIP string, activeClients []string, activeCount int, matched, protocol string) {
	if sm.webhookURL == "" {
		return
	}

	payload := WebhookPayload{
		Event:         event,
		State:         string(state),
		Service:       "youtube",
		TargetIP:      targetIP,
		ActiveClients: activeClients,
		ActiveCount:   activeCount,
		Matched:       matched,
		Protocol:      protocol,
		Timestamp:     time.Now().UTC(),
	}

	go sm.sendWebhookRequest(sm.webhookURL, payload)
}

func (sm *StateManager) sendWebhookRequest(url string, p WebhookPayload) {
	body, err := json.Marshal(p)
	if err != nil {
		log.Printf("[WEBHOOK-ERR] Failed to marshal payload: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		log.Printf("[WEBHOOK-ERR] Failed to construct HTTP request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Speedrr-NetworkMonitor/1.0")

	resp, err := sm.httpClient.Do(req)
	if err != nil {
		log.Printf("[WEBHOOK-ERR] Failed to dispatch webhook to %s: %v", url, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[WEBHOOK-SUCCESS] %s (Client: %s, Streams: %d) -> %s (HTTP %d)", p.Event, p.TargetIP, p.ActiveCount, url, resp.StatusCode)
	} else {
		log.Printf("[WEBHOOK-WARN] Endpoint responded with status: %s", resp.Status)
	}
}

// ============================================================================
// TLS CLIENT HELLO SNI PARSER (Zero-allocation Byte Offset Extraction)
// ============================================================================

// extractTLSClientHelloSNI parses TLS Client Hello records to extract Server Name Indication (SNI).
// Packet Layout:
// [0]       : Content Type (0x16 = Handshake)
// [1..2]    : TLS Version
// [3..4]    : Record Length
// [5]       : Handshake Type (0x01 = Client Hello)
// [6..8]    : Handshake Length
// [9..10]   : Client Version
// [11..42]  : Client Random (32 bytes)
// [43]      : Session ID Length (L) -> Skip L bytes
// [44+L..]  : Cipher Suites Length -> Skip
// [...]     : Compression Methods Length -> Skip
// [...]     : Extensions Length -> Iterate searching for Type 0x0000 (SNI)
func extractTLSClientHelloSNI(payload []byte) (string, bool) {
	if !isTLSClientHello(payload) {
		return "", false
	}

	offset, ok := skipClientHelloHeader(payload)
	if !ok {
		return "", false
	}

	return scanExtensionsForSNI(payload, offset)
}

func isTLSClientHello(payload []byte) bool {
	return len(payload) >= 44 && payload[0] == 0x16 && payload[5] == 0x01
}

func skipClientHelloHeader(payload []byte) (int, bool) {
	offset := 43 // Skip Record Header (5) + Handshake Type & Len (4) + Version (2) + Random (32)

	if offset >= len(payload) {
		return 0, false
	}
	offset += 1 + int(payload[offset])

	if offset+2 > len(payload) {
		return 0, false
	}
	cipherSuitesLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	offset += 2 + cipherSuitesLen

	if offset+1 > len(payload) {
		return 0, false
	}
	compressionLen := int(payload[offset])
	offset += 1 + compressionLen

	return offset, offset <= len(payload)
}

func scanExtensionsForSNI(payload []byte, offset int) (string, bool) {
	if offset+2 > len(payload) {
		return "", false
	}
	extensionsLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	offset += 2

	extensionsEnd := offset + extensionsLen
	if extensionsEnd > len(payload) {
		extensionsEnd = len(payload)
	}

	for offset+4 <= extensionsEnd {
		extType := binary.BigEndian.Uint16(payload[offset : offset+2])
		extLen := int(binary.BigEndian.Uint16(payload[offset+2 : offset+4]))
		offset += 4

		if offset+extLen > extensionsEnd {
			break
		}

		if extType == 0x0000 {
			return parseSNIBlock(payload[offset : offset+extLen])
		}

		offset += extLen
	}

	return "", false
}

func parseSNIBlock(sniBlock []byte) (string, bool) {
	if len(sniBlock) < 5 || sniBlock[2] != 0 {
		return "", false
	}

	hostnameLen := int(binary.BigEndian.Uint16(sniBlock[3:5]))
	if 5+hostnameLen <= len(sniBlock) {
		return strings.ToLower(string(sniBlock[5 : 5+hostnameLen])), true
	}
	return "", false
}

// isYouTubeTraffic determines whether the domain/SNI matches YouTube or GoogleVideo streaming services.
func isYouTubeTraffic(domain string) bool {
	domain = strings.ToLower(domain)
	targetPatterns := []string{
		"googlevideo.com",
		"youtube.com",
		"ytimg.com",
		"youtubei.googleapis.com",
		"video.google.com",
		"youtu.be",
	}

	for _, p := range targetPatterns {
		if strings.Contains(domain, p) {
			return true
		}
	}
	return false
}

// extractSourceIP retrieves the client local IP address from the IPv4/IPv6 packet header.
func extractSourceIP(packet gopacket.Packet) string {
	if ipLayer := packet.Layer(layers.LayerTypeIPv4); ipLayer != nil {
		if ip, ok := ipLayer.(*layers.IPv4); ok {
			if isPrivateIP(ip.SrcIP) {
				return ip.SrcIP.String()
			}
			if isPrivateIP(ip.DstIP) {
				return ip.DstIP.String()
			}
			return ip.SrcIP.String()
		}
	}
	if ipLayer := packet.Layer(layers.LayerTypeIPv6); ipLayer != nil {
		if ip, ok := ipLayer.(*layers.IPv6); ok {
			if isPrivateIP(ip.SrcIP) {
				return ip.SrcIP.String()
			}
			if isPrivateIP(ip.DstIP) {
				return ip.DstIP.String()
			}
			return ip.SrcIP.String()
		}
	}
	return ""
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

func findYouTubeDomainInPayload(payload []byte) (string, bool) {
	if len(payload) == 0 {
		return "", false
	}

	// 1. Try TLS Client Hello SNI parser first
	if sni, ok := extractTLSClientHelloSNI(payload); ok && isYouTubeTraffic(sni) {
		return sni, true
	}

	// 2. Fallback byte search for streaming tokens in QUIC/HTTP3/TLS frames
	payloadLower := bytes.ToLower(payload)
	targetPatterns := [][]byte{
		[]byte("googlevideo.com"),
		[]byte("youtube.com"),
		[]byte("ytimg.com"),
		[]byte("video.google.com"),
		[]byte("youtu.be"),
		[]byte("youtubei.googleapis.com"),
	}

	for _, p := range targetPatterns {
		if bytes.Contains(payloadLower, p) {
			return string(p), true
		}
	}

	return "", false
}

// ============================================================================
// MODULAR APPLICATION RUNTIME & CAPTURE LOGIC
// ============================================================================

func initAppConfig() *Config {
	var requestedPath string
	if len(os.Args) > 1 {
		requestedPath = os.Args[1]
	}

	cfg, err := LoadConfig(requestedPath)
	if err != nil {
		log.Fatalf("[FATAL] Failed to load configuration: %v", err)
	}

	log.Printf("[CONFIG] Interface      : %s", cfg.Interface)
	log.Printf("[CONFIG] Target IPs     : %v", cfg.TargetIPs)
	log.Printf("[CONFIG] Target Subnets : %v", cfg.TargetSubnets)
	log.Printf("[CONFIG] Webhook URL    : %s", cfg.WebhookURL)
	log.Printf("[CONFIG] Cooldown       : %v", cfg.CooldownTimeout)
	log.Printf("[CONFIG] Promiscuous    : %v", cfg.Promiscuous)
	log.Printf("[CONFIG] Debug          : %v", cfg.Debug)

	return cfg
}

func openPCAPHandle(cfg *Config) *pcap.Handle {
	handle, err := pcap.OpenLive(cfg.Interface, cfg.SnapLen, cfg.Promiscuous, pcap.BlockForever)
	if err != nil {
		var ifaceNames []string
		if devs, devErr := pcap.FindAllDevs(); devErr == nil {
			for _, d := range devs {
				ifaceNames = append(ifaceNames, d.Name)
			}
		}
		log.Fatalf("[FATAL] Failed to open PCAP interface '%s': %v\nAvailable network interfaces: %v\n(Verify that network_mode: host is set and CAP_NET_RAW / root privileges are enabled)", cfg.Interface, err, ifaceNames)
	}
	return handle
}

func buildBPFFilter(targets []string) string {
	var filters []string
	for _, target := range targets {
		if strings.Contains(target, "/") {
			// Subnet / CIDR format: net 192.168.1.0/24
			filters = append(filters, fmt.Sprintf("net %s", target))
		} else if target != "" {
			// Single Host IP: host 192.168.1.50
			filters = append(filters, fmt.Sprintf("host %s", target))
		}
	}

	if len(filters) > 0 {
		return fmt.Sprintf("(tcp or udp) and (port 53 or port 443) and (%s)", strings.Join(filters, " or "))
	}
	return "(tcp or udp) and (port 53 or port 443)"
}

func applyBPFFilter(handle *pcap.Handle, targets []string) {
	bpfFilter := buildBPFFilter(targets)
	if err := handle.SetBPFFilter(bpfFilter); err != nil {
		log.Fatalf("[FATAL] Failed to compile/set BPF Filter '%s': %v", bpfFilter, err)
	}
	log.Printf("[BPF] Active kernel filter: %s", bpfFilter)
}

func processPacketStream(packetSource *gopacket.PacketSource, cfg *Config, stateMgr *StateManager) {
	for packet := range packetSource.Packets() {
		if packet == nil {
			continue
		}
		processPacket(packet, cfg, stateMgr)
	}
}

func processPacket(packet gopacket.Packet, cfg *Config, stateMgr *StateManager) {
	clientIP := extractSourceIP(packet)

	// 1. Detect DNS Query (UDP 53)
	if dnsLayer := packet.Layer(layers.LayerTypeDNS); dnsLayer != nil {
		handleDNSPacket(dnsLayer, clientIP, cfg, stateMgr)
		return
	}

	// 2. Detect TLS Client Hello SNI (TCP 443)
	if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		handleTCPPacket(tcpLayer, clientIP, stateMgr)
		return
	}

	// 3. Detect QUIC / HTTP3 & Raw DNS (UDP 443 / 53)
	if udpLayer := packet.Layer(layers.LayerTypeUDP); udpLayer != nil {
		handleUDPPacket(udpLayer, clientIP, cfg, stateMgr)
	}
}

func handleDNSPacket(dnsLayer gopacket.Layer, clientIP string, cfg *Config, stateMgr *StateManager) {
	dns, ok := dnsLayer.(*layers.DNS)
	if !ok || dns.QR {
		return
	}

	for _, q := range dns.Questions {
		domain := string(q.Name)
		if isYouTubeTraffic(domain) {
			log.Printf("[DNS-MATCH] Client %s Query: %s", clientIP, domain)
			stateMgr.OnActivityDetected(clientIP, domain, "DNS")
		}
	}
}

func handleTCPPacket(tcpLayer gopacket.Layer, clientIP string, stateMgr *StateManager) {
	tcp, ok := tcpLayer.(*layers.TCP)
	if !ok || len(tcp.Payload) == 0 {
		return
	}

	if sni, ok := extractTLSClientHelloSNI(tcp.Payload); ok && isYouTubeTraffic(sni) {
		log.Printf("[TLS-MATCH] Client %s SNI: %s", clientIP, sni)
		stateMgr.OnActivityDetected(clientIP, sni, "TLS")
		return
	}

	if matchedDomain, ok := findYouTubeDomainInPayload(tcp.Payload); ok {
		log.Printf("[TCP-MATCH] Client %s Pattern: %s", clientIP, matchedDomain)
		stateMgr.OnActivityDetected(clientIP, matchedDomain, "TCP")
	}
}

func handleUDPPacket(udpLayer gopacket.Layer, clientIP string, cfg *Config, stateMgr *StateManager) {
	udp, ok := udpLayer.(*layers.UDP)
	if !ok || len(udp.Payload) == 0 {
		return
	}

	// Fallback DNS decoding if layer not sliced
	if udp.SrcPort == 53 || udp.DstPort == 53 {
		var dns layers.DNS
		if err := dns.DecodeFromBytes(udp.Payload, gopacket.NilDecodeFeedback); err == nil && !dns.QR {
			for _, q := range dns.Questions {
				domain := string(q.Name)
				if isYouTubeTraffic(domain) {
					log.Printf("[DNS-MATCH] Client %s Query: %s", clientIP, domain)
					stateMgr.OnActivityDetected(clientIP, domain, "DNS")
					return
				}
			}
		}
	}

	// QUIC / HTTP/3 on port 443
	if udp.DstPort == 443 || udp.SrcPort == 443 {
		if matchedDomain, ok := findYouTubeDomainInPayload(udp.Payload); ok {
			log.Printf("[QUIC-MATCH] Client %s Domain: %s", clientIP, matchedDomain)
			stateMgr.OnActivityDetected(clientIP, matchedDomain, "QUIC")
		}
	}
}

func waitForShutdown() {
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)
	sig := <-shutdownChan
	log.Printf("[SHUTDOWN] Signal %v received.", sig)
}

// ============================================================================
// MAIN ENTRYPOINT
// ============================================================================

func main() {
	log.Println("================================================================")
	log.Println("  Speedrr Sidecar - YouTube Bandwidth & Stream Network Monitor  ")
	log.Println("================================================================")

	cfg := initAppConfig()
	handle := openPCAPHandle(cfg)
	defer handle.Close()

	applyBPFFilter(handle, cfg.GetAllTargets())

	stateMgr := NewStateManager(cfg.WebhookURL, cfg.CooldownTimeout)

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	packetSource.NoCopy = true

	log.Println("[INFO] Starting real-time packet capture loop...")
	go processPacketStream(packetSource, cfg, stateMgr)

	waitForShutdown()
	log.Println("[SHUTDOWN] Network monitor shut down cleanly.")
}
