# booter

A tool to easily boot Talos machines using PXE.

## Usage

Run `booter` in a container on the host network:

```bash
docker run --rm --network host \
  ghcr.io/siderolabs/booter:v0.3.0
```

Then, power on machines in the **same subnet** as `booter` with **UEFI PXE boot** enabled.
Recommended boot order: **disk first, then network**.

To connect the machines to **Omni**, go to the **Omni Overview** page, click **“Copy Kernel Parameters”**, and run `booter` with the copied arguments:

```bash
docker run --rm --network host \
  ghcr.io/siderolabs/booter:v0.3.0 \
  <KERNEL_ARGS>
```

To see more options:

```bash
docker run --rm --network host ghcr.io/siderolabs/booter:v0.3.0 --help
```

## Using Local Assets

By default, `booter` fetches Talos kernel and initramfs files from the Image Factory on each boot.
For faster boot times or air-gapped environments, you can download these assets locally and serve them from disk.

### Downloading Assets from Image Factory

Download the kernel and initramfs files for your desired Talos version and schematic:

```bash
# Set your configuration
SCHEMATIC_ID="376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"
TALOS_VERSION="v1.12.1"
ASSETS_DIR="./talos-assets"
```

Create directory structure

```bash
mkdir -p "$ASSETS_DIR/$TALOS_VERSION/amd64"
```

Download assets for amd64

```bash
curl -L -o "$ASSETS_DIR/$TALOS_VERSION/amd64/kernel" \
  "https://factory.talos.dev/image/$SCHEMATIC_ID/$TALOS_VERSION/kernel-amd64"

curl -L -o "$ASSETS_DIR/$TALOS_VERSION/amd64/initramfs.xz" \
  "https://factory.talos.dev/image/$SCHEMATIC_ID/$TALOS_VERSION/initramfs.xz"
```

**For Secure Boot**, also download the signed kernel:

```bash
curl -L -o "$ASSETS_DIR/$TALOS_VERSION/amd64/vmlinuz-secureboot" \
  "https://factory.talos.dev/image/$SCHEMATIC_ID/$TALOS_VERSION/kernel-amd64-secureboot"
```


**Directory structure:**

```text
assets/
└── v1.12.1/
    └── amd64/
       ├── kernel
       ├── initramfs.xz
       └── vmlinuz-secureboot (optional)
```

### Running with Local Assets

#### Using Docker

Run `booter` with the local assets path mounted:

```bash
docker run --network host \
  -v "$PWD/$ASSETS_DIR:/assets:ro" \
  ghcr.io/siderolabs/booter:v0.3.0 \
    --local-assets-path=/assets \
    --talos-version=v1.12.1
```

When using `--local-assets-path`, booter automatically detects pre-patched binaries in `{assets-path}/tftp/` and skips patching.

## Run outside Docker (Development)

This is only for advanced use cases such as development or air-gapped environments where docker is not available.

### Generate Pre-Patched iPXE Binaries

For local execution use the Docker container to generate pre-patched iPXE binaries once:

```bash
mkdir -p ./$ASSETS_DIR/tftp
```

Run the container to generate patched binaries
The container will patch the binaries and write them to the tftp directory

```bash
docker run --rm --net=host \
  -v "$PWD/$ASSETS_DIR/tftp:/var/lib/tftp" \
  ghcr.io/siderolabs/booter:v0.3.0
```

**Note:** The patched binaries contain embedded scripts with your API endpoint. If your IP address changes, you'll need to regenerate them.

### Build `booter` binaries

```bash
make booter
```

Run binaries from `_out/` directory. Requires `sudo` to bind to port :69

```bash
sudo -E ./_out/booter-linux-amd64 \
  --local-assets-path=./assets \
  --talos-version=v1.12.1
```

**Directory structure with pre-patched binaries:**

```text
assets/
├── v1.12.1/
│   └── amd64/
│       ├── kernel
│       ├── initramfs.xz
│       └── vmlinuz-secureboot (optional)
└── tftp/
    ├── ipxe.efi
    ├── snp.efi
    ├── ipxe-arm64.efi
    ├── snp-arm64.efi
    ├── undionly.kpxe
    └── undionly.kpxe.0
```
