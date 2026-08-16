package main

import (
	"strings"
	"testing"
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

func TestConfigGetAllTargets(t *testing.T) {
	cfg := &Config{
		TargetIPs:     []string{"192.168.1.50"},
		TargetSubnets: []string{"192.168.1.0/24", "10.0.0.0/16"},
	}

	targets := cfg.GetAllTargets()
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}

	joined := strings.Join(targets, ",")
	if !strings.Contains(joined, "192.168.1.0/24") || !strings.Contains(joined, "192.168.1.50") {
		t.Errorf("targets missing expected values: %v", targets)
	}
}

func TestExtractTLSClientHelloSNI(t *testing.T) {
	// Sample TLS 1.2 Client Hello payload with SNI = "rr1---sn-4g5ednld.googlevideo.com"
	sniDomain := "rr1---sn-4g5ednld.googlevideo.com"
	sniBytes := []byte(sniDomain)

	// Build raw TLS Client Hello packet
	var payload []byte
	payload = append(payload, 0x16)       // ContentType: Handshake
	payload = append(payload, 0x03, 0x01) // TLS 1.0 record version
	payload = append(payload, 0x00, 0x00) // Dummy record length (filled later)
	payload = append(payload, 0x01)       // Handshake Type: Client Hello
	payload = append(payload, 0x00, 0x00, 0x00) // Dummy handshake length
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
	// Extension SNI: Type 0x0000
	extBlock = append(extBlock, 0x00, 0x00)

	var sniExtContent []byte
	// ServerNameList length
	listLen := len(sniBytes) + 3
	sniExtContent = append(sniExtContent, byte(listLen>>8), byte(listLen&0xff))
	// NameType: 0 (hostname)
	sniExtContent = append(sniExtContent, 0x00)
	// Hostname length
	nameLen := len(sniBytes)
	sniExtContent = append(sniExtContent, byte(nameLen>>8), byte(nameLen&0xff))
	sniExtContent = append(sniExtContent, sniBytes...)

	// SNI Extension length
	extBlock = append(extBlock, byte(len(sniExtContent)>>8), byte(len(sniExtContent)&0xff))
	extBlock = append(extBlock, sniExtContent...)

	// Append extensions total length
	payload = append(payload, byte(len(extBlock)>>8), byte(len(extBlock)&0xff))
	payload = append(payload, extBlock...)

	extractedSNI, ok := extractTLSClientHelloSNI(payload)
	if !ok {
		t.Fatalf("extractTLSClientHelloSNI gagal mengekstrak SNI")
	}

	if extractedSNI != sniDomain {
		t.Errorf("extracted SNI = %s, expected %s", extractedSNI, sniDomain)
	}
}
