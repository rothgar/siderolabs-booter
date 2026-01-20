// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package constants provides constants for the server package.
package constants

const (
	// DefaultIPXEPath is the default path to the iPXE binaries.
	DefaultIPXEPath = "/var/lib/ipxe"

	// DefaultTFTPPath is the default path from which the TFTP server serves files.
	DefaultTFTPPath = "/var/lib/tftp"

	// IPXEURLPath is the path from which the HTTP server serves the iPXE scripts.
	IPXEURLPath = "ipxe"
)
