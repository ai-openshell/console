package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/clients"
	"github.com/ai-openshell/console/backend/pkg/models"
)

type gatewayClients struct {
	sdk openshell.ClientInterface
	// raw is the direct gRPC client for what the SDK's own client does not
	// offer: binary-safe uploads, the keys of a provider's credentials, and
	// provider profiles with their endpoints whole.
	raw *clients.RawExecClient
}

func (c *gatewayClients) Close() {
	if c.sdk != nil {
		if err := c.sdk.Close(); err != nil {
			slog.Warn("SDK client close failed", "error", err)
		}
	}
	if c.raw != nil {
		if err := c.raw.Close(); err != nil {
			slog.Warn("raw gateway client close failed", "error", err)
		}
	}
}

func newGatewayClients(gatewayURL, gatewayCACert, gatewayClientCert, gatewayClientKey string) (*gatewayClients, error) {
	useTLS := strings.HasPrefix(gatewayURL, "grpcs://") || strings.HasPrefix(gatewayURL, "https://")
	sdkAddress := normalizeGatewayAddress(gatewayURL, useTLS)

	if (gatewayClientCert == "") != (gatewayClientKey == "") {
		return nil, fmt.Errorf("gateway mTLS requires both --gateway-client-cert and --gateway-client-key")
	}

	sdkCfg := openshell.Config{
		Address: sdkAddress,
		Auth:    clients.ContextAuthProvider{RequireTLS: useTLS},
	}
	if useTLS {
		tlsCfg := &openshell.TLSConfig{CAFile: gatewayCACert}
		if gatewayClientCert != "" {
			tlsCfg.CertFile = gatewayClientCert
			tlsCfg.KeyFile = gatewayClientKey
		}
		sdkCfg.TLS = tlsCfg
	} else {
		sdkCfg.TLS = &openshell.TLSConfig{Insecure: true}
	}

	sdkClient, err := openshell.NewClient(sdkCfg)
	if err != nil {
		return nil, fmt.Errorf("SDK client setup failed: %w", err)
	}

	rawHost := strings.TrimPrefix(strings.TrimPrefix(sdkAddress, "https://"), "http://")
	raw, err := clients.NewRawExecClient(rawHost, gatewayCACert, gatewayClientCert, gatewayClientKey, useTLS)
	if err != nil {
		if closeErr := sdkClient.Close(); closeErr != nil {
			slog.Warn("SDK client close failed during setup rollback", "error", closeErr)
		}
		return nil, fmt.Errorf("raw gateway client setup failed: %w", err)
	}

	return &gatewayClients{sdk: sdkClient, raw: raw}, nil
}

func normalizeGatewayAddress(gatewayURL string, useTLS bool) string {
	switch {
	case strings.HasPrefix(gatewayURL, "grpcs://"):
		return "https://" + strings.TrimPrefix(gatewayURL, "grpcs://")
	case strings.HasPrefix(gatewayURL, "grpc://"):
		return "http://" + strings.TrimPrefix(gatewayURL, "grpc://")
	case strings.HasPrefix(gatewayURL, "https://"), strings.HasPrefix(gatewayURL, "http://"):
		return gatewayURL
	default:
		scheme := "http"
		if useTLS {
			scheme = "https"
		}
		return fmt.Sprintf("%s://%s", scheme, gatewayURL)
	}
}

func warnGatewayConfig(gatewayURL, gatewayCACert string, authDisabled bool) {
	if gatewayURL == defaultGatewayURL {
		slog.Warn("gateway URL is the default — verify OPENSHELL_GATEWAY_URL is configured correctly", "url", gatewayURL)
	}
	if gatewayCACert != "" && !strings.HasPrefix(gatewayURL, "grpcs://") && !strings.HasPrefix(gatewayURL, "https://") {
		slog.Warn(
			"gateway CA cert is set but gateway URL has no TLS scheme; use grpcs:// or https:// for TLS gateways",
			"url", gatewayURL,
			"caCert", gatewayCACert,
		)
	}
	if authDisabled {
		slog.Warn("AUTH_DISABLED=true — authentication is OFF; never use this outside local development")
	}
}

// gatewayReleaseLine reads the gateway release line this BFF judges gateways
// against.
//
// The line is compiled into the build (models.BuiltInGatewayReleaseLine), so a
// BFF that is told nothing still reports compatibility. GATEWAY_RELEASE_LINE
// replaces it, for tests and local development; an empty value is no override.
// An override that is not a line is logged and dropped — never fatal, and
// never swapped for the built-in line: the operator asked for something else,
// so compatibility is reported as unknown until the value is fixed. The result
// only feeds a notice in the UI, so a bad value must not stop the BFF serving.
func gatewayReleaseLine(override string) models.GatewayReleaseLine {
	if strings.TrimSpace(override) == "" {
		override = models.BuiltInGatewayReleaseLine
	}
	line, err := models.ParseGatewayReleaseLine(override)
	if err != nil {
		slog.Warn(
			"gateway release line ignored — gateway compatibility will be reported as unknown; set GATEWAY_RELEASE_LINE to major.minor, or unset it to use the line this build is for",
			"error", err,
			"builtIn", models.BuiltInGatewayReleaseLine,
		)
		return line
	}
	if line.String() != models.BuiltInGatewayReleaseLine {
		slog.Info(
			"gateway release line overridden by GATEWAY_RELEASE_LINE or -gateway-release-line",
			"line", line.String(),
			"builtIn", models.BuiltInGatewayReleaseLine,
		)
	}
	return line
}

func exitOnError(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}
