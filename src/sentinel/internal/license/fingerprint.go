// Package license provides hardware fingerprinting and Control Plane authorization
// for the TrustBridge Sentinel.
//
// The fingerprinting logic uses a fallback chain to generate a unique hardware
// identifier for the deployment environment: DMI UUID → Azure IMDS → hostname hash.
package license

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// FingerprintSource indicates the source of the hardware fingerprint.
type FingerprintSource string

const (
	// SourceDMI indicates the fingerprint came from DMI product UUID.
	SourceDMI FingerprintSource = "dmi"
	// SourceIMDS indicates the fingerprint came from Azure Instance Metadata Service.
	SourceIMDS FingerprintSource = "imds"
	// SourceHostname indicates the fingerprint is a hash of hostname + MAC addresses.
	SourceHostname FingerprintSource = "hostname"

	// Default paths and endpoints
	defaultDMIPath      = "/sys/class/dmi/id/product_uuid"
	defaultIMDSEndpoint = "http://169.254.169.254"
	imdsAPIVersion      = "2021-02-01"
	imdsTimeout         = 2 * time.Second
)

// HardwareFingerprint represents the generated hardware identifier with metadata.
type HardwareFingerprint struct {
	ID     string            `json:"id"`
	Source FingerprintSource `json:"source"`
}

// FingerprintGenerator provides the interface for fingerprint generation.
type FingerprintGenerator interface {
	Generate() (*HardwareFingerprint, error)
}

// DefaultFingerprintGenerator implements FingerprintGenerator using the standard
// fallback chain: DMI → IMDS → hostname.
type DefaultFingerprintGenerator struct {
	dmiPath      string
	imdsEndpoint string
	httpClient   *http.Client
}

// IMDSResponse represents the Azure Instance Metadata Service response.
type IMDSResponse struct {
	Compute struct {
		VMID string `json:"vmId"`
		Name string `json:"name"`
	} `json:"compute"`
}

// NewFingerprintGenerator creates a new DefaultFingerprintGenerator with default settings.
func NewFingerprintGenerator() *DefaultFingerprintGenerator {
	return &DefaultFingerprintGenerator{
		dmiPath:      defaultDMIPath,
		imdsEndpoint: defaultIMDSEndpoint,
		httpClient: &http.Client{
			Timeout: imdsTimeout,
		},
	}
}

// NewFingerprintGeneratorWithOptions creates a new DefaultFingerprintGenerator with
// custom paths and HTTP client for testing.
func NewFingerprintGeneratorWithOptions(dmiPath, imdsEndpoint string, client *http.Client) *DefaultFingerprintGenerator {
	g := &DefaultFingerprintGenerator{
		dmiPath:      dmiPath,
		imdsEndpoint: imdsEndpoint,
		httpClient:   client,
	}

	// Use defaults if empty
	if g.dmiPath == "" {
		g.dmiPath = defaultDMIPath
	}
	if g.imdsEndpoint == "" {
		g.imdsEndpoint = defaultIMDSEndpoint
	}
	if g.httpClient == nil {
		g.httpClient = &http.Client{Timeout: imdsTimeout}
	}

	return g
}

// Generate produces a hardware fingerprint using the fallback chain.
// It tries DMI first, then Azure IMDS, and finally falls back to hostname + MAC hash.
func (g *DefaultFingerprintGenerator) Generate() (*HardwareFingerprint, error) {
	var errs []string

	// Try DMI first
	id, err := g.tryDMI()
	if err == nil && id != "" {
		return &HardwareFingerprint{ID: id, Source: SourceDMI}, nil
	}
	errs = append(errs, fmt.Sprintf("DMI: %v", err))

	// Try Azure IMDS
	id, err = g.tryIMDS()
	if err == nil && id != "" {
		return &HardwareFingerprint{ID: id, Source: SourceIMDS}, nil
	}
	errs = append(errs, fmt.Sprintf("IMDS: %v", err))

	// Fall back to hostname + MAC hash
	id, err = g.tryHostnameFallback()
	if err == nil && id != "" {
		return &HardwareFingerprint{ID: id, Source: SourceHostname}, nil
	}
	errs = append(errs, fmt.Sprintf("hostname: %v", err))

	return nil, fmt.Errorf("failed to generate hardware fingerprint: %s", strings.Join(errs, "; "))
}

// tryDMI attempts to read the hardware UUID from DMI.
func (g *DefaultFingerprintGenerator) tryDMI() (string, error) {
	data, err := os.ReadFile(g.dmiPath)
	if err != nil {
		return "", fmt.Errorf("failed to read DMI: %w", err)
	}

	uuid := strings.TrimSpace(string(data))
	if uuid == "" {
		return "", fmt.Errorf("DMI UUID is empty")
	}

	// Normalize to lowercase
	return strings.ToLower(uuid), nil
}

// tryIMDS attempts to get the VM ID from Azure Instance Metadata Service.
func (g *DefaultFingerprintGenerator) tryIMDS() (string, error) {
	url := fmt.Sprintf("%s/metadata/instance?api-version=%s", g.imdsEndpoint, imdsAPIVersion)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create IMDS request: %w", err)
	}

	// Azure IMDS requires this header
	req.Header.Set("Metadata", "true")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("IMDS request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("IMDS returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read IMDS response: %w", err)
	}

	var imdsResp IMDSResponse
	if err := json.Unmarshal(body, &imdsResp); err != nil {
		return "", fmt.Errorf("failed to parse IMDS response: %w", err)
	}

	vmID := imdsResp.Compute.VMID
	if vmID == "" {
		return "", fmt.Errorf("IMDS response missing vmId")
	}

	// Combine with hostname for additional uniqueness
	hostname, _ := os.Hostname()
	combined := vmID
	if hostname != "" {
		combined = vmID + "-" + hostname
	}

	return strings.ToLower(combined), nil
}

// tryHostnameFallback generates a fingerprint from hostname and MAC addresses.
// This is the last resort fallback when DMI and IMDS are not available.
func (g *DefaultFingerprintGenerator) tryHostnameFallback() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("failed to get hostname: %w", err)
	}

	if hostname == "" {
		return "", fmt.Errorf("hostname is empty")
	}

	// Get MAC addresses for additional entropy
	macs := getMACAddresses()

	// Build the identifier components
	var parts []string
	parts = append(parts, hostname)
	parts = append(parts, macs...)

	// Hash the combined identifier
	return hashIdentifier(parts...), nil
}

// getMACAddresses returns a sorted list of MAC addresses from network interfaces.
func getMACAddresses() []string {
	var macs []string

	interfaces, err := net.Interfaces()
	if err != nil {
		return macs
	}

	for _, iface := range interfaces {
		// Skip loopback and interfaces without hardware addresses
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		mac := iface.HardwareAddr.String()
		if mac != "" {
			macs = append(macs, mac)
		}
	}

	return macs
}

// hashIdentifier computes a SHA-256 hash of the provided parts and returns
// the first 32 hex characters as the identifier.
func hashIdentifier(parts ...string) string {
	combined := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(combined))
	fullHex := hex.EncodeToString(hash[:])
	// Return first 32 hex characters (128 bits) for a reasonably short identifier
	return fullHex[:32]
}

// GenerateHardwareID is a convenience function that generates a hardware ID
// using the default fingerprint generator.
func GenerateHardwareID() (string, error) {
	generator := NewFingerprintGenerator()
	fp, err := generator.Generate()
	if err != nil {
		return "", err
	}
	return fp.ID, nil
}

// GenerateHardwareFingerprintWithSource is a convenience function that returns
// both the ID and the source of the fingerprint.
func GenerateHardwareFingerprintWithSource() (*HardwareFingerprint, error) {
	generator := NewFingerprintGenerator()
	return generator.Generate()
}
