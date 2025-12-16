# PawScript Cryptography, Compression & Image Processing Plan

## Overview

This document outlines proposed additions for cryptography, compression, and image processing capabilities in PawScript, following the language's philosophy of safe defaults with host extensibility.

## Assessment by Category

### Cryptography/Hashing

| Feature | Priority | Rationale |
|---------|----------|-----------|
| **Hashing (SHA256, MD5, etc.)** | High - Built-in | Ubiquitous: checksums, cache keys, integrity verification. Safe, no secrets involved. |
| **HMAC** | High - Built-in | API authentication is extremely common |
| **Symmetric encryption (AES)** | Medium - Built-in | Encrypting files/data. Relatively safe if key management is external |
| **Asymmetric (RSA/EC)** | Low - Host-provided | Complex key management, easy to misuse |
| **GPG/PGP** | Low - Host-provided | Heavy dependency, specialized use case |
| **TLS/Certificates** | Host-provided | Should be handled by HTTP layer, not scripts |

### Compression

| Feature | Priority | Rationale |
|---------|----------|-----------|
| **gzip/deflate** | High - Built-in | Web content, log files, extremely common |
| **zip archives** | High - Built-in | Universal archive format, file bundling |
| **tar archives** | Medium - Built-in | Unix standard, often paired with gzip |
| **zstd/lz4** | Low - Host-provided | Modern but less universal |
| **7z/rar** | Skip | Proprietary or complex |

### Image Processing

| Feature | Priority | Rationale |
|---------|----------|-----------|
| **Resize/thumbnail** | Medium - Built-in | Very common scripting need |
| **Format conversion** | Medium - Built-in | PNG↔JPEG↔GIF |
| **Crop/rotate** | Medium - Built-in | Basic manipulation |
| **Read metadata (EXIF)** | Low - Built-in | Useful for photo scripts |
| **Filters/effects** | Host-provided | Heavy, specialized |
| **OCR** | Host-provided | Heavy dependency (Tesseract) |
| **PDF generation** | Host-provided | Complex |

## Tiered Implementation

### Tier 1: Always Available (Built-in)

#### Hashing & HMAC

Zero risk, universally needed for checksums, cache keys, integrity verification:

```pawscript
# Basic hashing
hash: {sha256 "hello world"}
hash: {sha256 ~byte_data}
hash: {md5 ~data}
hash: {sha1 ~data}
hash: {sha512 ~data}

# Hash files directly
file_hash: {sha256_file "/path/to/file"}

# HMAC for API authentication
signature: {hmac_sha256 ~secret_key, ~message}
signature: {hmac_sha512 ~key, ~data}

# Constant-time comparison (prevents timing attacks)
if {hash_verify ~expected, ~actual} then (
    echo "Hash matches"
)

# Cryptographically secure random bytes
key: {crypto_random 32}      # 32 bytes = 256 bits
nonce: {crypto_random 12}    # 12 bytes for AES-GCM
```

#### Compression

Very common, integrates naturally with file operations:

```pawscript
# gzip compression
compressed: {gzip ~data}
original: {gunzip ~compressed}

# Compress/decompress files
gzip_file "/path/to/file.txt"           # Creates file.txt.gz
gunzip_file "/path/to/file.txt.gz"      # Restores file.txt

# zip archives
zip_create "archive.zip", ("file1.txt", "file2.txt", "subdir/")
zip_extract "archive.zip", "/destination/"

# List and read from archives without full extraction
files: {zip_list "archive.zip"}
for ~files, entry, (
    echo ~entry.name, ~entry.size, ~entry.compressed
)
content: {zip_read "archive.zip", "specific-file.txt"}

# Add to existing archive
zip_add "archive.zip", "newfile.txt"

# tar archives (often paired with gzip)
tar_create "archive.tar", ("file1.txt", "dir/")
tar_extract "archive.tar", "/destination/"
tar_list "archive.tar"

# tar.gz in one step
tar_create "archive.tar.gz", ~files, compress: "gzip"
```

### Tier 2: Opt-in Built-in

#### Symmetric Encryption

Useful but requires care. Uses authenticated encryption (AES-GCM) to prevent common mistakes:

```pawscript
# Generate a secure key
key: {crypto_random 32}  # 256 bits for AES-256

# Encrypt data (AES-256-GCM - authenticated encryption)
encrypted: {encrypt ~plaintext, ~key}
# Returns: nonce + ciphertext + auth tag (all needed for decryption)

# Decrypt data
decrypted: {decrypt ~encrypted, ~key}

# Encrypt with associated data (AEAD)
encrypted: {encrypt ~data, ~key, aad: ~metadata}
decrypted: {decrypt ~encrypted, ~key, aad: ~metadata}

# Key derivation from password (for password-based encryption)
salt: {crypto_random 16}
key: {pbkdf2 ~password, ~salt, iterations: 100000, length: 32}

# Encrypt/decrypt files
encrypt_file "/path/to/secret.txt", ~key      # Creates secret.txt.enc
decrypt_file "/path/to/secret.txt.enc", ~key  # Restores secret.txt
```

#### Basic Image Operations

Common enough for scripting (thumbnails, format conversion):

```pawscript
# Load image
img: {image_open "photo.jpg"}

# Get image info
info: {image_info "photo.jpg"}
echo "Size:", ~info.width, "x", ~info.height
echo "Format:", ~info.format
echo "Color mode:", ~info.mode

# Resize (maintains aspect ratio by default)
thumb: {image_resize ~img, width: 200}
thumb: {image_resize ~img, height: 150}
thumb: {image_resize ~img, width: 200, height: 150}  # Exact size
thumb: {image_resize ~img, scale: 0.5}               # 50% size

# Save with format/quality options
image_save ~thumb, "thumbnail.jpg", quality: 85
image_save ~img, "output.png"  # Format from extension

# Direct format conversion
image_convert "input.png", "output.jpg", quality: 90
image_convert "photo.jpg", "photo.webp"

# Crop
cropped: {image_crop ~img, x: 100, y: 50, width: 400, height: 300}

# Rotate
rotated: {image_rotate ~img, 90}   # 90, 180, 270 degrees
rotated: {image_rotate ~img, -45}  # Arbitrary angle

# Flip
flipped: {image_flip ~img, "horizontal"}  # or "vertical"

# Basic adjustments (opt-in, may require flag)
adjusted: {image_adjust ~img, brightness: 1.2, contrast: 1.1}
```

### Tier 3: Host-provided Only

These require explicit host registration due to complexity, security concerns, or heavy dependencies:

#### GPG/PGP Operations

```go
// Host provides GPG capability
ps.RegisterGPGKeyring("/path/to/keyring")
```

```pawscript
# Only available if host enabled it
signed: {gpg_sign ~data, key: "user@example.com"}
verified: {gpg_verify ~signed_data}
encrypted: {gpg_encrypt ~data, recipients: ("alice@example.com", "bob@example.com")}
decrypted: {gpg_decrypt ~encrypted}
```

#### RSA/EC Operations

```go
// Host provides asymmetric crypto
ps.RegisterKeyPair("signing", privateKey, publicKey)
```

```pawscript
signature: {rsa_sign "signing", ~data}
valid: {rsa_verify "signing", ~data, ~signature}
```

#### Advanced Image Processing

```go
// Host provides advanced image features
ps.EnableAdvancedImageProcessing()
```

```pawscript
# Filters and effects
filtered: {image_blur ~img, radius: 5}
filtered: {image_sharpen ~img}
filtered: {image_grayscale ~img}

# Text overlay
with_text: {image_text ~img, "Watermark", x: 10, y: 10, font: "Arial", size: 24}

# Composite images
combined: {image_composite ~background, ~overlay, x: 100, y: 50}
```

## Proposed Command Sets

### Hashing Commands (Always Available)

| Command | Description |
|---------|-------------|
| `md5 data` | MD5 hash (hex string) |
| `sha1 data` | SHA-1 hash |
| `sha256 data` | SHA-256 hash |
| `sha512 data` | SHA-512 hash |
| `sha256_file path` | Hash file contents |
| `sha512_file path` | Hash file contents |
| `hmac_sha256 key, data` | HMAC-SHA256 |
| `hmac_sha512 key, data` | HMAC-SHA512 |
| `hash_verify expected, actual` | Constant-time comparison |
| `crypto_random bytes` | Secure random bytes |

### Compression Commands (Always Available)

| Command | Description |
|---------|-------------|
| `gzip data` | Compress bytes |
| `gunzip data` | Decompress bytes |
| `gzip_file path` | Compress file (creates .gz) |
| `gunzip_file path` | Decompress .gz file |
| `zip_create path, files` | Create zip archive |
| `zip_extract path, dest` | Extract zip archive |
| `zip_list path` | List archive contents |
| `zip_read path, entry` | Read single entry |
| `zip_add path, file` | Add to existing archive |
| `tar_create path, files, ...` | Create tar archive |
| `tar_extract path, dest` | Extract tar archive |
| `tar_list path` | List tar contents |

### Encryption Commands (Opt-in)

| Command | Description |
|---------|-------------|
| `encrypt data, key, ...` | AES-256-GCM encrypt |
| `decrypt data, key, ...` | AES-256-GCM decrypt |
| `encrypt_file path, key` | Encrypt file |
| `decrypt_file path, key` | Decrypt file |
| `pbkdf2 password, salt, ...` | Key derivation |

### Image Commands (Opt-in)

| Command | Description |
|---------|-------------|
| `image_open path` | Load image |
| `image_save img, path, ...` | Save image |
| `image_info path` | Get dimensions/format |
| `image_resize img, ...` | Resize/scale |
| `image_crop img, ...` | Crop region |
| `image_rotate img, degrees` | Rotate |
| `image_flip img, direction` | Flip horizontal/vertical |
| `image_convert src, dst, ...` | Format conversion |
| `image_adjust img, ...` | Brightness/contrast |

## Security Considerations

### Hashing
- MD5 and SHA1 are available but should not be used for security purposes (only checksums)
- Recommend SHA256+ for any security-sensitive use

### Encryption
- Only authenticated encryption (AES-GCM) to prevent padding oracle attacks
- Key management is the user's responsibility
- PBKDF2 with high iteration count for password-derived keys
- No ECB mode or other insecure configurations exposed

### Compression
- Zip bombs: Consider file count and decompressed size limits
- Path traversal: Sanitize entry paths during extraction

## CLI Flags

```
--allow-crypto      Enable encryption/decryption commands
--allow-images      Enable image processing commands
--unrestricted      Enable all features
```

## Host-Side Configuration (Go)

```go
ps.SetCryptoConfig(&pawscript.CryptoConfig{
    AllowEncryption:    true,
    AllowKeyDerivation: true,
    MaxKeySize:         512,  // bits
    MinPBKDF2Iterations: 100000,
})

ps.SetImageConfig(&pawscript.ImageConfig{
    AllowProcessing:   true,
    MaxImageSize:      100 * 1024 * 1024,  // 100MB
    AllowedFormats:    []string{"jpeg", "png", "gif", "webp"},
    MaxOutputSize:     50 * 1024 * 1024,   // 50MB
})

ps.SetCompressionConfig(&pawscript.CompressionConfig{
    MaxArchiveSize:     1024 * 1024 * 1024,  // 1GB
    MaxFileCount:       10000,
    MaxCompressionRatio: 100,  // Zip bomb protection
})
```

## Go Implementation Notes

All features can be implemented with pure Go (no CGO):

| Feature | Go Package |
|---------|-----------|
| Hashing | `crypto/sha256`, `crypto/sha512`, `crypto/md5`, `crypto/sha1` |
| HMAC | `crypto/hmac` |
| AES-GCM | `crypto/aes`, `crypto/cipher` |
| PBKDF2 | `golang.org/x/crypto/pbkdf2` |
| Random | `crypto/rand` |
| gzip | `compress/gzip` |
| zip | `archive/zip` |
| tar | `archive/tar` |
| Images | `image`, `image/jpeg`, `image/png`, `image/gif` |
| Image resize | `golang.org/x/image/draw` or `github.com/disintegration/imaging` |

## Example: Secure File Backup Script

```pawscript
# Generate or load encryption key
key_file: "~/.backup-key"
if {exists ~key_file} then (
    key: {read_file ~key_file, binary: true}
) else (
    key: {crypto_random 32}
    write_file ~key_file, ~key, binary: true
    echo "Generated new backup key - store safely!"
)

# Create compressed archive of important files
files: ("documents/", "photos/", "config/")
tar_create "/tmp/backup.tar.gz", ~files, compress: "gzip"

# Encrypt the archive
encrypt_file "/tmp/backup.tar.gz", ~key
# Creates /tmp/backup.tar.gz.enc

# Generate checksum for integrity verification
hash: {sha256_file "/tmp/backup.tar.gz.enc"}
echo "Backup hash:", ~hash

# Clean up unencrypted temp file
remove "/tmp/backup.tar.gz"
```

## Example: Thumbnail Generator

```pawscript
# Process all images in a directory
source_dir: "photos/"
thumb_dir: "thumbnails/"

mkdir ~thumb_dir

files: {list_dir ~source_dir}
for ~files, file, (
    if {match ~file, (\.(jpg|jpeg|png|gif)$), case_insensitive: true} then (
        source: "~source_dir~file"
        dest: "~thumb_dir~file"

        echo "Processing:", ~file
        img: {image_open ~source}
        thumb: {image_resize ~img, width: 200}
        image_save ~thumb, ~dest, quality: 80

        info: {image_info ~dest}
        echo "  Created:", ~info.width, "x", ~info.height
    )
)
```

## Summary

| Category | Tier | Risk | Dependencies |
|----------|------|------|--------------|
| Hashing | 1 (always) | None | stdlib |
| Compression | 1 (always) | Low | stdlib |
| Symmetric Crypto | 2 (opt-in) | Medium | stdlib |
| Basic Images | 2 (opt-in) | None | light |
| GPG/Asymmetric | 3 (host) | High | heavy |
| Advanced Images | 3 (host) | None | heavy |

This design provides practical cryptography and file handling for scripting while maintaining PawScript's security-first philosophy.
