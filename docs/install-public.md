# Installing Jamie's Library on the public internet

This guide is for a library that people reach from outside, at a domain name such as `library.example.org`, from anywhere. It needs a server with a public address, a domain name pointing at it, and HTTPS with a certificate browsers already trust. For a library used only on its own network, see [Installing on a home or office network](install-lan.md).

Contents: [Installation](#installation) · [Configuration](#configuration) · [Network access](#network-access) · [Security](#security) · [Maintenance](#maintenance)

Two ways to get HTTPS are described, each [checked](#check-that-it-works) the same way. Pick one:

- **Behind a reverse proxy** (recommended; the compose file does it all with Caddy, which gets and renews a free certificate itself).
- **With your own certificate**, which the library serves itself.

## Installation

### Behind the Caddy reverse proxy, with Docker

You need a 64-bit x86 or ARM Linux server with Docker and the Compose plugin; a domain name whose DNS points at the server; and ports 80 and 443 free and open to the internet (Caddy needs both to get its certificate).

1. Put your domain name in `deploy/Caddyfile`, replacing `library.example.org` on its first line:

   ```
   library.example.org {
   	encode zstd gzip
   	reverse_proxy jamieslibrary:8080
   }
   ```

2. Start it, then read the setup code:

   ```sh
   git clone <this repository> jamieslibrary && cd jamieslibrary
   # edit deploy/Caddyfile first
   docker compose -p jamieslibrary -f deploy/compose.public.yml up -d --build
   docker compose -p jamieslibrary -f deploy/compose.public.yml logs jamieslibrary
   ```

   The log shows a one-time code:

   ```
   No administrator exists yet. To create the first one, open the app in a browser and enter this one-time code.
   SETUP CODE: 2B9G-QXQ3-05ZE-W76P
   listening addr=172.30.0.10:8080 tls=false ...
   ```

3. Open `https://library.example.org`, enter the code and create the first administrator. The code is new at every start until an administrator exists, is never stored, and ten wrong tries block an address for 15 minutes.

`deploy/compose.public.yml` runs two containers on a private network: the library (plain HTTP, port 8080, reachable only from that network) and Caddy (ports 80 and 443). It sets `JL_HTTP_ONLY`, `JL_BIND_ADDRESS` and `JL_TRUSTED_PROXIES` for you. Use `-p jamieslibrary` on every compose command, so the volumes are named `jamieslibrary_jamieslibrary-data` (the library's data), and `jamieslibrary_caddy-data` and `jamieslibrary_caddy-config` (Caddy's certificates).

No trust step is needed on any device: the certificate comes from a public authority. People use the library at `https://library.example.org` with no port number.

### Behind a reverse proxy on the same machine, without Docker

Run the program and any proxy that sets `X-Forwarded-For` (Caddy, nginx, Apache). The program listens for the proxy on a local port only:

```sh
JL_HTTP_ONLY=1 JL_BIND_ADDRESS=127.0.0.1 JL_TRUSTED_PROXIES=127.0.0.1 \
GOMEMLIMIT=400MiB ./jamieslibrary --data /var/lib/jamieslibrary
```

and in Caddy:

```
library.example.org {
	reverse_proxy 127.0.0.1:8080
}
```

Release files, and building from source, are described in the [README](../README.md). For another proxy see [Behind a reverse proxy](#behind-a-reverse-proxy) below for what it must do.

### Your own certificate

If you already have a certificate for the domain, the library can serve it itself without a proxy: set `JL_TLS_CERT` and `JL_TLS_KEY` to the PEM files. The certificate file must hold the whole chain (the server certificate first, then intermediates), and the key must belong to it.

**Getting a certificate.** Browsers trust a certificate only from a public authority. A free one is available from Let's Encrypt; for example, with certbot, on a server whose port 80 is free while it runs:

```sh
sudo certbot certonly --standalone -d library.example.org
```

This writes `/etc/letsencrypt/live/library.example.org/fullchain.pem` (the chain, for `JL_TLS_CERT`) and `privkey.pem` (for `JL_TLS_KEY`). Any other authority works the same way, as long as you get a PEM chain and key. A certificate from the library's own local authority is not for this: see [Why not the local authority](#why-not-the-local-authority).

```sh
JL_TLS_CERT=/etc/ssl/library/fullchain.pem JL_TLS_KEY=/etc/ssl/library/privkey.pem \
./jamieslibrary --data /var/lib/jamieslibrary --https-port 443
```

(A normal user can't listen on port 443 on Linux; either run the program with a way to do so, or let it listen on 8443 and forward 443 to it in the firewall or router.)

**The key is a secret.** Whoever reads `privkey.pem` can impersonate your library. It must never be readable by everyone: keep it mode `0400` or `0600`, owned by the user that runs the library, and don't put it in the data directory's backups or in a repository. With certbot, its `live/` files are only links into `archive/`: point the program at them directly, or copy the files as below, and in Docker don't mount only `live/`, because the links then lead nowhere.

With Docker, the container runs as user 65532, so that user must be able to read the key, without anyone else being able to. Give it a copy owned by that user:

```sh
sudo install -d -m 0755 /etc/ssl/library
sudo install -m 0644 /etc/letsencrypt/live/library.example.org/fullchain.pem /etc/ssl/library/fullchain.pem
sudo install -o 65532 -m 0400 /etc/letsencrypt/live/library.example.org/privkey.pem /etc/ssl/library/privkey.pem
```

(or leave the key as it is and give that user read access to it with `setfacl -m u:65532:r privkey.pem`; the directories above it need execute access for that user too). Don't `chmod 644` the key. If the library can't read it, `docker logs` shows `open /certs/privkey.pem: permission denied` and the container stops.

```sh
docker run -d --name jamieslibrary --restart unless-stopped \
  -p 443:8443 -v jamieslibrary-data:/data \
  -v /etc/ssl/library:/certs:ro \
  -e JL_TLS_CERT=/certs/fullchain.pem -e JL_TLS_KEY=/certs/privkey.pem \
  jamieslibrary:latest
```

(Build the image first: `docker build -f deploy/Dockerfile -t jamieslibrary .`)

**Renewal.** Certificates from Let's Encrypt last 90 days, and certbot renews them in the background. The library does not renew anything itself. When the files change on disk (a changed modification time or size, checked at most once a second when someone connects), it loads the new certificate for the next connection with no restart, and logs `reloaded the certificate`. After certbot renews, copy the files again if you use copies as above, for example in a certbot deploy hook (`--deploy-hook` or a script in `/etc/letsencrypt/renewal-hooks/deploy/`). Replace the certificate and the key together. If the new files can't be read, or the key doesn't belong to the certificate, the old certificate stays in use and a warning is logged (`the certificate files changed but can't be used; still serving the previous certificate`). A certificate that has expired is served anyway, with a warning in the log, and browsers will refuse it, so check that renewal works before the date.

With your own certificate the server needs no trust step, and the plain-HTTP port (8080) only redirects to HTTPS and shows a page saying no step is needed. You don't have to open it. The library also sends `Strict-Transport-Security` for a year.

The setup code goes to the console as described above: `docker logs jamieslibrary`.

### Why not the local authority

The local authority of a LAN install can't be used on the internet, whatever the name you give it. It signs only local names (such as `.local` and `.lan`) and private addresses, so no public domain name could be certified by it. Each device would also have to be set up to trust it by hand, and a device that does is trusting this installation's key. On the internet, use a certificate from a public authority, by Caddy or as above. The local authority isn't started when you set `JL_HTTP_ONLY` or your own certificate.

### Plain HTTP is never an option

The session cookie is always marked `Secure` (and named `__Host-jl_session`), in every mode. A browser doesn't keep or send such a cookie over plain `http://` (other than on `localhost`), so nobody could sign in. Even if one did, passwords would cross the internet in the clear. Serve the library to the public only through HTTPS, and leave the library's own plain-HTTP port (`JL_HTTP_ONLY`) reachable by the proxy alone.

### Check that it works

1. Open `https://library.example.org` in a browser: the page opens with the padlock, with no warning, and the certificate (padlock › certificate) names your domain and a public authority as its issuer.
2. Open `http://library.example.org`: the browser ends up at `https://library.example.org`. Or from a shell:

   ```sh
   curl -sI http://library.example.org/ | head -3     # 308 and a Location: https://... line (Caddy)
   curl -sI https://library.example.org/ | head -1     # a 200 or other answer, with no certificate error
   ```

   With your own certificate, the library's port 8080 answers `307` with the `Location: https://...` line, if you've opened it.
3. From another machine, check that `http://library.example.org:8080` gets no answer: the library's own port isn't published.

If a browser reports an invalid certificate, check that DNS points at this server and that ports 80 and 443 are open (Caddy: `docker compose -p jamieslibrary -f deploy/compose.public.yml logs caddy` says why it couldn't get one), or, with your own, that the chain file holds the intermediates and the certificate hasn't expired.

## Configuration

Each setting is a flag or a `JL_*` environment variable; a flag wins over its variable. A bad value stops the server and the message names the setting. With Docker, put variables under `environment:` in the compose file.

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--data` | `JL_DATA_DIR` | `./data` (`/data` in the image) | The data directory: everything the library keeps. |
| `--port` | `JL_HTTP_PORT` | `8080` | The plain-HTTP port. With `JL_HTTP_ONLY` it is the whole app, for the proxy; otherwise it only redirects to HTTPS. |
| `--https-port` | `JL_HTTPS_PORT` | `8443` | The HTTPS port, used only when the library serves HTTPS itself (your own certificate). Must differ from the HTTP port. |
| `--bind` | `JL_BIND_ADDRESS` | empty: every interface | The IP address the listeners bind to. For a proxy on the same machine use `127.0.0.1`; in Docker, the address on the proxy's network. |
| `--log-level` | `JL_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| | `JL_HTTP_ONLY` | `0` | `1` serves plain HTTP only, with no certificate of its own, for a reverse proxy. Don't use it without a proxy that adds HTTPS: sign-in needs a secure connection, and passwords would travel in the clear. Can't be combined with `JL_TLS_CERT`. |
| | `JL_TLS_CERT`, `JL_TLS_KEY` | empty | Your own certificate chain and key (PEM). Both or neither. |
| | `JL_TRUSTED_PROXIES` | empty | The reverse proxies whose `X-Forwarded-For` header is believed: CIDR ranges or bare addresses, separated by commas or spaces, for example `127.0.0.1` or `172.30.0.2/32`. A range that would trust everyone (`0.0.0.0/0`) is refused. See below. |
| | `JL_ADDRESSES`, `JL_HOSTNAMES` | empty | For the local certificate authority of a LAN install ([that guide](install-lan.md#configuration)). Not used with `JL_HTTP_ONLY` or your own certificate. |
| | `GOMEMLIMIT` | none (`400MiB` in the image) | Go's memory limit; a hint to the runtime, not a hard cap. |

With `JL_HTTP_ONLY=1` and no `JL_BIND_ADDRESS`, the server logs a warning that the app is on plain HTTP on every network interface.

### Behind a reverse proxy

The library limits wrong sign-ins (5 in a row from one address block it for 15 minutes) and wrong invitation or setup codes (10 in a row) **per client address**. Behind a proxy, every connection comes from the proxy, so the library must be told whom to believe about the visitor's real address:

- **`JL_TRUSTED_PROXIES` empty (the default):** the client address is the connection's own address. A forged `X-Forwarded-For` is ignored, so nobody can use it to dodge a block. Behind a proxy this makes all visitors one address, so five wrong sign-ins from anyone would block everyone.
- **`JL_TRUSTED_PROXIES` set to the proxy's address:** when the connection comes from a listed address, the client address is the right-most address in `X-Forwarded-For` that isn't itself listed. Only then is a block per visitor. IPv6 visitors are counted by their `/64`.

List **only the proxy's own address**, never a whole network that also holds other machines (in Docker the network's gateway is the host itself). The proxy must also set `X-Forwarded-For` to the address it saw, adding to a value the visitor sent rather than passing it through (Caddy's `reverse_proxy` does this by default and Caddy replaces a forged value from the internet), and nothing but the proxy may be able to reach the library's port. `compose.public.yml` does both: no published port, and Caddy at the fixed address `172.30.0.2`, the only one listed.

Checked on the compose file: five wrong sign-ins through Caddy, each with a different forged `X-Forwarded-For`, were answered `401` five times and the sixth `429 Too many attempts, try again later.` (with a `Retry-After` header), because the limit counted the one real address. With `JL_TRUSTED_PROXIES=127.0.0.1` on a bare program, five failures with `X-Forwarded-For: 1.1.1.1` blocked only `1.1.1.1`, and a visitor at `2.2.2.2` still got `401`.

The limits are kept in memory: restarting the server clears them.

## Network access

| Port | What | Open to |
|---|---|---|
| 443/tcp | HTTPS, answered by Caddy (or by the library, with your own certificate) | the internet |
| 80/tcp | Caddy gets its certificate with it, and sends visitors to HTTPS | the internet |
| 8080/tcp | The library's own plain-HTTP port in the compose setup | nobody: it isn't published; Caddy reaches it on the private network |

Point the domain's DNS `A` (and `AAAA`) record at the server before you start; Caddy asks for a certificate at its first start and again when it renews, and it can't without ports 80 and 443 reachable. Set the firewall to allow 80 and 443 and nothing else except what you need for administration (such as SSH). For example, with `ufw`: `ufw allow 80,443/tcp`. Note that Docker publishes ports by editing the firewall itself and can bypass `ufw` rules for published ports, so check from outside the machine that only 80 and 443 answer.

If you run the program without Docker behind a proxy on the same machine, set `JL_BIND_ADDRESS=127.0.0.1` so only the proxy can reach it, and don't open its port.

With your own certificate and no proxy, open the HTTPS port (443, or 8443 if people type the port).

## Security

- **The data directory is secret.** It holds every user's password hash, every patron's loan history and card number, and the images. It is created readable by its owner only (`0700`, files `0600`). Keep it, and every backup, where only the library's administrator can read.
- **HTTPS everywhere.** The session cookie is marked `Secure` and `HttpOnly`, `SameSite=Lax`, and named `__Host-jl_session`, so browsers send it only over HTTPS. Every response carries security headers (a strict Content-Security-Policy, `nosniff`, no referrer). When the library serves HTTPS itself (your own certificate) it also sends `Strict-Transport-Security` for a year; behind a proxy it doesn't, so add that header in the proxy if you want it. State-changing API calls take only `application/json`, and a cross-origin `POST` is refused with `403`.
- **Throttling.** Five wrong sign-ins in a row from one address block that address for 15 minutes; ten wrong invitation or setup codes block registration or setup from it for 15 minutes. A block is on the address, never on an account, so nobody can lock another user out. These limits are only as good as `JL_TRUSTED_PROXIES` ([above](#behind-a-reverse-proxy)).
- **Sessions** end after 8 hours without use (an administrator can set 15 minutes to 30 days).
- **Passwords** are 10 to 256 characters; the library keeps only a hash.
- **Firewall.** Open only 80 and 443. Don't publish the library's own port (8080) to the internet: in `compose.public.yml` it is deliberately not published.
- **Keep the server updated:** the operating system, Docker, and the Caddy image (its tag is set in `compose.public.yml`; change the tag to take a newer one, and pull it with `docker compose -p jamieslibrary -f deploy/compose.public.yml pull caddy`).
- **A locked-out administrator** is recovered at the server, not on the web: see [Maintenance](#maintenance).

## Maintenance

Everything the library keeps is in the data directory (with Docker, the volume `jamieslibrary_jamieslibrary-data` at `/data`). See [Data and upgrades](data-and-upgrades.md) for its contents. Caddy's certificates are in two other volumes and are re-fetched by Caddy if lost.

### Backup

**A backup is: stop the server, copy the whole data directory, start the server.** Copy only while it is stopped: a copy taken while it runs can miss what is still in the database's write-ahead log.

```sh
mkdir -m 700 backups
docker compose -p jamieslibrary -f deploy/compose.public.yml stop jamieslibrary
docker run --rm -v jamieslibrary_jamieslibrary-data:/data:ro -v "$PWD/backups":/backup busybox \
  tar czf /backup/jamieslibrary-$(date +%Y%m%d).tar.gz -C /data .
docker compose -p jamieslibrary -f deploy/compose.public.yml start jamieslibrary
```

To restore into an empty volume:

```sh
docker compose -p jamieslibrary -f deploy/compose.public.yml down
docker volume rm jamieslibrary_jamieslibrary-data        # only if it holds data you mean to replace
docker volume create jamieslibrary_jamieslibrary-data
docker run --rm -v jamieslibrary_jamieslibrary-data:/data -v "$PWD/backups":/backup:ro busybox \
  tar xzf /backup/jamieslibrary-20261115.tar.gz -C /data
docker compose -p jamieslibrary -f deploy/compose.public.yml up -d
```

Without Docker: stop the program, `cp -a data data-backup-$(date +%Y%m%d)`, start it. A host folder used in place of the volume must be owned by user 65532 and be mode `0700`.

Take backups regularly, and keep some off the server, in a place only the administrator can read: they hold the same secrets as the data directory. Try a restore before you need it.

### Upgrade

1. **Make a copy first**, as above. The server also saves the database in `backups/` inside the data directory before it changes how data is stored, but only the database, and only when the version needs it.
2. Install the new version.
   - Docker: `git pull`, then `docker compose -p jamieslibrary -f deploy/compose.public.yml up -d --build`.
   - Program: stop it, replace the file with the new release (check it against `SHA256SUMS`), start it.
3. Check that it started: `docker compose -p jamieslibrary -f deploy/compose.public.yml logs jamieslibrary` shows `listening` with the new `version=`.

If the new version can't upgrade the data, it prints one message and exits having changed nothing: [Data and upgrades](data-and-upgrades.md) lists each message and what to do. To go back to an older version, restore the copy from step 1; an older program refuses to start on data a newer one has upgraded.

### Locked-out administrator

On the server, with the same version that is running:

```sh
docker compose -p jamieslibrary -f deploy/compose.public.yml exec jamieslibrary /usr/local/bin/jamieslibrary admin reset-password USERNAME
```

(or `./jamieslibrary admin reset-password --data DIR USERNAME` for the program). It prints a temporary password of 16 characters, which that person must change when they next sign in, and ends their sessions. `--make-admin` also gives the account the administrator role.
