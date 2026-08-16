package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// StreamState status pemutaran media
type StreamState string

const (
	StateIdle   StreamState = "IDLE"
	StateActive StreamState = "ACTIVE"
)

// WebhookPayload struktur payload JSON yang dikirimkan ke endpoint Speedrr
type WebhookPayload struct {
	Event     string    `json:"event"`     // "stream_started" atau "stream_stopped"
	State     string    `json:"state"`     // "ACTIVE" atau "IDLE"
	Service   string    `json:"service"`   // "youtube"
	TargetIP  string    `json:"target_ip"` // IP perangkat yang memutar streaming
	Matched   string    `json:"matched"`   // SNI atau Domain DNS yang tertangkap
	Protocol  string    `json:"protocol"`  // "TLS" atau "DNS"
	Timestamp time.Time `json:"timestamp"`
}

// StateManager mengelola state streaming secara thread-safe dengan timer cooldown
type StateManager struct {
	mu           sync.Mutex
	currentState StreamState
	cooldownDur  time.Duration
	timer        *time.Timer
	targetIP     string
	webhookURL   string
	httpClient   *http.Client
}

// NewStateManager inisialisasi state manager
func NewStateManager(targetIP, webhookURL string, cooldown time.Duration) *StateManager {
	return &StateManager{
		currentState: StateIdle,
		cooldownDur:  cooldown,
		targetIP:     targetIP,
		webhookURL:   webhookURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// OnActivityDetected menangani deteksi paket streaming baru
func (sm *StateManager) OnActivityDetected(matchedDetail, protocol string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Jika state sebelumnya IDLE, ubah ke ACTIVE dan kirim webhook event start
	if sm.currentState == StateIdle {
		sm.currentState = StateActive
		log.Printf("[STATE-TRANSITION] IDLE -> ACTIVE (Pemicu: %s via %s)", matchedDetail, protocol)
		sm.dispatchWebhook("stream_started", StateActive, matchedDetail, protocol)
	}

	// Reset cooldown timer setiap kali paket baru masuk
	if sm.timer != nil {
		sm.timer.Stop()
	}

	sm.timer = time.AfterFunc(sm.cooldownDur, func() {
		sm.mu.Lock()
		defer sm.mu.Unlock()

		// Saat timer habis tanpa paket baru, ubah state ke IDLE dan kirim webhook event stop
		sm.currentState = StateIdle
		log.Printf("[STATE-TRANSITION] ACTIVE -> IDLE (Cooldown timeout %v tercapai)", sm.cooldownDur)
		sm.dispatchWebhook("stream_stopped", StateIdle, "cooldown_timeout", "SYSTEM")
	})
}

// dispatchWebhook mengirimkan notifikasi HTTP POST JSON secara asynchronous
func (sm *StateManager) dispatchWebhook(event string, state StreamState, matched, protocol string) {
	if sm.webhookURL == "" {
		return
	}

	payload := WebhookPayload{
		Event:     event,
		State:     string(state),
		Service:   "youtube",
		TargetIP:  sm.targetIP,
		Matched:   matched,
		Protocol:  protocol,
		Timestamp: time.Now().UTC(),
	}

	go sm.sendWebhookRequest(sm.webhookURL, payload)
}

func (sm *StateManager) sendWebhookRequest(url string, p WebhookPayload) {
	body, err := json.Marshal(p)
	if err != nil {
		log.Printf("[WEBHOOK-ERR] Gagal marshal payload: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		log.Printf("[WEBHOOK-ERR] Gagal membuat HTTP request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Speedrr-NetworkMonitor/1.0")

	resp, err := sm.httpClient.Do(req)
	if err != nil {
		log.Printf("[WEBHOOK-ERR] Gagal mengirim ke %s: %v", url, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[WEBHOOK-SUCCESS] %s -> %s (HTTP %d)", p.Event, url, resp.StatusCode)
	} else {
		log.Printf("[WEBHOOK-WARN] Endpoint merespons status: %s", resp.Status)
	}
}

// ============================================================================
// TLS CLIENT HELLO SNI PARSER (Zero-allocation Byte Offset Extraction)
// ============================================================================

// extractTLSClientHelloSNI mengekstrak Server Name Indication (SNI) dari payload TLS Handshake
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
	// [0]: ContentType 0x16 (Handshake), [5]: HandshakeType 0x01 (Client Hello)
	return len(payload) >= 44 && payload[0] == 0x16 && payload[5] == 0x01
}

func skipClientHelloHeader(payload []byte) (int, bool) {
	offset := 43 // Titik awal setelah Client Random (5 + 4 + 2 + 32)

	// Lewati Session ID
	if offset >= len(payload) {
		return 0, false
	}
	offset += 1 + int(payload[offset])

	// Lewati Cipher Suites
	if offset+2 > len(payload) {
		return 0, false
	}
	cipherSuitesLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	offset += 2 + cipherSuitesLen

	// Lewati Compression Methods
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
	// sniBlock[2]: NameType (0 = Hostname), sniBlock[3..5]: Hostname length
	if len(sniBlock) < 5 || sniBlock[2] != 0 {
		return "", false
	}

	hostnameLen := int(binary.BigEndian.Uint16(sniBlock[3:5]))
	if 5+hostnameLen <= len(sniBlock) {
		return strings.ToLower(string(sniBlock[5 : 5+hostnameLen])), true
	}
	return "", false
}

// isYouTubeTraffic memeriksa apakah domain atau SNI merupakan target streaming YouTube
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

// ============================================================================
// MODULAR APPLICATION RUNTIME & CAPTURE LOGIC
// ============================================================================

func initAppConfig() *Config {
	configPath := "config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("[FATAL] Gagal membaca konfigurasi: %v", err)
	}

	log.Printf("[CONFIG] Interface   : %s", cfg.Interface)
	log.Printf("[CONFIG] Target IPs  : %v", cfg.TargetIPs)
	log.Printf("[CONFIG] Webhook URL : %s", cfg.WebhookURL)
	log.Printf("[CONFIG] Cooldown    : %v", cfg.CooldownTimeout)
	log.Printf("[CONFIG] Promiscuous : %v", cfg.Promiscuous)

	return cfg
}

func openPCAPHandle(cfg *Config) *pcap.Handle {
	handle, err := pcap.OpenLive(cfg.Interface, cfg.SnapLen, cfg.Promiscuous, pcap.BlockForever)
	if err != nil {
		log.Fatalf("[FATAL] Gagal membuka interface '%s': %v\n(Perlu privilege root / CAP_NET_RAW)", cfg.Interface, err)
	}
	return handle
}

func applyBPFFilter(handle *pcap.Handle, targetIPs []string) {
	var hostFilters []string
	for _, ip := range targetIPs {
		hostFilters = append(hostFilters, fmt.Sprintf("host %s", ip))
	}

	var bpfFilter string
	if len(hostFilters) > 0 {
		bpfFilter = fmt.Sprintf("(tcp or udp) and (port 53 or port 443) and (%s)", strings.Join(hostFilters, " or "))
	} else {
		bpfFilter = "(tcp or udp) and (port 53 or port 443)"
	}

	if err := handle.SetBPFFilter(bpfFilter); err != nil {
		log.Fatalf("[FATAL] Gagal memasang BPF Filter '%s': %v", bpfFilter, err)
	}
	log.Printf("[BPF] Filter aktif di kernel: %s", bpfFilter)
}

func initStateManager(cfg *Config) *StateManager {
	primaryTarget := "MULTIPLE_TARGETS"
	if len(cfg.TargetIPs) > 0 {
		primaryTarget = cfg.TargetIPs[0]
	}
	return NewStateManager(primaryTarget, cfg.WebhookURL, cfg.CooldownTimeout)
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
	// 1. Deteksi DNS Query (UDP 53)
	if dnsLayer := packet.Layer(layers.LayerTypeDNS); dnsLayer != nil {
		handleDNSPacket(dnsLayer, cfg, stateMgr)
		return
	}

	// 2. Deteksi TLS Client Hello SNI (TCP 443)
	if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		handleTCPPacket(tcpLayer, stateMgr)
	}
}

func handleDNSPacket(dnsLayer gopacket.Layer, cfg *Config, stateMgr *StateManager) {
	dns, ok := dnsLayer.(*layers.DNS)
	if !ok || dns.QR {
		return
	}

	for _, q := range dns.Questions {
		domain := string(q.Name)
		if isYouTubeTraffic(domain) {
			if cfg.Debug {
				log.Printf("[DNS-MATCH] Query: %s", domain)
			}
			stateMgr.OnActivityDetected(domain, "DNS")
		}
	}
}

func handleTCPPacket(tcpLayer gopacket.Layer, stateMgr *StateManager) {
	tcp, ok := tcpLayer.(*layers.TCP)
	if !ok || len(tcp.Payload) == 0 {
		return
	}

	sni, ok := extractTLSClientHelloSNI(tcp.Payload)
	if ok && isYouTubeTraffic(sni) {
		log.Printf("[TLS-MATCH] Terdeteksi Streaming SNI: %s", sni)
		stateMgr.OnActivityDetected(sni, "TLS")
	}
}

func waitForShutdown() {
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)
	sig := <-shutdownChan
	log.Printf("[SHUTDOWN] Sinyal %v diterima.", sig)
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

	applyBPFFilter(handle, cfg.TargetIPs)

	stateMgr := initStateManager(cfg)

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	packetSource.NoCopy = true

	log.Println("[INFO] Memulai loop penangkapan paket real-time...")
	go processPacketStream(packetSource, cfg, stateMgr)

	waitForShutdown()
	log.Println("[SHUTDOWN] Monitor berhasil dimatikan dengan aman.")
}
