// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package server implements the DHCP + iPXE server servers.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jackpal/gateway"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/siderolabs/booter/internal/server/assets"
	"github.com/siderolabs/booter/internal/server/config"
	"github.com/siderolabs/booter/internal/server/dhcp"
	"github.com/siderolabs/booter/internal/server/imagefactory"
	"github.com/siderolabs/booter/internal/server/ipxe"
	"github.com/siderolabs/booter/internal/server/machineconfig"
	"github.com/siderolabs/booter/internal/server/omni"
	"github.com/siderolabs/booter/internal/server/server"
	"github.com/siderolabs/booter/internal/server/tftp"
)

// OmniEndpointEnvVar is the environment variable that contains the Omni endpoint.
const OmniEndpointEnvVar = "OMNI_ENDPOINT"

// Server implements the server.
type Server struct {
	logger *zap.Logger

	options Options
}

// New creates a new Server.
func New(options Options, logger *zap.Logger) *Server {
	return &Server{
		options: options,
		logger:  logger,
	}
}

// Run runs the server.
//
//nolint:gocyclo,cyclop
func (s *Server) Run(ctx context.Context) error {
	var err error

	s.options.APIAdvertiseAddress, err = s.determineAPIAdvertiseAddress()
	if err != nil {
		return fmt.Errorf("failed to determine API advertise address: %w", err)
	}

	if s.options.DHCPProxyIfaceOrIP == "" {
		s.logger.Info("DHCP proxy interface or IP is not explicitly defined, the interface of the API advertise address will be used by the DHCP proxy",
			zap.String("address", s.options.APIAdvertiseAddress))

		s.options.DHCPProxyIfaceOrIP = s.options.APIAdvertiseAddress
	}

	// Validate local assets configuration
	localAssetsEnabled := s.options.LocalAssetsPath != ""
	if localAssetsEnabled {
		// Validate incompatible flags
		if len(s.options.Extensions) > 0 {
			return fmt.Errorf("--local-assets-path cannot be used with --extensions (schematics only apply to Image Factory)")
		}

		if s.options.SchematicID != "" {
			return fmt.Errorf("--local-assets-path cannot be used with --schematic-id (schematics only apply to Image Factory)")
		}

		s.logger.Info("local assets mode enabled", zap.String("path", s.options.LocalAssetsPath))
	}

	// Resolve iPXE and TFTP paths
	const defaultIPXEPath = "/var/lib/ipxe"
	const defaultTFTPPath = "/var/lib/tftp"

	ipxePath := defaultIPXEPath
	tftpPath := defaultTFTPPath
	skipIPXEPatching := false

	// Check if local assets path has pre-patched iPXE binaries
	if localAssetsEnabled {
		prePatchedTFTPPath := filepath.Join(s.options.LocalAssetsPath, "tftp")
		if s.hasPatchedIPXEBinaries(prePatchedTFTPPath) {
			ipxePath = prePatchedTFTPPath
			tftpPath = prePatchedTFTPPath
			skipIPXEPatching = true
			s.logger.Info("using pre-patched iPXE binaries from local assets",
				zap.String("path", prePatchedTFTPPath))
		}
	}

	if !skipIPXEPatching {
		s.logger.Info("using iPXE and TFTP paths",
			zap.String("ipxe_path", ipxePath),
			zap.String("tftp_path", tftpPath))
	}

	configServerEnabled := s.options.Omni.APIEndpoint != ""

	s.logger.Info("starting server",
		zap.Any("options", s.options),
	)

	var configHandler http.Handler

	if configServerEnabled {
		var machineConfig []byte

		var omniConnOpts omni.ConnectionOptions

		if omniConnOpts, err = omni.GetConnectionOptions(ctx, s.options.Omni.APIEndpoint, s.options.Omni.APIInsecureSkipTLSVerify, s.logger); err != nil {
			return fmt.Errorf("failed to get Omni connection options: %w", err)
		}

		if machineConfig, err = machineconfig.Build(omniConnOpts); err != nil {
			return fmt.Errorf("failed to build machine config: %w", err)
		}

		if configHandler, err = config.NewHandler(machineConfig, s.logger.With(zap.String("component", "config_handler"))); err != nil {
			return fmt.Errorf("failed to create config handler: %w", err)
		}
	}

	imageFactoryClient, err := imagefactory.NewClient(s.options.ImageFactoryBaseURL, s.options.ImageFactoryPXEBaseURL,
		s.options.SecureBootEnabled, s.logger.With(zap.String("component", "image_factory_client")))
	if err != nil {
		return fmt.Errorf("failed to create image factory client: %w", err)
	}

	if s.options.TalosVersion == "" {
		if localAssetsEnabled {
			return fmt.Errorf("--talos-version is required when using --local-assets-path")
		}

		if s.options.TalosVersion, err = imageFactoryClient.GetLatestStableVersion(ctx); err != nil {
			return fmt.Errorf("failed to get the latest stable Talos version from the image factory: %w", err)
		}

		s.logger.Info("Talos version is not explicitly defined, the latest stable Talos version from the image factory will be used",
			zap.String("version", s.options.TalosVersion))
	}

	// Create assets handler if local assets are enabled
	var assetsHandler http.Handler

	var localAssetsBaseURL string

	if localAssetsEnabled {
		assetsHandler, err = assets.NewHandler(s.options.LocalAssetsPath, s.options.TalosVersion, s.options.SecureBootEnabled,
			s.logger.With(zap.String("component", "assets_handler")))
		if err != nil {
			return fmt.Errorf("failed to create assets handler: %w", err)
		}

		localAssetsBaseURL = "http://" + net.JoinHostPort(s.options.APIAdvertiseAddress, strconv.Itoa(s.options.APIPort)) + "/assets"
	}

	ipxeHandler, err := ipxe.NewHandler(ctx, configServerEnabled, imageFactoryClient, ipxe.HandlerOptions{
		APIAdvertiseAddress: s.options.APIAdvertiseAddress,
		APIPort:             s.options.APIPort,
		Extensions:          s.options.Extensions,
		ExtraKernelArgs:     s.options.ExtraKernelArgs,
		TalosVersion:        s.options.TalosVersion,
		SchematicID:         s.options.SchematicID,
		LocalAssetsEnabled:  localAssetsEnabled,
		LocalAssetsBaseURL:  localAssetsBaseURL,
		SecureBootEnabled:   s.options.SecureBootEnabled,
		IPXEPath:            ipxePath,
		TFTPPath:            tftpPath,
		SkipPatching:        skipIPXEPatching,
	}, s.logger.With(zap.String("component", "ipxe_handler")))
	if err != nil {
		return fmt.Errorf("failed to create iPXE handler: %w", err)
	}

	tftpServer := tftp.NewServer(s.options.APIListenAddress, tftpPath, s.logger.With(zap.String("component", "tftp_server")))
	srvr := server.New(ctx, s.options.APIListenAddress, s.options.APIPort, configHandler, ipxeHandler, assetsHandler, ipxePath, s.logger.With(zap.String("component", "server")))

	components := []component{
		{srvr.Run, "server"},
		{tftpServer.Run, "TFTP server"},
	}

	if !s.options.DisableDHCPProxy {
		dhcpProxy := dhcp.NewProxy(s.options.APIAdvertiseAddress, s.options.APIPort, s.options.DHCPProxyIfaceOrIP, s.logger.With(zap.String("component", "dhcp_proxy")))

		components = append(components, component{dhcpProxy.Run, "DHCP proxy"})
	}

	return s.runComponents(ctx, components)
}

type component struct {
	run  func(context.Context) error
	name string
}

// runComponents runs the long-running components in their own goroutines.
//
// It will terminate all components when one of them terminates, irrespective of whether it terminates with an error.
func (s *Server) runComponents(ctx context.Context, components []component) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eg, ctx := errgroup.WithContext(ctx)

	for _, comp := range components {
		logger := s.logger.With(zap.String("component", comp.name))

		eg.Go(func() error {
			defer cancel() // cancel the parent context, so all other components are also stopped even if this one does not return an error

			logger.Info("start component")

			if err := comp.run(ctx); err != nil {
				logger.Error("failed to run component", zap.Error(err))

				return err
			}

			logger.Info("component stopped")

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to run components: %w", err)
	}

	return nil
}

func (s *Server) determineAPIAdvertiseAddress() (string, error) {
	if s.options.APIAdvertiseAddress != "" {
		s.logger.Info("using explicit API advertise address", zap.String("address", s.options.APIAdvertiseAddress))

		return s.options.APIAdvertiseAddress, nil
	}

	defaultSourceIP, err := gateway.DiscoverInterface()
	if err != nil {
		return "", fmt.Errorf("failed to discover default source IP: %w", err)
	}

	ip := defaultSourceIP.String()

	s.logger.Info("API advertise address is not explicitly defined, the IP on the default interface will be used as the API advertise address",
		zap.String("address", defaultSourceIP.String()))

	return ip, nil
}

// hasPatchedIPXEBinaries checks if the given directory contains pre-patched iPXE binaries.
// It checks for the presence of key patched binaries that would be created during the patching process.
func (s *Server) hasPatchedIPXEBinaries(tftpPath string) bool {
	// Check if directory exists
	info, err := os.Stat(tftpPath)
	if err != nil || !info.IsDir() {
		return false
	}

	// Check for key patched binaries
	requiredFiles := []string{
		"ipxe.efi",
		"snp.efi",
		"undionly.kpxe",
	}

	for _, file := range requiredFiles {
		filePath := filepath.Join(tftpPath, file)
		if _, err := os.Stat(filePath); err != nil {
			return false
		}
	}

	return true
}
