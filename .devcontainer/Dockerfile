FROM mcr.microsoft.com/devcontainers/go:1

RUN apt-get update && apt-get install -y --no-install-recommends \
    sqlite3 \
    podman \
    fuse-overlayfs \
    uidmap \
    curl \
    gnupg \
    libnspr4 libnss3 libatk1.0-0t64 libatk-bridge2.0-0t64 \
    libdbus-1-3 libcups2t64 libxcb1 libxkbcommon0 libasound2t64 \
    libgbm1 libx11-6 libxext6 libcairo2 libpango-1.0-0 \
    libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libatspi2.0-0t64 \
    fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*

# Install gVisor via official apt repo (GPG-verified)
RUN curl -fsSL https://gvisor.dev/archive.key \
        | gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] \
        https://storage.googleapis.com/gvisor/releases release main" \
        > /etc/apt/sources.list.d/gvisor.list \
    && apt-get update \
    && apt-get install -y runsc \
    && rm -rf /var/lib/apt/lists/*

# Register runsc with Podman; systrap runs in userspace, no KVM needed
RUN printf '[engine.runtimes]\nrunsc = ["/usr/local/bin/runsc", "--platform=systrap"]\n' \
    >> /etc/containers/containers.conf
