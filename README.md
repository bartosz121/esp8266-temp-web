# ESP8266 Web

## Environment Variables

- `APP_HOST`
- `APP_PORT`
- `APP_DB_HOST`
- `APP_DB_PORT`
- `APP_DB_USER`
- `APP_DB_PASS`
- `APP_DB_NAME`

## Build

```bash
go build -o esp8266-web .
./esp8266-web
```

## Development

### Setup

```bash
git clone https://github.com/bartosz121/esp8266-temp-web
cd esp8266-temp-web
go mod download
```

### Running the Application

```bash
./run-dev-db.sh
go run . --db-user esp8266_user --db-pass esp8266_pass --db-name esp8266_db
```

### Running Tests

```bash
APP_DB_USER=esp8266_user APP_DB_PASS=esp8266_pass APP_DB_PORT=5432 go test -v
```
