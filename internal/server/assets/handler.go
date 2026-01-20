// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package assets

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// Handler serves local Talos kernel and initramfs files.
type Handler struct {
	logger            *zap.Logger
	fileServer        http.Handler
	assetsPath        string
	talosVersion      string
	secureBootEnabled bool
}

// NewHandler creates a new assets handler and validates that required files exist.
func NewHandler(assetsPath, talosVersion string, secureBootEnabled bool, logger *zap.Logger) (*Handler, error) {
	// Validate that the assets path exists
	info, err := os.Stat(assetsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("assets path does not exist: %s", assetsPath)
		}

		return nil, fmt.Errorf("failed to stat assets path: %w", err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("assets path is not a directory: %s", assetsPath)
	}

	h := &Handler{
		assetsPath:        assetsPath,
		talosVersion:      talosVersion,
		secureBootEnabled: secureBootEnabled,
		logger:            logger,
		fileServer:        http.FileServer(http.Dir(assetsPath)),
	}

	// Validate that required files exist
	if err := h.validateAssets(); err != nil {
		return nil, err
	}

	return h, nil
}

// ServeHTTP serves kernel and initramfs files.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	// Log the request
	h.logger.Debug("serving asset", zap.String("path", r.URL.Path))

	// Validate the requested path to prevent directory traversal
	cleanPath := filepath.Clean(r.URL.Path)
	if strings.Contains(cleanPath, "..") {
		h.logger.Warn("invalid asset path requested", zap.String("path", r.URL.Path))
		http.Error(w, "Invalid path", http.StatusBadRequest)

		return
	}

	// Check if the file exists before serving
	fullPath := filepath.Join(h.assetsPath, cleanPath)
	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			h.logger.Warn("asset not found", zap.String("path", r.URL.Path))
			http.Error(w, fmt.Sprintf("Asset not found: %s", r.URL.Path), http.StatusNotFound)

			return
		}

		h.logger.Error("failed to stat asset", zap.String("path", r.URL.Path), zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)

		return
	}

	// Serve the file
	h.fileServer.ServeHTTP(w, r)
}

// validateAssets checks that required files exist for the configured version.
func (h *Handler) validateAssets() error {
	// Check for both amd64 and arm64 architectures
	architectures := []string{"amd64", "arm64"}
	requiredFiles := []string{"kernel", "initramfs.xz"}

	var missingFiles []string

	for _, arch := range architectures {
		archPath := filepath.Join(h.assetsPath, h.talosVersion, arch)

		// Check if architecture directory exists
		if _, err := os.Stat(archPath); err != nil {
			if os.IsNotExist(err) {
				h.logger.Warn("architecture directory not found", zap.String("path", archPath))
				missingFiles = append(missingFiles, fmt.Sprintf("%s/ (directory)", archPath))

				continue
			}

			return fmt.Errorf("failed to stat architecture directory %s: %w", archPath, err)
		}

		// Check for required files
		for _, file := range requiredFiles {
			filePath := filepath.Join(archPath, file)
			if _, err := os.Stat(filePath); err != nil {
				if os.IsNotExist(err) {
					h.logger.Warn("required asset file not found", zap.String("path", filePath))
					missingFiles = append(missingFiles, filePath)

					continue
				}

				return fmt.Errorf("failed to stat asset file %s: %w", filePath, err)
			}
		}

		// Check for optional secure boot file
		if h.secureBootEnabled {
			secureBootFile := filepath.Join(archPath, "vmlinuz-secureboot")
			if _, err := os.Stat(secureBootFile); err != nil {
				if os.IsNotExist(err) {
					h.logger.Warn("secure boot file not found (optional but required when booting with secure boot enabled)",
						zap.String("path", secureBootFile))
				}
			}
		}
	}

	// Log missing files as warnings but don't fail (allows partial deployment)
	if len(missingFiles) > 0 {
		h.logger.Warn("some asset files are missing - this may cause boot failures for affected architectures",
			zap.Strings("missing_files", missingFiles))
	} else {
		h.logger.Info("all required assets validated successfully",
			zap.String("version", h.talosVersion),
			zap.Bool("secure_boot", h.secureBootEnabled))
	}

	return nil
}
