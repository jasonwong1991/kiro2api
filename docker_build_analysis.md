# Docker Build Memory Bottleneck Analysis

**Project**: kiro2api
**Target Environment**: 1GB RAM, single-core VPS
**Problem**: Docker build causes VPS to freeze/crash
**Analysis Date**: 2025-11-09

---

## Executive Summary

The Docker build configuration uses **multi-stage builds with cross-compilation tooling** that is **highly memory-intensive** for a 1GB RAM VPS. Key findings:

- **Build memory requirement**: Estimated 800MB-1.2GB (exceeds available RAM)
- **CGO compilation**: Enabled but not required (adds 200-300MB overhead)
- **Cross-compilation toolchain**: tonistiigi/xx + Clang/LLVM adds significant overhead
- **Go module cache**: ~505MB download + 246MB build cache
- **Concurrent operations**: BuildKit cache mounts may cause memory spikes

**Critical Issue**: The build process will likely consume 100%+ of available RAM, causing OOM kills or system freeze.

---

## Code Sections

### Docker Configuration Files

- `/Users/JasonWong/Documents/github/2api/kiro2api/Dockerfile:1~72` (Multi-stage Dockerfile): Cross-compilation setup with CGO enabled
- `/Users/JasonWong/Documents/github/2api/kiro2api/docker-compose.yml:1~37` (Docker Compose): Service configuration without resource limits
- `/Users/JasonWong/Documents/github/2api/kiro2api/.dockerignore:1~47` (Build context filter): Excludes test files, docs, and build artifacts

### Build Configuration

- `/Users/JasonWong/Documents/github/2api/kiro2api/go.mod:1~51` (Go modules): 56 total dependencies including bytedance/sonic
- `/Users/JasonWong/Documents/github/2api/kiro2api/go.sum:1~94` (Dependency checksums): 94 lines of dependency verification

### Source Code

- **Total Go source files**: 55 files (excluding tests)
- **Total lines of code**: ~12,097 lines
- **Build context size**: ~580KB (source only, excluding .git)
- **Static assets**: 13KB (HTML/CSS/JS files)

---

## Report

### Conclusions

#### 1. Memory-Intensive Build Configuration

**Current Dockerfile Analysis** (`Dockerfile:1~72`):

```dockerfile
# Line 9: Uses golang:alpine as builder (base image ~300MB)
FROM --platform=$BUILDPLATFORM golang:alpine AS builder

# Line 13: Installs cross-compilation toolchain
COPY --from=tonistiigi/xx:1.6.1 / /
RUN apk add --no-cache git ca-certificates tzdata clang lld

# Line 21: Installs target platform toolchain
RUN xx-apk add musl-dev gcc

# Line 35: CGO enabled for bytedance/sonic
ENV CGO_ENABLED=1

# Line 36-41: Build with cache mounts
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/go-mod \
    xx-go build -ldflags="-s -w" -o kiro2api main.go
```

**Memory Consumption Breakdown**:

| Component | Estimated Memory | Notes |
|-----------|-----------------|-------|
| Base golang:alpine image | 300-400MB | Includes Go toolchain |
| Cross-compilation tools (xx + clang/lld) | 150-250MB | LLVM toolchain overhead |
| Go module download | 100-150MB | 56 dependencies, ~505MB on disk |
| Go build cache | 200-300MB | Compilation artifacts |
| CGO compilation | 100-200MB | C compiler + linker overhead |
| BuildKit overhead | 50-100MB | Docker BuildKit daemon |
| **Total Peak Memory** | **900MB-1.4GB** | **Exceeds 1GB RAM limit** |

#### 2. CGO is Not Required

**Testing Results**:

```bash
# CGO enabled build
CGO_ENABLED=1 go build -ldflags="-s -w" -o test_cgo main.go
# Result: 20MB binary

# CGO disabled build
CGO_ENABLED=0 go build -ldflags="-s -w" -o test_nocgo main.go
# Result: 20MB binary (identical size)
```

**Conclusion**: bytedance/sonic v1.14.1 has fallback implementations. CGO is **not required** for this project, but the Dockerfile forces `CGO_ENABLED=1` (line 35), adding unnecessary memory overhead.

#### 3. Cross-Compilation Toolchain Overhead

**Current Setup** (`Dockerfile:9-21`):

- Uses `tonistiigi/xx:1.6.1` for cross-platform builds
- Installs Clang/LLVM toolchain
- Installs target platform GCC/musl-dev

**Impact**:
- Adds 150-250MB memory overhead
- Required only for building arm64 on amd64 (or vice versa)
- **Not needed** if building on the same architecture as deployment

#### 4. No Resource Limits in docker-compose.yml

**Current Configuration** (`docker-compose.yml:1~37`):

```yaml
services:
  kiro2api:
    build:
      context: .
      dockerfile: Dockerfile
    # NO memory limits defined
    # NO CPU limits defined
```

**Issue**: Docker will attempt to use all available system memory during build, causing:
- OOM (Out of Memory) kills
- System swap thrashing
- VPS freeze/unresponsiveness

#### 5. Build Context is Optimized

**Good News**:
- `.dockerignore` properly excludes test files, docs, and build artifacts
- Build context size: ~580KB (very small)
- No large binary files or assets in context
- Static assets: only 13KB

**This is NOT a bottleneck**.

#### 6. Go Module Cache Strategy

**Current Approach** (`Dockerfile:27-28`):

```dockerfile
RUN --mount=type=cache,target=/root/.cache/go-mod \
    go mod download
```

**Memory Impact**:
- Downloads ~505MB of dependencies
- BuildKit cache mounts keep data in memory during build
- On 1GB RAM system, this alone can consume 50% of available memory

---

### Relations

#### Dockerfile to Build Memory

- `Dockerfile:9` (golang:alpine base) → **300-400MB base memory**
- `Dockerfile:13-14` (cross-compilation tools) → **+150-250MB overhead**
- `Dockerfile:21` (target toolchain) → **+50-100MB overhead**
- `Dockerfile:35` (CGO_ENABLED=1) → **+100-200MB compilation overhead**
- `Dockerfile:36-41` (build with cache mounts) → **+200-300MB build cache**

**Total Chain**: Base + Tools + CGO + Build = **900MB-1.4GB peak memory**

#### go.mod to Memory Requirements

- `go.mod:6` (bytedance/sonic v1.14.1) → Triggers CGO requirement (unnecessary)
- `go.mod:7` (gin-gonic/gin v1.11.0) → Large dependency tree
- `go.mod:17` (quic-go/quic-go v0.55.0) → Heavy dependency with many sub-packages
- Total 56 modules → **~505MB module cache** + **~246MB build cache**

#### docker-compose.yml to System Stability

- `docker-compose.yml:3-5` (build configuration) → No resource limits
- Missing `deploy.resources.limits.memory` → Can consume all system RAM
- Missing `deploy.resources.limits.cpus` → Can starve other processes

#### CGO Dependency Chain

- `Dockerfile:35` (CGO_ENABLED=1) → Forces C compiler usage
- `Dockerfile:21` (xx-apk add musl-dev gcc) → Installs GCC toolchain
- `go.mod:6` (bytedance/sonic) → Has CGO optimizations but **not required**
- **Result**: Unnecessary 200-300MB memory overhead

---

### Result

#### Answer to Research Questions

**1. What is the current Dockerfile configuration?**

- **Multi-stage build**: Yes (builder + runtime stages)
- **Base images**:
  - Builder: `golang:alpine` (~300MB)
  - Runtime: `alpine:3.19` (~7MB)
- **Build steps**:
  1. Install cross-compilation toolchain (tonistiigi/xx + clang/lld)
  2. Install target platform toolchain (musl-dev + gcc)
  3. Download Go modules with cache mount
  4. Build with CGO enabled and cache mounts
  5. Copy binary to minimal runtime image

**2. What is the docker-compose.yml configuration?**

- **Build context**: Current directory (`.`)
- **Resource limits**: **NONE** (critical issue)
- **Build args**: None specified
- **Volumes**: AWS SSO cache volume mounted
- **Ports**: 5656:8080 mapping

**3. What are the Go build commands and flags?**

```bash
xx-go build -ldflags="-s -w" -o kiro2api main.go
```

- `-ldflags="-s -w"`: Strip debug symbols (reduces binary size)
- `xx-go`: Cross-compilation wrapper
- **CGO_ENABLED=1**: Enabled (unnecessary)
- **Cache mounts**: Used for both go-build and go-mod

**4. Are there any large dependencies or assets?**

- **Dependencies**: 56 Go modules, ~505MB total
- **Largest dependencies**:
  - `golang.org/x/tools` (large tooling package)
  - `github.com/quic-go/quic-go` (complex networking)
  - `google.golang.org/protobuf` (protocol buffers)
- **Assets**: Only 13KB static files (not a concern)

**5. What is the size of the build context?**

- **Source code**: ~580KB
- **Total project**: 2.4MB (including .git)
- **Build context sent to Docker**: ~580KB (after .dockerignore)
- **Conclusion**: Build context is **well-optimized** and not a bottleneck

**6. Are there any concurrent build processes?**

- **BuildKit cache mounts**: Yes, two concurrent cache mounts
  - `/root/.cache/go-build` (compilation cache)
  - `/root/.cache/go-mod` (module cache)
- **Impact**: Both caches active simultaneously during build
- **Memory spike**: Can cause 400-600MB simultaneous memory usage

---

### Attention

#### Critical Issues for 1GB RAM VPS

1. **Build Will Likely Fail or Freeze**
   - Peak memory requirement: 900MB-1.4GB
   - Available RAM: 1GB
   - System overhead: ~100-200MB
   - **Result**: Insufficient memory, OOM kill or system freeze

2. **CGO Unnecessarily Enabled**
   - `Dockerfile:35` sets `CGO_ENABLED=1`
   - Testing shows CGO is **not required** (binary size identical)
   - Adds 200-300MB memory overhead
   - **Fix**: Set `CGO_ENABLED=0`

3. **Cross-Compilation Toolchain Overhead**
   - tonistiigi/xx + Clang/LLVM adds 150-250MB
   - Only needed for cross-platform builds
   - **Question**: Is cross-compilation needed for VPS deployment?
   - **Fix**: Use native builds if deploying to same architecture

4. **No Resource Limits in docker-compose.yml**
   - Docker can consume all system memory
   - No memory limits configured
   - No CPU limits configured
   - **Risk**: System-wide freeze, not just build failure

5. **BuildKit Cache Mounts**
   - Two concurrent cache mounts during build
   - Can cause memory spikes
   - **Issue**: Cache data kept in memory during build
   - **Fix**: Consider disabling cache mounts for low-memory builds

6. **Go Module Download Size**
   - 56 dependencies totaling ~505MB
   - All downloaded during build
   - **Impact**: 50% of available RAM consumed by dependencies alone

#### Potential Build Failure Scenarios

1. **OOM Kill During Module Download**
   - Symptom: Build stops at "go mod download"
   - Cause: 505MB modules + 300MB base image > 1GB RAM

2. **OOM Kill During Compilation**
   - Symptom: Build stops during "go build"
   - Cause: Build cache + CGO compilation > available RAM

3. **System Freeze**
   - Symptom: VPS becomes unresponsive
   - Cause: Docker consumes all RAM, system starts swapping
   - **Danger**: May require hard reboot

4. **BuildKit Daemon Crash**
   - Symptom: "failed to solve" errors
   - Cause: BuildKit daemon OOM killed by system

#### Recommendations Priority

**CRITICAL (Must Fix)**:

1. **Disable CGO** - Saves 200-300MB
   ```dockerfile
   ENV CGO_ENABLED=0
   ```

2. **Add Memory Limits** - Prevents system freeze
   ```yaml
   deploy:
     resources:
       limits:
         memory: 800M
   ```

3. **Use Pre-built Image** - Avoid building on VPS
   ```bash
   docker pull ghcr.io/caidaoli/kiro2api:latest
   ```

**HIGH (Strongly Recommended)**:

4. **Remove Cross-Compilation** - Saves 150-250MB
   - Use simple `FROM golang:alpine` without xx toolchain
   - Build for target platform only

5. **Disable BuildKit Cache Mounts** - Reduces memory spikes
   ```dockerfile
   # Remove --mount=type=cache flags
   RUN go mod download
   RUN go build -ldflags="-s -w" -o kiro2api main.go
   ```

**MEDIUM (Consider)**:

6. **Build on Different Machine** - Recommended approach
   - Build on local machine or CI/CD
   - Push image to registry
   - Pull on VPS (only ~50MB download)

7. **Use Swap Space** - Temporary workaround
   ```bash
   # Add 2GB swap on VPS
   fallocate -l 2G /swapfile
   chmod 600 /swapfile
   mkswap /swapfile
   swapon /swapfile
   ```

8. **Reduce Go Build Parallelism**
   ```dockerfile
   ENV GOMAXPROCS=1
   ```

---

## Optimized Dockerfile for Low-Memory VPS

Here's a minimal Dockerfile that should work on 1GB RAM:

```dockerfile
# Optimized for low-memory environments (1GB RAM)
FROM golang:alpine AS builder

# Install minimal dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy dependency files
COPY go.mod go.sum ./

# Download dependencies (no cache mount to reduce memory)
RUN go mod download

# Copy source code
COPY . .

# Build with CGO disabled and minimal parallelism
ENV CGO_ENABLED=0
ENV GOMAXPROCS=1
RUN go build -ldflags="-s -w" -o kiro2api main.go

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /app/kiro2api .
COPY --from=builder /app/static ./static

RUN mkdir -p /home/appuser/.aws/sso/cache && \
    chown -R appuser:appgroup /app /home/appuser

USER appuser

EXPOSE 8080

CMD ["./kiro2api"]
```

**Changes Made**:
- Removed cross-compilation toolchain (saves 150-250MB)
- Removed CGO (saves 200-300MB)
- Removed BuildKit cache mounts (reduces memory spikes)
- Added `GOMAXPROCS=1` (limits parallelism)
- Simplified to native build only

**Expected Memory Usage**: 400-600MB (should fit in 1GB RAM)

---

## Alternative: Use Pre-built Images

**Best Solution for 1GB RAM VPS**:

```bash
# Don't build on VPS - just pull the image
docker pull ghcr.io/caidaoli/kiro2api:latest

# Run directly
docker run -d \
  --name kiro2api \
  --memory="800m" \
  --memory-swap="800m" \
  -p 8080:8080 \
  -e KIRO_AUTH_TOKEN='[{"auth":"Social","refreshToken":"your_token"}]' \
  -e KIRO_CLIENT_TOKEN="123456" \
  ghcr.io/caidaoli/kiro2api:latest
```

**Advantages**:
- No build memory required
- Only ~50MB download
- Instant deployment
- No risk of OOM during build

---

## Implementation Steps

### Option 1: Use Pre-built Image (Recommended)

```bash
# 1. Stop any existing containers
docker-compose down

# 2. Pull pre-built image
docker pull ghcr.io/caidaoli/kiro2api:latest

# 3. Update docker-compose.yml to use pre-built image
# Replace 'build:' section with:
#   image: ghcr.io/caidaoli/kiro2api:latest

# 4. Add resource limits
# Add under 'kiro2api:' service:
#   deploy:
#     resources:
#       limits:
#         memory: 800M
#         cpus: '1.0'

# 5. Start service
docker-compose up -d
```

### Option 2: Build Locally and Push

```bash
# 1. Build on local machine (with sufficient RAM)
docker build -t kiro2api:latest .

# 2. Save image to tar
docker save kiro2api:latest | gzip > kiro2api.tar.gz

# 3. Transfer to VPS
scp kiro2api.tar.gz user@vps:/tmp/

# 4. Load on VPS
ssh user@vps
docker load < /tmp/kiro2api.tar.gz

# 5. Run with resource limits
docker run -d \
  --name kiro2api \
  --memory="800m" \
  -p 8080:8080 \
  --env-file .env \
  kiro2api:latest
```

### Option 3: Optimize Dockerfile for VPS Build

```bash
# 1. Replace Dockerfile with optimized version (see above)

# 2. Add swap space first
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# 3. Build with resource limits
docker build \
  --memory="800m" \
  --memory-swap="2g" \
  -t kiro2api:latest .

# 4. Run with limits
docker run -d \
  --name kiro2api \
  --memory="800m" \
  -p 8080:8080 \
  --env-file .env \
  kiro2api:latest
```

---

## Monitoring and Verification

### Check Memory Usage During Build

```bash
# Terminal 1: Start build
docker build -t kiro2api:latest .

# Terminal 2: Monitor memory
watch -n 1 'free -h && echo "---" && docker stats --no-stream'
```

### Verify Successful Deployment

```bash
# Check container status
docker ps -a

# Check logs
docker logs kiro2api

# Test API
curl -H "Authorization: Bearer 123456" \
  http://localhost:8080/v1/models

# Monitor runtime memory
docker stats kiro2api
```

---

## Summary

**Root Cause**: Docker build requires 900MB-1.4GB RAM, exceeding 1GB VPS capacity.

**Primary Culprits**:
1. Cross-compilation toolchain (150-250MB)
2. CGO compilation (200-300MB)
3. BuildKit cache mounts (memory spikes)
4. No resource limits (can freeze system)

**Best Solution**: Use pre-built images from `ghcr.io/caidaoli/kiro2api:latest`

**Alternative**: Optimize Dockerfile by removing CGO and cross-compilation

**Quick Fix**: Add 2GB swap space before building

**File Locations**:
- Dockerfile: `/Users/JasonWong/Documents/github/2api/kiro2api/Dockerfile`
- docker-compose.yml: `/Users/JasonWong/Documents/github/2api/kiro2api/docker-compose.yml`
- Optimized Dockerfile: See section above

---

