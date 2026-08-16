package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

func TestIsYouTubeTraffic(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		expected bool
	}{
		{"YouTube Web", "www.youtube.com", true},
		{"Google Video CDN", "rr1---sn-4g5ednld.googlevideo.com", true},
		{"YouTube Images CDN", "i.ytimg.com", true},
		{"Google Video direct", "video.google.com", true},
		{"Shortened URL", "youtu.be", true},
		{"Google Search", "www.google.com", false},
		{"Netflix CDN", "ipv4-c001-sin001-netflix.com", false},
		{"Local Hostname", "pve.lan", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isYouTubeTraffic(tt.domain)
			if got != tt.expected {
				t.Errorf("isYouTubeTraffic(%q) = %v, expected %v", tt.domain, got, tt.expected)
			}
		})
	}
}

func TestBuildBPFFilter(t *testing.T) {
	tests := []struct {
		name     string
		targets  []string
		expected string
	}{
		{
			name:     "Empty targets",
			targets:  []string{},
			expected: "(tcp or udp) and (port 53 or port 443)",
		},
		{
			name:     "Single Host IP",
			targets:  []string{"192.168.1.50"},
			expected: "(tcp or udp) and (port 53 or port 443) and (host 192.168.1.50)",
		},
		{
			name:     "Multiple Hosts and Subnets",
			targets:  []string{"192.168.1.50", "10.0.0.0/24", "172.16.0.0/16"},
			expected: "(tcp or udp) and (port 53 or port 443) and (host 192.168.1.50 or net 10.0.0.0/24 or net 172.16.0.0/16)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildBPFFilter(tt.targets)
			if got != tt.expected {
				t.Errorf("buildBPFFilter(%v) = %q, expected %q", tt.targets, got, tt.expected)
			}
		})
	}
}

func TestExtractTLSClientHelloSNI(t *testing.T) {
	sniDomain := "rr1---sn-4g5ednld.googlevideo.com"
	sniBytes := []byte(sniDomain)

	// Build raw TLS Client Hello packet
	var payload []byte
	payload = append(payload, 0x16)       // ContentType: Handshake
	payload = append(payload, 0x03, 0x01) // TLS 1.0 record version
	payload = append(payload, 0x00, 0x00) // Dummy record length
	payload = append(payload, 0x01)       // Handshake Type: Client Hello
	payload = append(payload, 0x00, 0x00, 0x00)
	payload = append(payload, 0x03, 0x03) // Client version: TLS 1.2

	// Random 32 bytes
	payload = append(payload, make([]byte, 32)...)

	// Session ID length 0
	payload = append(payload, 0x00)

	// Cipher suites (length 2 + 2 bytes suite)
	payload = append(payload, 0x00, 0x02, 0xc0, 0x2f)

	// Compression methods (length 1 + 1 byte null compression)
	payload = append(payload, 0x01, 0x00)

	// Extensions block
	var extBlock []byte
	extBlock = append(extBlock, 0x00, 0x00) // Extension Type: SNI

	var sniExtContent []byte
	listLen := len(sniBytes) + 3
	sniExtContent = append(sniExtContent, byte(listLen>>8), byte(listLen&0xff))
	sniExtContent = append(sniExtContent, 0x00) // Hostname type
	nameLen := len(sniBytes)
	sniExtContent = append(sniExtContent, byte(nameLen>>8), byte(nameLen&0xff))
	sniExtContent = append(sniExtContent, sniBytes...)

	extBlock = append(extBlock, byte(len(sniExtContent)>>8), byte(len(sniExtContent)&0xff))
	extBlock = append(extBlock, sniExtContent...)

	payload = append(payload, byte(len(extBlock)>>8), byte(len(extBlock)&0xff))
	payload = append(payload, extBlock...)

	extractedSNI, ok := extractTLSClientHelloSNI(payload)
	if !ok {
		t.Fatalf("extractTLSClientHelloSNI failed to extract SNI")
	}

	if extractedSNI != sniDomain {
		t.Errorf("extracted SNI = %s, expected %s", extractedSNI, sniDomain)
	}

	// Test invalid / truncated payloads
	if _, ok := extractTLSClientHelloSNI([]byte{0x16, 0x03}); ok {
		t.Errorf("expected false on truncated payload")
	}
	if _, ok := extractTLSClientHelloSNI(make([]byte, 50)); ok {
		t.Errorf("expected false on non-handshake payload")
	}
}

func TestStateManagerLifecycle(t *testing.T) {
	var mu sync.Mutex
	var receivedPayloads []WebhookPayload

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err == nil {
			mu.Lock()
			receivedPayloads = append(receivedPayloads, p)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cooldown := 50 * time.Millisecond
	sm := NewStateManager(ts.URL, cooldown)

	// 1. Client 1 activity -> IDLE to ACTIVE transition
	sm.OnActivityDetected("192.168.1.105", "r1.googlevideo.com", "TLS")

	// Allow goroutine webhook dispatch
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	if len(receivedPayloads) != 1 {
		t.Fatalf("expected 1 webhook payload, got %d", len(receivedPayloads))
	}
	if receivedPayloads[0].Event != "stream_started" || receivedPayloads[0].ActiveCount != 1 {
		t.Errorf("unexpected payload 0: %+v", receivedPayloads[0])
	}
	mu.Unlock()

	// 2. Client 2 activity -> ACTIVE state update
	sm.OnActivityDetected("192.168.1.120", "youtube.com", "DNS")
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	if len(receivedPayloads) != 2 {
		t.Fatalf("expected 2 webhook payloads, got %d", len(receivedPayloads))
	}
	if receivedPayloads[1].Event != "stream_update" || receivedPayloads[1].ActiveCount != 2 {
		t.Errorf("unexpected payload 1: %+v", receivedPayloads[1])
	}
	mu.Unlock()

	// 3. Wait for client timers to expire
	time.Sleep(80 * time.Millisecond)

	mu.Lock()
	if len(receivedPayloads) < 3 {
		t.Fatalf("expected at least 3 payloads after cooldown, got %d", len(receivedPayloads))
	}
	lastPayload := receivedPayloads[len(receivedPayloads)-1]
	if lastPayload.Event != "stream_stopped" || lastPayload.ActiveCount != 0 {
		t.Errorf("expected final event stream_stopped with count 0, got %+v", lastPayload)
	}
	mu.Unlock()
}

func TestExtractSourceIP(t *testing.T) {
	// 1. IPv4 Packet
	ip4 := &layers.IPv4{
		SrcIP: net.ParseIP("192.168.1.55"),
		DstIP: net.ParseIP("8.8.8.8"),
	}
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{}
	if err := gopacket.SerializeLayers(buf, opts, ip4); err != nil {
		t.Fatalf("failed to serialize IPv4: %v", err)
	}
	pkt4 := gopacket.NewPacket(buf.Bytes(), layers.LayerTypeIPv4, gopacket.Default)
	if src := extractSourceIP(pkt4); src != "192.168.1.55" {
		t.Errorf("extractSourceIP IPv4 expected 192.168.1.55, got %s", src)
	}

	// 2. IPv6 Packet
	ip6 := &layers.IPv6{
		SrcIP: net.ParseIP("2001:db8::1"),
		DstIP: net.ParseIP("2001:db8::2"),
	}
	buf6 := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf6, opts, ip6); err != nil {
		t.Fatalf("failed to serialize IPv6: %v", err)
	}
	pkt6 := gopacket.NewPacket(buf6.Bytes(), layers.LayerTypeIPv6, gopacket.Default)
	if src := extractSourceIP(pkt6); src != "2001:db8::1" {
		t.Errorf("extractSourceIP IPv6 expected 2001:db8::1, got %s", src)
	}

	// 3. Non-IP Packet
	emptyPkt := gopacket.NewPacket([]byte{0x00, 0x01}, layers.LayerTypeEthernet, gopacket.Default)
	if src := extractSourceIP(emptyPkt); src != "" {
		t.Errorf("expected empty string for non-IP packet, got %s", src)
	}
}
