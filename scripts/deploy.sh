#!/bin/bash
set -e

SERVER_USER="ec2-user"
SERVER_IP="13.234.34.188"
SERVER_DIR="/home/ec2-user/Approx/server"
PEM="$(cd "$(dirname "$0")" && pwd)/../server/LightsailDefaultKey-ap-south-1.pem"
SRC_DIR="$(cd "$(dirname "$0")" && pwd)/../server"

ssh_cmd() { ssh -i "$PEM" "$SERVER_USER@$SERVER_IP" "$@"; }
scp_cmd() { scp -i "$PEM" "$@"; }

echo "[build] Cross-compiling for Linux..."
cd "$SRC_DIR"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o approx-server .
echo "[build] Done — $(du -sh approx-server | cut -f1) binary"

echo "[server] Stopping old instance..."
ssh_cmd "pkill -f approx-server; sleep 1" || true

echo "[scp] Uploading binary..."
scp_cmd approx-server "$SERVER_USER@$SERVER_IP:$SERVER_DIR/"

echo "[scp] Uploading static files..."
scp_cmd -r static "$SERVER_USER@$SERVER_IP:$SERVER_DIR/"

echo "[scp] Done."

echo "[server] Starting..."
ssh_cmd "cd $SERVER_DIR && set -a && source .env && set +a && nohup ./approx-server > ~/approx.log 2>&1 & sleep 1 && curl -s localhost:8080/health"

echo ""
echo "Deployed to http://$SERVER_IP:8080"
