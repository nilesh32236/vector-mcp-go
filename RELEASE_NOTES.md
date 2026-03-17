# LanceDB Go SDK - Native Binaries Release

This release contains pre-built native libraries for the LanceDB Go SDK.

## Installation

```bash
go get github.com/lancedb/lancedb-go
```

No additional build steps required! The native libraries are included.

## Supported Platforms

- **macOS**: Intel (amd64) and Apple Silicon (arm64)
- **Linux**: Intel/AMD (amd64) and ARM (arm64)  
- **Windows**: Intel/AMD (amd64)

## Files Included

- `include/lancedb.h` - C header file
- `lib/darwin_amd64/` - macOS Intel binaries
- `lib/darwin_arm64/` - macOS Apple Silicon binaries  
- `lib/linux_amd64/` - Linux AMD64 binaries
- `lib/linux_arm64/` - Linux ARM64 binaries
- `lib/windows_amd64/` - Windows AMD64 binaries

## Usage

See the examples in the `examples/` directory for usage patterns.
