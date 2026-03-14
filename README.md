# sublink

**sublink** is a self-hosted proxy that aggregates multiple [3x-ui](https://github.com/MHSanaei/3x-ui) VLESS subscription URLs into a single link.

Instead of giving users multiple subscription links from different VPN panels, they get one URL — sublink fetches all upstream panels in parallel and merges the configs into one response.

```
Client (V2RayNG / Shadowrocket / …)
        │
        │  GET https://your-server.com/api/TOKEN
        ▼
      sublink
        │
        ├──► https://vpn1.example.com/api/TOKEN  ─┐
        └──► https://vpn2.example.com/api/TOKEN  ─┘  (parallel)
                                                   │
              merged base64 subscription  ◄─────────┘
```

When opened in a browser, the page shows a **QR code** that VPN apps can scan directly.

---

## Requirements

- A Linux server (VPS or local machine)
- **Docker** and **Docker Compose**

Install Docker if you don't have it:

```bash
bash <(curl -sSL https://get.docker.com)
sudo systemctl enable --now docker
sudo usermod -aG docker $(whoami)
```

> After running `usermod`, log out and back in for the group change to take effect.

---

## Installation

### 1. Clone the repository

```bash
git clone https://github.com/mikop/sublink.git
cd sublink
```

### 2. Create the config file

The repository includes `config.json.tmp` as a template. Copy it and fill in your values:

```bash
cp config.json.tmp config.json
```

Open `config.json` in any editor:

```bash
nano config.json
```

```json
{
  "server": {
    "port": 8080
  },
  "upstream": {
    "timeout_sec": 15,
    "update_interval": 1,
    "hosts": [
      "https://vpn1.example.com",
      "https://vpn2.example.com"
    ]
  },
  "admin": {
    "username": "admin",
    "password": "your_strong_password_here"
  }
}
```

| Field | Description |
|---|---|
| `server.port` | Port the app listens on (default: `8080`) |
| `upstream.timeout_sec` | Timeout per upstream request in seconds |
| `upstream.update_interval` | `Profile-Update-Interval` header value (hours) |
| `upstream.hosts` | List of upstream 3x-ui panel base URLs |
| `admin.username` | Admin panel login |
| `admin.password` | **Change this before starting** |

> ⚠️ **Important:** Change `admin.password` to something strong. The config is stored in plain text on disk — keep the file permissions restricted.

### 3. Start

```bash
docker compose up -d
```

That's it. The service is now running.

---

## Usage

### Subscription URL

Give users this URL — they paste it into their VPN app as a subscription:

```
http://your-server:8080/api/TOKEN
```

Where `TOKEN` is the subscription token from your 3x-ui panel (the same path you'd use on the panel directly).

#### In a browser

Opening the subscription URL in a browser shows a page with:
- A **QR code** of the subscription URL — scan it directly with V2RayNG, Shadowrocket, etc.
- Traffic statistics (upload / download / quota / expiry)
- A list of all merged VLESS configs (click any to copy)

#### In a VPN app

VPN clients receive a standard base64-encoded subscription with all configs merged from every upstream host.

### Admin panel

```
http://your-server:8080/admin/
```

Log in with the credentials from `config.json`. From the admin panel you can:

- Add or remove upstream hosts
- Change request timeout and update interval
- Change the admin username and password

All changes take effect immediately without restarting the container.

---

## HTTPS / SSL

The repository includes `nginx/nginx.conf.tmp` — a template nginx config. Copy it and edit the certificate paths before starting:

```bash
cp nginx/nginx.conf.tmp nginx/nginx.conf
nano nginx/nginx.conf
```

Update these two lines with your actual domain and certificate paths:

```nginx
ssl_certificate     /etc/nginx/certs/live/your.domain.com/fullchain.pem;
ssl_certificate_key /etc/nginx/certs/live/your.domain.com/privkey.pem;
```

The `certs/` folder is mounted into the nginx container (see `docker-compose.yml`), so place your certificates there on the host.

#### Obtaining a certificate with certbot

Install certbot:

```bash
# Debian / Ubuntu
sudo apt install certbot

# RHEL / CentOS
sudo dnf install certbot
```

Issue a certificate for your domain (port 80 must be open and the domain must point to this server):

```bash
sudo certbot certonly --standalone --agree-tos \
  --register-unsafely-without-email \
  -d your.domain.com
```

Certbot saves the certificates to `/etc/letsencrypt/live/your.domain.com/`. Mount that directory into the container by editing the `nginx` volumes in `docker-compose.yml`:

```yaml
volumes:
  - ./nginx/nginx.conf:/etc/nginx/nginx.conf:ro
  - /etc/letsencrypt:/etc/nginx/certs:ro
```

Then set the paths in `nginx/nginx.conf`:

```nginx
ssl_certificate     /etc/nginx/certs/live/your.domain.com/fullchain.pem;
ssl_certificate_key /etc/nginx/certs/live/your.domain.com/privkey.pem;
```

For a quick self-signed certificate for a local IP:

```bash
mkdir certs
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
  -keyout certs/cert.key \
  -out certs/cert.crt \
  -subj "/CN=192.168.0.200" \
  -addext "subjectAltName=IP:192.168.0.200"
```

Then use `docker-compose.yml` with the `nginx` service.

---

## Project structure

```
.
├── Dockerfile
├── README.md
├── README_RU.md
├── certs/
│   ├── cert.crt          # your SSL certificate
│   └── cert.key          # your SSL private key
├── cmd/
│   └── server/
│       └── main.go
├── compose.yml
├── config.json           # active config (created from config.json.tmp)
├── config.json.tmp       # config template — copy to config.json
├── go.mod
├── internal/
│   ├── admin/
│   │   └── admin.go      # admin panel (HTML + API)
│   ├── aggregator/
│   │   └── aggregator.go # parallel upstream fetcher
│   ├── config/
│   │   └── config.go     # config load / hot-reload
│   └── handler/
│       └── handler.go    # HTTP handler + subscription page
└── nginx/
    ├── nginx.conf         # active nginx config (created from nginx.conf.tmp)
    └── nginx.conf.tmp     # nginx config template — copy to nginx.conf
```

---

## Useful commands

```bash
# View logs
docker compose logs -f

# Restart
docker compose restart

# Stop
docker compose down

# Rebuild after code changes
docker compose up -d --build
```