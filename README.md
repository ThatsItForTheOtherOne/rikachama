# Rikachama
This is an imageboard I wrote in golang as a learning exercise. 
It uses podman/gVisor to securely handle files.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-create-admin <username>` | string | `""` | Create an admin account with the given username (prompts for password on stdin), then exit. |
| `-dev` | bool | `false` | Enable developer mode (disables `Secure` cookies for local HTTP). Never enable in production. |
| `-config <path>` | string | `config.json` | Path to the config file. Useful when running multiple boards from one binary. |
| `-port <n>` | int | `3200` | TCP port to bind. Must be 1-65535. Pair with `-config` to run multiple boards on one host. |

## Browser compatibility

| Browser | Released | Renders | Posts | Notes |
|---------|----------|---------|-------|-------|
| IE3 | Aug 1996 | partial | ❌ | Predates RFC 1867 multipart/form-data support |
| HotJava 3.0 | 2004 | partial | ❌ | Sends multipart without boundary param; doesn't follow redirects |
| Netscape Navigator 4.x | 1997-2002 | ⚠️ | ✅ | Text wraps incorrectly. No clean CSS workaround. Known bug w/ NN4.0. |
| IE5.01 | Nov 1999 | ✅ | ✅ | Lowest version verified to work correctly |
| IE5.5 | June 2000 | ✅ | ✅ | |
| Netscape Navigator 6.2 | 2001 | ✅ | ✅ | |
| Opera 5 | Dec 2000 | ✅ | ✅ | |
| IE6 | Aug 2001 | ✅ | ✅ | |
| K-Meleon 0.7 | Nov 2002 | ✅ | ✅ | |

Modern browsers also work of course, but that's less interesting.

## Installation

This is a recommended setup. Other setups can and probably will work, but this is what I did during testing. This was done on Rocky Linux 10.

First, install podman and gVisor. gVisor's install instructions can be found [here](https://gvisor.dev/docs/user_guide/install/).

Create a dedicated system user
```bash
sudo useradd --system --no-create-home --home-dir /var/lib/rikachama rikachama
sudo install -d -o rikachama -g rikachama /var/lib/rikachama
sudo loginctl enable-linger rikachama
sudo usermod --add-subuids 100000-165535 --add-subgids 100000-165535 rikachama
```
You might need to run ``sudo -u rikachama podman system migrate`` at this point. During testing I accidentally forgot usermod and had to run it.

Make sure the user can invoke gVisor
```bash
sudo -u rikachama /usr/local/bin/runsc --version  # smoke test
```
Register gVisor with podman so podman can invoke it as a container runtime
```bash
sudo tee /etc/containers/containers.conf <<'EOF'
[engine.runtimes]
runsc = ["/usr/local/bin/runsc"]
EOF
```
Compile the binary (with go build) and install it to a good location 
```bash
sudo install -d /opt/rikachama
sudo install -o root -g root -m 755 PATH_TO_RIKACHAMA_BINARY /opt/rikachama/rikachama
```

I used a systemd template unit for service management

```ini
# /etc/systemd/system/rikachama@.service
[Unit]
Description=rikachama imageboard (%i)
After=network.target

[Service]
Type=simple
User=rikachama
Group=rikachama
WorkingDirectory=/opt/rikachama/%i
ExecStart=/opt/rikachama/rikachama -config config.json -port ${PORT}
EnvironmentFile=/opt/rikachama/%i/.env
Restart=on-failure
RestartSec=2

ProtectSystem=strict
ReadWritePaths=/opt/rikachama/%i /var/lib/rikachama /tmp /var/tmp
LockPersonality=true
RestrictRealtime=true
RestrictSUIDSGID=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectClock=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK
UMask=0077
ProtectProc=invisible
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
```

It requires a per instance .env file in the instance directory with PORT= and a config file. Use config.json.example to write your board-level config.

Here's an example for creating a random board, with an admin user "admin".
```bash
sudo install -d -o rikachama -g rikachama /opt/rikachama/random
sudo install -d -o rikachama -g rikachama /opt/rikachama/random/upload
sudo install -o rikachama -g rikachama -m 600 config.json.example /opt/rikachama/random/config.json
echo "PORT=3201" | sudo -u rikachama tee /opt/rikachama/random/.env
# Now edit /opt/rikachama/random/config.json — at minimum, set site_secret:
#   openssl rand -hex 32   (paste into the site_secret field)
# Also adjust title/home/etc. to taste.
cd /opt/rikachama/random/
sudo -u rikachama /opt/rikachama/rikachama -create-admin admin
```
It will ask for a password on stdin. Password will not echo. 

Finally, start with systemd.
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now rikachama@random
```

Note that all of these flags were checked by hand. A list of intentionally omitted tags and reasons why follow.

| Flag | Reason |
|------|--------|
| PrivateTmp=true | Unsupported upstream [podman issue #14106](https://github.com/containers/podman/issues/14106) |
| ProtectHome=true | Podman cannot access its runtime directories |
| MemoryDenyWriteExecute=true | gVisor cannot execute |
| ProtectHostname=true | Breaks container build |

Also, the podman flag ``--runtime-flag=ignore-cgroups`` must be used if using gVisor and rootless podman. This is the recommended configuration and the only one used during testing. Note that podman enforces its own resource limits with its own cgroups. The flag is set by default in the example config. I am not sure what exactly causes it but I believe it is related to [gVisor issue #11543](https://github.com/google/gvisor/issues/11543).

Currently rikachama uses fork+exec to invoke podman. Switching to the podman/docker socket API would clean up most of these quirks. This is planned for a future version.

