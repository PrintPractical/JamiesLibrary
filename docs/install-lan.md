# Installing Jamie's Library on a home or office network

This guide is for a library whose computer sits on its own network (a home, a school, a hall) and is reached by phones and computers on that same network. It needs no domain name and no internet connection. If the library must be reachable from the internet, read [Installing on the public internet](install-public.md) instead.

Contents: [Installation](#installation) · [Configuration](#configuration) · [Network access](#network-access) · [Security](#security) · [Maintenance](#maintenance)

## Installation

The server serves its pages over HTTPS from its first start. It makes its own local certificate authority for that, so a phone's camera can scan barcodes (browsers allow the camera only on a secure page). Each phone or computer does a one-time trust step, described under [Network access](#network-access).

### With Docker (recommended)

You need a 64-bit x86 or ARM (Raspberry Pi 4 or newer) **Linux** machine with Docker and the Compose plugin. The compose file uses host networking, which is a Linux feature: on Docker Desktop for Mac or Windows see [Without host networking](#without-host-networking) below.

```sh
git clone <this repository> jamieslibrary && cd jamieslibrary
docker compose -p jamieslibrary -f deploy/compose.lan.yml up -d --build
docker compose -p jamieslibrary -f deploy/compose.lan.yml logs
```

The log ends with the fingerprint of the local certificate and a one-time setup code:

```
Local CA root SHA-256 fingerprint: FF:F0:19:F1:4F:61:28:05:46:D3:32:B8:F6:EA:1F:A6:BF:CC:5D:77:4F:95:56:A7:B4:5E:DF:2D:C5:E1:59:D5
No administrator exists yet. To create the first one, open the app in a browser and enter this one-time code.
On a phone or another computer, first follow the trust guide: http://localhost:8080/trust/
SETUP CODE: VD0Y-CKSB-WK71-0NZM
```

Open `https://THIS-MACHINE:8443` (the machine's address or name, see [Network access](#network-access)) and enter the code to create the first administrator. The code is new at every start until an administrator exists, is never stored, and 10 wrong tries block an address for 15 minutes. After a restart, the newest `SETUP CODE` in the log is the one that works.

Use `-p jamieslibrary` on every compose command in this guide. Compose names the data volume after the project, which would otherwise be the folder name (`deploy`); with `-p jamieslibrary` it is `jamieslibrary_jamieslibrary-data`, which is the name the backup commands below use.

The container runs as user 65532 with a read-only root filesystem; only `/data` is writable. The image is built for `linux/amd64` and `linux/arm64`.

### Without host networking

Docker Desktop (Mac, Windows) and some hosts can't give a container the machine's own network. Then the server can't see the machine's real address, and the certificate it makes for connections by address would name only the container's private address. Do one of these:

- **Tell it the address.** Copy `deploy/compose.lan.yml`, remove `network_mode: host`, add `ports: ["8443:8443", "8080:8080"]`, and set `JL_ADDRESSES` to the machine's LAN address (for example `192.168.1.20`). Give the machine a fixed address in your router so this stays true.
- **Use a name instead of an address.** A name ending in `.local`, `.lan`, `.home.arpa` or `.internal` (for example `library.local`) needs no setting: the server makes a certificate for whatever such name the browser asks for. Your router, or the machine's own mDNS service (Bonjour on a Mac, Avahi on Linux), has to make the name point at the machine.

### As a downloaded program

Download the program for your system (`jamieslibrary-VERSION-linux-amd64`, `-linux-arm64` or `-darwin-arm64`; there is no Windows program) and its `SHA256SUMS`, then:

```sh
sha256sum -c SHA256SUMS --ignore-missing     # on a Mac: shasum -a 256 -c SHA256SUMS
chmod +x jamieslibrary-VERSION-linux-amd64
GOMEMLIMIT=400MiB ./jamieslibrary-VERSION-linux-amd64 --data /var/lib/jamieslibrary
```

Release files are built with `scripts/release.sh VERSION`; see the [README](../README.md). Or build it yourself from source as the README describes. On a small machine set `GOMEMLIMIT=400MiB`: a Raspberry Pi 4 with 20,000 copies then stays under 512 MB. The Docker image sets it already.

The program has no service manager built in. Run it the way you run other servers on that machine (systemd, launchd, a terminal in `tmux`). Stop it with Ctrl-C or SIGTERM: it lets running requests finish for up to 10 seconds.

## Configuration

Each setting is a command-line flag or a `JL_*` environment variable; a flag wins over its variable. A bad value stops the server and the message names the setting. With Docker, put variables under `environment:` in the compose file, then `up -d` again.

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--data` | `JL_DATA_DIR` | `./data` (`/data` in the image) | The data directory: everything the library keeps. |
| `--port` | `JL_HTTP_PORT` | `8080` | The plain-HTTP port. It serves only the trust guide and the certificate downloads, and sends everything else to HTTPS. |
| `--https-port` | `JL_HTTPS_PORT` | `8443` | The port the app is served on, over HTTPS. Must differ from the HTTP port. |
| `--bind` | `JL_BIND_ADDRESS` | empty: every interface | The IP address both ports listen on. |
| `--log-level` | `JL_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| | `JL_ADDRESSES` | empty | Extra local IP addresses to put in the certificate used for connections by address, separated by commas or spaces. For when the server can't see its own address. Only addresses in the private ranges below are used. |
| | `JL_HOSTNAMES` | empty | Your own host names for the library, such as `library.myschool.example`. **Read at the very first start only**; see below. |
| | `JL_TLS_CERT`, `JL_TLS_KEY` | empty | Your own certificate and key (PEM), to use instead of the local certificate authority. Both or neither. See [the public guide](install-public.md#your-own-certificate). |
| | `JL_HTTP_ONLY` | `0` | `1` serves plain HTTP only, for a reverse proxy. Not for a LAN install: see [the public guide](install-public.md). |
| | `JL_TRUSTED_PROXIES` | empty | Reverse proxies whose `X-Forwarded-For` is believed. Leave empty on a LAN. See [the public guide](install-public.md#behind-a-reverse-proxy). |
| | `GOMEMLIMIT` | none (`400MiB` in the image) | Go's memory limit. A hint to the runtime, not a hard cap. |

**What the certificate names.** Without any setting, the local certificate covers every address the machine itself has that is in a private range (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`, link-local and loopback, and IPv6 `fc00::/7`, `fe80::/10`, `::1`), plus `JL_ADDRESSES`; and, for a browser that asks by name, that name when it ends in `.local`, `.lan`, `.home.arpa`, `.internal` or `.localhost`.

**`JL_HOSTNAMES`** is for names outside those endings. The certificate authority is made at the first start with the list of names it may ever vouch for, and that list can't grow later: a name added afterwards is ignored and logged as a warning. So set `JL_HOSTNAMES` before the first start. A name needs at least two parts (`library.example.com`, not `library`). If you forgot, the only remedy is a new certificate authority, which means every device repeats the trust step: stop the server, move the `tls/` folder out of the data directory, make a new empty one (`mkdir -m 700 tls`), and start again. Without the empty folder the server refuses to start rather than quietly make a new authority.

## Network access

| Port | What | Open to |
|---|---|---|
| 8443/tcp | The app, over HTTPS | your network |
| 8080/tcp | Trust guide, certificate downloads and the redirect to HTTPS | your network |

Don't forward these ports from your router to the internet; use the [public guide](install-public.md) for that. If the machine has a firewall, allow the two ports from your network only (for example, with `ufw`: `ufw allow from 192.168.1.0/24 to any port 8443,8080 proto tcp`).

The app is at `https://ADDRESS:8443`, for example `https://192.168.1.20:8443` or `https://library.local:8443`. Opening the plain address `http://ADDRESS:8080` sends a browser to the HTTPS one.

By default the server listens on every network interface. To listen on one address only, set `JL_BIND_ADDRESS`.

### The trust step, once per phone or computer

A device that hasn't done the step opens the library with a browser warning, and the camera stays off. Typing codes, or a USB or Bluetooth barcode scanner, work without the step.

On each phone or computer, open `http://ADDRESS:8080/trust/` and follow the steps for that device (iPhone and iPad, Android, or Windows, Mac and Linux computers). The guide works from the first start; the address in the setup message is `localhost` unless you set `JL_HOSTNAMES` or `JL_ADDRESSES`, so use the machine's real address.

**Check that it is the right certificate.** Anyone on the network could swap the file you download for another. The steps tell you to compare the SHA-256 fingerprint your device shows for the certificate with the fingerprint printed on the server's console, in the line

```
Local CA root SHA-256 fingerprint: FF:F0:19:F1:...
```

(`docker compose -p jamieslibrary -f deploy/compose.lan.yml logs | grep fingerprint` shows it.) Compare the first 8 and last 8 characters. If they differ, don't trust it. The fingerprint on the trust page itself was fetched over plain HTTP and is only for reference.

**What your device trusts, plainly.** By doing the step you tell the device to believe certificates signed by this installation's own authority, and only for this installation's own names and for local-network names and addresses (names ending in `.local`, `.lan`, `.home.arpa`, `.internal` or `.localhost`, and addresses in the private ranges above, plus any names you set in `JL_HOSTNAMES`). That limit is written into the certificate itself, so a device refuses a certificate for a public website such as a bank's even if the authority's key were stolen. The authority's key lives on the server only: the data directory and its backups hold it, so keep them private ([Security](#security)).

**The step is done once.** It is not repeated when the server restarts, is upgraded, renews its certificate (a server certificate lasts about 13 months and is renewed 30 days before it ends; the authority's own certificate lasts 20 years), or gets a new address or name. It is repeated only if the `tls/` folder is lost (restore it from a backup) or you deliberately make a new authority.

## Security

- **The data directory is secret.** It holds the local certificate authority's key (`tls/intermediate.key`), every user's password hash and every patron's loan history and card number. Anyone who can read it, or a backup of it, can sign in as anyone and can make certificates that devices which did the trust step accept for local names. It is created readable by its owner only (`0700`, files `0600`). Keep it, and every backup, where only the library's administrator can read.
- **Keep the machine on your own network.** A LAN install is meant for people on that network. Don't forward its ports from your router. A plain-HTTP port is open next to the HTTPS one, but it serves only the trust guide and the redirect: no sign-in, no data, no cookies.
- **Sessions and sign-in limits.** Signed-in sessions end after 8 hours without use (an administrator can set 15 minutes to 30 days). Five wrong sign-ins from one address block that address for 15 minutes; the block never locks an account, so nobody can lock a user out by guessing.
- **Docker.** The container runs as an unprivileged user with a read-only root filesystem, no added privileges and no Linux capabilities.
- **A locked-out administrator** is recovered at the server, not on the web: see [Maintenance](#maintenance).

## Maintenance

Everything the library keeps is in the data directory (for Docker, the volume `jamieslibrary_jamieslibrary-data` mounted at `/data`); see [Data and upgrades](data-and-upgrades.md) for what is in it.

### Backup

**A backup is: stop the server, copy the whole data directory, start the server.** Copy only while it is stopped: a copy taken while it runs can miss what is still in the database's write-ahead log. The copy includes the certificate authority in `tls/`, which is what lets phones keep trusting a restored server.

With Docker (backups go in a folder only you can open):

```sh
mkdir -m 700 backups
docker compose -p jamieslibrary -f deploy/compose.lan.yml stop
docker run --rm -v jamieslibrary_jamieslibrary-data:/data:ro -v "$PWD/backups":/backup busybox \
  tar czf /backup/jamieslibrary-$(date +%Y%m%d).tar.gz -C /data .
docker compose -p jamieslibrary -f deploy/compose.lan.yml start
```

To restore into an empty volume (a new machine, say, after `up -d` has not yet been run there):

```sh
docker volume create jamieslibrary_jamieslibrary-data
docker run --rm -v jamieslibrary_jamieslibrary-data:/data -v "$PWD/backups":/backup:ro busybox \
  tar xzf /backup/jamieslibrary-20261115.tar.gz -C /data
docker compose -p jamieslibrary -f deploy/compose.lan.yml up -d --build
```

The restored server starts with the same certificate authority (the same fingerprint in its log), so no phone repeats the trust step. A tar archive keeps the files' owner (65532) and modes.

With the program: stop it, `cp -a data data-backup-$(date +%Y%m%d)`, start it. To restore, put the copy where `--data` points.

If a folder on the host replaces the Docker volume (`./library-data:/data`), it must be owned by user 65532 and be mode `0700`.

Keep backups private, exactly as the data directory: they hold the same secrets.

### Upgrade

1. **Make a copy first**, as under Backup. The server also saves the database in `backups/` inside the data directory before it changes it, but only the database, and only when the version changes how data is stored; your own copy covers everything.
2. Stop the server, install the new version, start it.
   - Docker: `git pull`, then `docker compose -p jamieslibrary -f deploy/compose.lan.yml up -d --build`. The volume, and with it every setting, user and certificate, is kept.
   - Program: stop it, replace the file with the new release (check it against `SHA256SUMS`), start it.
3. Check the log: `listening` with the new `version=` means it started. Phones don't need to do anything.

If the new version can't upgrade the data it prints one message and exits, having changed nothing; [Data and upgrades](data-and-upgrades.md) lists each message and what to do. Going back to an older version means restoring the copy you took in step 1: an older program refuses to start on data a newer one has upgraded.

### Locked-out administrator

On the server, with the same version that is running:

```sh
docker compose -p jamieslibrary -f deploy/compose.lan.yml exec jamieslibrary /usr/local/bin/jamieslibrary admin reset-password USERNAME
```

or `./jamieslibrary admin reset-password --data DIR USERNAME` for the program. It prints a temporary password of 16 characters, which that person must change when they next sign in, and ends their sessions. Add `--make-admin` to also give the account the administrator role (for an owner who was demoted).
