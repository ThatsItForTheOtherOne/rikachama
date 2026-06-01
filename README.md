# Rikachama
This is an imageboard I wrote in golang as a learning exercise. 
It uses podman/gVisor to securely handle files.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-create-admin <username>` | string | `""` | Create an admin account with the given username (prompts for password on stdin), then exit. |
| `-dev` | bool | `false` | Enable developer mode (disables `Secure` cookies for local HTTP). Never enable in production. |
| `-config <path>` | string | `config.json` | Path to the config file. Useful when running multiple boards from one binary. |
| `-port <n>` | int | `3200` | TCP port to bind. Must be 1-65535. Pair with `-config` to run multiple boards on one host. |
