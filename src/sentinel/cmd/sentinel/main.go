// Package main implements the TrustBridge Sentinel entry point.
//
// The Sentinel is a security sidecar that:
//  1. Authorizes with the Control Plane using hardware fingerprinting
//  2. Downloads and verifies encrypted model assets
//  3. Decrypts assets to a FIFO for runtime consumption
//  4. Exposes health endpoints for Kubernetes probes
//
// The startup sequence follows the state machine:
// Boot → Authorize → Hydrate → Decrypt → Ready
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"trustbridge/sentinel/internal/asset"
	"trustbridge/sentinel/internal/billing"
	"trustbridge/sentinel/internal/config"
	"trustbridge/sentinel/internal/crypto"
	"trustbridge/sentinel/internal/health"
	"trustbridge/sentinel/internal/license"
	"trustbridge/sentinel/internal/proxy"
	"trustbridge/sentinel/internal/state"
)

// Version information (set at build time).
var (
	Version   = "0.1.0"
	BuildTime = "unknown"
)

// sentinelLogger wraps slog.Logger to implement state.Logger interface.
type sentinelLogger struct {
	logger *slog.Logger
}

func (l *sentinelLogger) Info(msg string, keysAndValues ...interface{}) {
	l.logger.Info(msg, keysAndValues...)
}

func (l *sentinelLogger) Error(msg string, keysAndValues ...interface{}) {
	l.logger.Error(msg, keysAndValues...)
}

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("TrustBridge Sentinel starting",
		"version", Version,
		"build_time", BuildTime,
	)

	// Run the sentinel
	if err := run(logger); err != nil {
		logger.Error("Sentinel failed", "error", err.Error())
		os.Exit(1)
	}
}

// run executes the sentinel lifecycle.
func run(logger *slog.Logger) error {
	// Create context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("Received shutdown signal", "signal", sig.String())
		cancel()
	}()

	// Initialize state machine
	stateMachine := state.New(
		state.WithLogger(&sentinelLogger{logger: logger}),
	)

	// PHASE: Boot - Load configuration
	logger.Info("Phase: Boot - Loading configuration")
	cfg, err := loadConfig(logger)
	if err != nil {
		stateMachine.Suspend(fmt.Sprintf("configuration error: %v", err))
		return fmt.Errorf("boot failed: %w", err)
	}

	stateMachine.SetAssetID(cfg.AssetID)
	logger.Info("Configuration loaded", "config", cfg.String())

	// Start health server immediately (returns 503 until Ready)
	healthServer := health.NewServer(stateMachine, health.WithAddr(cfg.HealthAddr))
	if err := healthServer.Start(); err != nil {
		logger.Warn("Failed to start health server", "error", err.Error())
	} else {
		logger.Info("Health server started", "addr", cfg.HealthAddr)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		healthServer.Stop(shutdownCtx)
	}()

	// PHASE: Authorize - Call Control Plane
	if err := stateMachine.Transition(state.StateAuthorize); err != nil {
		return fmt.Errorf("failed to transition to Authorize: %w", err)
	}
	logger.Info("Phase: Authorize - Calling Control Plane")

	authResp, err := authorize(ctx, cfg, logger)
	if err != nil {
		stateMachine.Suspend(fmt.Sprintf("authorization failed: %v", err))
		return fmt.Errorf("authorize failed: %w", err)
	}
	logger.Info("Authorization successful",
		"expires_at", authResp.ExpiresAt.Format(time.RFC3339),
	)

	// PHASE: Hydrate - Download and verify assets
	if err := stateMachine.Transition(state.StateHydrate); err != nil {
		return fmt.Errorf("failed to transition to Hydrate: %w", err)
	}
	logger.Info("Phase: Hydrate - Downloading assets")

	manifest, encryptedPath, err := hydrate(ctx, cfg, authResp, logger)
	if err != nil {
		stateMachine.Suspend(fmt.Sprintf("hydration failed: %v", err))
		return fmt.Errorf("hydrate failed: %w", err)
	}
	logger.Info("Hydration complete",
		"encrypted_path", encryptedPath,
		"plaintext_bytes", manifest.PlaintextBytes,
	)

	// PHASE: Decrypt - Create FIFO and start decryption
	if err := stateMachine.Transition(state.StateDecrypt); err != nil {
		return fmt.Errorf("failed to transition to Decrypt: %w", err)
	}
	logger.Info("Phase: Decrypt - Starting decryption to FIFO")

	decryptionKey, err := hex.DecodeString(authResp.DecryptionKeyHex)
	if err != nil {
		stateMachine.Suspend(fmt.Sprintf("invalid decryption key: %v", err))
		return fmt.Errorf("invalid decryption key: %w", err)
	}

	// Start async decryption to FIFO
	decryptResultCh := crypto.DecryptToFIFO(
		ctx,
		encryptedPath,
		cfg.PipePath,
		decryptionKey,
		crypto.WithLogger(logger),
		crypto.WithTotalBytes(manifest.PlaintextBytes),
	)

	// Write ready signal for runtime
	if err := crypto.WriteReadySignal(cfg.ReadySignal); err != nil {
		stateMachine.Suspend(fmt.Sprintf("failed to write ready signal: %v", err))
		return fmt.Errorf("failed to write ready signal: %w", err)
	}
	logger.Info("Ready signal written", "path", cfg.ReadySignal)

	// PHASE: Ready - Model weights available
	if err := stateMachine.Transition(state.StateReady); err != nil {
		return fmt.Errorf("failed to transition to Ready: %w", err)
	}
	logger.Info("Phase: Ready - Sentinel is ready",
		"health_endpoint", fmt.Sprintf("http://%s/health", cfg.HealthAddr),
		"fifo_path", cfg.PipePath,
	)

	// Create billing components if enabled
	var billingCounter *billing.Counter
	var billingAgent *billing.Agent
	var billingMiddleware *billing.Middleware

	if cfg.BillingEnabled {
		billingCounter = billing.NewCounter()
		billingMiddleware = billing.NewMiddleware(billingCounter)
		logger.Info("Billing enabled",
			"interval", cfg.BillingInterval.String(),
			"dimension", cfg.BillingDimension,
		)
	}

	// Start proxy server
	proxyOpts := []proxy.ServerOption{
		proxy.WithLogger(logger),
	}
	if billingMiddleware != nil {
		proxyOpts = append(proxyOpts, proxy.WithBillingMiddleware(billingMiddleware))
	}

	proxyServer := proxy.NewServer(
		stateMachine,
		&proxy.ProxyConfig{
			PublicAddr: cfg.PublicAddr,
			RuntimeURL: cfg.RuntimeURL,
			ContractID: cfg.ContractID,
			AssetID:    cfg.AssetID,
		},
		proxyOpts...,
	)
	if err := proxyServer.Start(); err != nil {
		stateMachine.Suspend(fmt.Sprintf("proxy start failed: %v", err))
		return fmt.Errorf("failed to start proxy: %w", err)
	}
	logger.Info("Proxy server started",
		"addr", cfg.PublicAddr,
		"runtime", cfg.RuntimeURL,
	)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		proxyServer.Stop(shutdownCtx)
	}()

	// Start billing agent if enabled
	if cfg.BillingEnabled && billingCounter != nil {
		var reporter billing.MeterReporter

		// Use real metering client for production, log reporter for testing
		if cfg.MeteringEndpoint == config.DefaultMeteringEndpoint {
			reporter = billing.NewMeteringClient(billing.MeteringConfig{
				Endpoint:   cfg.MeteringEndpoint,
				ResourceID: cfg.BillingResourceID,
				PlanID:     cfg.BillingPlanID,
				Dimension:  cfg.BillingDimension,
			}, billing.WithMeteringLogger(&sentinelLogger{logger: logger}))
		} else {
			// Non-default endpoint means testing mode - use log reporter
			reporter = billing.NewLogReporter(&sentinelLogger{logger: logger})
			logger.Info("Using stub billing reporter for testing",
				"metering_endpoint", cfg.MeteringEndpoint,
			)
		}

		billingAgent = billing.NewAgent(
			billingCounter,
			reporter,
			stateMachine.Suspend,
			billing.WithConfig(billing.AgentConfig{
				Interval:   cfg.BillingInterval,
				ContractID: cfg.ContractID,
				AssetID:    cfg.AssetID,
				ResourceID: cfg.BillingResourceID,
				Dimension:  cfg.BillingDimension,
			}),
			billing.WithLogger(&sentinelLogger{logger: logger}),
		)

		if err := billingAgent.Start(); err != nil {
			logger.Error("Failed to start billing agent", "error", err.Error())
		} else {
			logger.Info("Billing agent started",
				"interval", cfg.BillingInterval.String(),
				"resource_id", cfg.BillingResourceID,
			)
		}

		defer func() {
			if billingAgent != nil && billingAgent.IsRunning() {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer shutdownCancel()
				if err := billingAgent.Stop(shutdownCtx); err != nil {
					logger.Error("Failed to stop billing agent", "error", err.Error())
				}
			}
		}()
	}

	// Wait for either:
	// 1. Context cancellation (shutdown signal)
	// 2. Decryption completion (success or error)
	select {
	case <-ctx.Done():
		logger.Info("Shutdown requested, waiting for decryption to complete")
		// Wait a bit for decryption to finish gracefully
		select {
		case result := <-decryptResultCh:
			if result.Err != nil {
				logger.Error("Decryption failed during shutdown", "error", result.Err.Error())
			} else {
				logger.Info("Decryption completed", "bytes_written", result.BytesWritten)
			}
		case <-time.After(5 * time.Second):
			logger.Warn("Decryption did not complete within timeout")
		}
		return nil

	case result := <-decryptResultCh:
		if result.Err != nil {
			stateMachine.Suspend(fmt.Sprintf("decryption failed: %v", result.Err))
			return fmt.Errorf("decryption failed: %w", result.Err)
		}
		logger.Info("Decryption completed successfully",
			"bytes_written", result.BytesWritten,
		)

		// Keep running until shutdown signal
		logger.Info("Sentinel running, waiting for shutdown signal...")
		<-ctx.Done()
		return nil
	}
}

// loadConfig loads and validates configuration from environment variables.
func loadConfig(logger *slog.Logger) (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// Adjust log level if specified
	switch cfg.LogLevel {
	case "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "warn":
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	}

	return cfg, nil
}

// authorize calls the Control Plane to request authorization.
func authorize(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*license.AuthResponse, error) {
	// Generate hardware fingerprint
	logger.Info("Generating hardware fingerprint")
	fingerprint, err := license.GenerateHardwareFingerprintWithSource()
	if err != nil {
		return nil, fmt.Errorf("failed to generate hardware fingerprint: %w", err)
	}
	logger.Info("Hardware fingerprint generated",
		"source", string(fingerprint.Source),
		"id_prefix", fingerprint.ID[:8]+"...",
	)

	// Create license client
	client := license.NewLicenseClient(
		cfg.EDCEndpoint,
		license.WithClientVersion(fmt.Sprintf("sentinel/%s", Version)),
	)

	// Authorize
	logger.Info("Calling Control Plane for authorization",
		"endpoint", cfg.EDCEndpoint,
		"contract_id", cfg.ContractID,
		"asset_id", cfg.AssetID,
	)

	resp, err := client.Authorize(ctx, cfg.ContractID, cfg.AssetID, fingerprint.ID)
	if err != nil {
		return nil, fmt.Errorf("authorization request failed: %w", err)
	}

	return resp, nil
}

// hydrate downloads the manifest and encrypted asset, then verifies integrity.
func hydrate(ctx context.Context, cfg *config.Config, authResp *license.AuthResponse, logger *slog.Logger) (*asset.Manifest, string, error) {
	// Download manifest
	logger.Info("Downloading manifest", "url_prefix", truncateURL(authResp.ManifestUrl))
	manifest, err := asset.DownloadManifest(ctx, authResp.ManifestUrl)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download manifest: %w", err)
	}
	logger.Info("Manifest downloaded and validated",
		"asset_id", manifest.AssetID,
		"plaintext_bytes", manifest.PlaintextBytes,
		"chunk_bytes", manifest.ChunkBytes,
	)

	// Prepare download path
	encryptedPath := filepath.Join(cfg.TargetDir, manifest.WeightsFilename)
	logger.Info("Downloading encrypted asset",
		"url_prefix", truncateURL(authResp.SASUrl),
		"target", encryptedPath,
	)

	// Download encrypted asset with concurrency
	downloader := asset.NewDownloader(
		asset.WithConcurrency(cfg.DownloadConcurrency),
		asset.WithChunkBytes(cfg.DownloadChunkBytes),
		asset.WithProgressCallback(func(downloaded, total int64) {
			// Progress is logged by the downloader
		}),
	)

	expectedSize := manifest.CiphertextSize()
	result, err := downloader.DownloadFileConcurrent(ctx, authResp.SASUrl, encryptedPath, expectedSize)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download encrypted asset: %w", err)
	}
	logger.Info("Download complete",
		"bytes_downloaded", result.BytesWritten,
		"duration", result.Duration.String(),
	)

	// Verify hash
	logger.Info("Verifying asset integrity")
	if err := asset.VerifyFileHash(encryptedPath, manifest.SHA256Ciphertext); err != nil {
		// Clean up the downloaded file
		os.Remove(encryptedPath)
		return nil, "", fmt.Errorf("integrity verification failed: %w", err)
	}
	logger.Info("Asset integrity verified")

	return manifest, encryptedPath, nil
}

// truncateURL returns a truncated URL for logging (hides SAS tokens).
func truncateURL(url string) string {
	if len(url) <= 50 {
		return url
	}
	// Find the path part before query string
	for i, c := range url {
		if c == '?' {
			if i > 50 {
				return url[:50] + "..."
			}
			return url[:i] + "?..."
		}
	}
	return url[:50] + "..."
}
