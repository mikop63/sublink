# sublink

**sublink** — self-hosted прокси, который объединяет несколько подписок [3x-ui](https://github.com/MHSanaei/3x-ui) в одну ссылку.

Вместо того чтобы давать пользователям несколько ссылок на подписки с разных VPN-панелей, они получают одну. sublink параллельно обходит все upstream-панели и склеивает конфиги в один ответ.

```
Клиент (V2RayNG / Shadowrocket / …)
        │
        │  GET https://your-server.com/api/TOKEN
        ▼
      sublink
        │
        ├──► https://vpn1.example.com/api/TOKEN  ─┐
        └──► https://vpn2.example.com/api/TOKEN  ─┘  (параллельно)
                                                   │
         склеенная base64-подписка  ◄───────────────┘
```

При открытии в браузере страница показывает **QR-код**, который VPN-приложения могут отсканировать напрямую.

---

## Требования

- Linux-сервер (VPS или локальная машина)
- **Docker** и **Docker Compose**

Установка Docker, если его ещё нет:

```bash
bash <(curl -sSL https://get.docker.com)
sudo systemctl enable --now docker
sudo usermod -aG docker $(whoami)
```

> После `usermod` нужно выйти из сессии и войти снова, чтобы изменения группы применились.

---

## Установка

### 1. Клонировать репозиторий

```bash
git clone https://github.com/mikop/sublink.git
cd sublink
```

### 2. Создать файл конфигурации

В репозитории есть `config.json.tmp` — шаблон конфигурации. Скопируй его и заполни своими значениями:

```bash
cp config.json.tmp config.json
```

Открой `config.json` в любом редакторе:

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
    "password": "сюда_впиши_надёжный_пароль"
  }
}
```

| Поле | Описание |
|---|---|
| `server.port` | Порт приложения (по умолчанию: `8080`) |
| `upstream.timeout_sec` | Таймаут запроса к каждому upstream в секундах |
| `upstream.update_interval` | Значение заголовка `Profile-Update-Interval` (в часах) |
| `upstream.hosts` | Список базовых URL панелей 3x-ui |
| `admin.username` | Логин для входа в админку |
| `admin.password` | **Обязательно смени перед запуском** |

> ⚠️ **Важно:** Смени `admin.password` на что-то надёжное. Конфиг хранится на диске в открытом виде — ограничь права на файл при необходимости.

### 3. Запустить

```bash
docker compose up -d
```

Готово. Сервис запущен.

---

## Использование

### Ссылка на подписку

Эту ссылку нужно добавить в VPN-приложение как подписку:

```
http://your-server:8080/api/TOKEN
```

`TOKEN` — токен подписки из твоей панели 3x-ui (тот же путь, что используется напрямую на панели).

#### В браузере

При открытии ссылки подписки в браузере отображается страница с:
- **QR-кодом** URL подписки — можно отсканировать напрямую в V2RayNG, Shadowrocket и т.д.
- Статистикой трафика (загрузка / выгрузка / квота / срок действия)
- Списком всех склеенных VLESS-конфигов (клик на любой — копирует)

#### В VPN-приложении

Клиенты получают стандартную base64-закодированную подписку со всеми конфигами, объединёнными со всех upstream-хостов.

### Админ-панель

```
http://your-server:8080/admin/
```

Войди с учётными данными из `config.json`. В админ-панели можно:

- Добавлять и удалять upstream-хосты
- Менять таймаут запросов и интервал обновления
- Менять логин и пароль администратора

Все изменения применяются сразу без перезапуска контейнера.

---

## HTTPS / SSL

В репозитории есть `nginx/nginx.conf.tmp` — шаблон конфига nginx. Скопируй его и пропиши пути к сертификатам:

```bash
cp nginx/nginx.conf.tmp nginx/nginx.conf
nano nginx/nginx.conf
```

Обнови эти две строки, указав свой домен и пути к сертификатам:

```nginx
ssl_certificate     /etc/nginx/certs/live/your.domain.com/fullchain.pem;
ssl_certificate_key /etc/nginx/certs/live/your.domain.com/privkey.pem;
```

Папка `certs/` монтируется в контейнер nginx (см. `docker-compose.yml`), поэтому положи сертификаты туда на хосте.

#### Получение сертификата через certbot

Установи certbot:

```bash
# Debian / Ubuntu
sudo apt install certbot

# RHEL / CentOS
sudo dnf install certbot
```

Выпусти сертификат для домена (80 порт должен быть открыт, домен должен смотреть на этот сервер):

```bash
sudo certbot certonly --standalone --agree-tos \
  --register-unsafely-without-email \
  -d your.domain.com
```

Certbot сохранит сертификаты в `/etc/letsencrypt/live/your.domain.com/`. Подключи эту папку в `docker-compose.yml`:

```yaml
volumes:
  - ./nginx/nginx.conf:/etc/nginx/nginx.conf:ro
  - /etc/letsencrypt:/etc/nginx/certs:ro
```

И пропиши пути в `nginx/nginx.conf`:

```nginx
ssl_certificate     /etc/nginx/certs/live/your.domain.com/fullchain.pem;
ssl_certificate_key /etc/nginx/certs/live/your.domain.com/privkey.pem;
```

Самоподписанный сертификат для локального IP-адреса:

```bash
mkdir certs
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
  -keyout certs/cert.key \
  -out certs/cert.crt \
  -subj "/CN=192.168.0.200" \
  -addext "subjectAltName=IP:192.168.0.200"
```

Затем используй `docker-compose.yml` с сервисом `nginx`.

---

## Структура проекта

```
.
├── Dockerfile
├── README.md
├── README_RU.md
├── certs/
│   ├── cert.crt          # SSL-сертификат
│   └── cert.key          # приватный ключ
├── cmd/
│   └── server/
│       └── main.go
├── compose.yml
├── config.json           # активный конфиг (создаётся из config.json.tmp)
├── config.json.tmp       # шаблон конфига — скопируй в config.json
├── go.mod
├── internal/
│   ├── admin/
│   │   └── admin.go      # админ-панель (HTML + API)
│   ├── aggregator/
│   │   └── aggregator.go # параллельный сборщик upstream-ов
│   ├── config/
│   │   └── config.go     # загрузка конфига и горячая перезагрузка
│   └── handler/
│       └── handler.go    # HTTP-обработчик + страница подписки
└── nginx/
    ├── nginx.conf         # активный конфиг nginx (создаётся из nginx.conf.tmp)
    └── nginx.conf.tmp     # шаблон конфига nginx — скопируй в nginx.conf
```

---

## Полезные команды

```bash
# Смотреть логи
docker compose logs -f

# Перезапустить
docker compose restart

# Остановить
docker compose down

# Пересобрать после изменений в коде
docker compose up -d --build
```