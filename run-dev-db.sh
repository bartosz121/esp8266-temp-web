#!/bin/bash

set -euo pipefail

echo "go run . --host 127.0.0.1 --port 8080 --db-host localhost --db-port 5432 --db-user esp8266_user --db-pass esp8266_pass --db-name esp8266_db"

echo "Starting Postgres 18 container..."
docker run --rm -it \
  -e POSTGRES_DB=esp8266_db \
  -e POSTGRES_USER=esp8266_user \
  -e POSTGRES_PASSWORD=esp8266_pass \
  -p 5432:5432 \
  postgres:18
